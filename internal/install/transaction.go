package install

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var (
	ErrCurrentNotManaged = errors.New("current install target is not managed by CodeMCP")
	ErrVersionConflict   = errors.New("install version already exists with different binary content")
)

const staleInstallStageAge = 24 * time.Hour

type Staged struct {
	Layout  Layout
	Version string
	Dir     string
	Binary  string
	Reused  bool
}

type Activation struct {
	Layout          Layout
	Version         string
	PreviousVersion string
	PreviousTarget  string
	CurrentTarget   string
}

func Stage(layout Layout, version, source string) (Staged, error) {
	version = strings.TrimSpace(version)
	finalDir, err := layout.VersionDir(version)
	if err != nil {
		return Staged{}, err
	}
	finalBinary, err := layout.VersionBinary(version)
	if err != nil {
		return Staged{}, err
	}
	source, err = resolveSourceBinary(source)
	if err != nil {
		return Staged{}, err
	}
	if info, statErr := os.Stat(finalBinary); statErr == nil {
		if !info.Mode().IsRegular() {
			return Staged{}, fmt.Errorf("installed binary is not a regular file: %s", finalBinary)
		}
		equal, compareErr := sameFileContent(source, finalBinary)
		if compareErr != nil {
			return Staged{}, compareErr
		}
		if !equal {
			return Staged{}, fmt.Errorf("%w: %s", ErrVersionConflict, version)
		}
		return Staged{Layout: layout, Version: version, Dir: finalDir, Binary: finalBinary, Reused: true}, nil
	} else if !os.IsNotExist(statErr) {
		return Staged{}, statErr
	}
	if _, statErr := os.Lstat(finalDir); statErr == nil {
		return Staged{}, fmt.Errorf("install version directory exists without a valid binary: %s", finalDir)
	} else if !os.IsNotExist(statErr) {
		return Staged{}, statErr
	}
	if err := os.MkdirAll(layout.Versions, 0755); err != nil {
		return Staged{}, err
	}
	pruneStaleInstallStages(layout.Versions, time.Now())
	staging, err := os.MkdirTemp(layout.Versions, ".staging-"+version+"-")
	if err != nil {
		return Staged{}, err
	}
	defer os.RemoveAll(staging)
	stagingBinary := filepath.Join(staging, layout.BinaryName)
	if err := copyBinary(source, stagingBinary); err != nil {
		return Staged{}, err
	}
	if err := os.Rename(staging, finalDir); err != nil {
		return Staged{}, fmt.Errorf("activate staged version directory: %w", err)
	}
	return Staged{Layout: layout, Version: version, Dir: finalDir, Binary: finalBinary}, nil
}

func pruneStaleInstallStages(root string, now time.Time) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	cutoff := now.Add(-staleInstallStageAge)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), ".staging-") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		_ = removeAllInstallPath(filepath.Join(root, entry.Name()))
	}
}

func Activate(staged Staged) (Activation, error) {
	if _, err := os.Stat(staged.Binary); err != nil {
		return Activation{}, fmt.Errorf("staged binary unavailable: %w", err)
	}
	currentVersion, currentTarget, err := CurrentVersion(staged.Layout)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Activation{}, err
	}
	if currentVersion != "" {
		currentTarget, err = staged.Layout.VersionDir(currentVersion)
		if err != nil {
			return Activation{}, err
		}
	}
	if currentVersion == staged.Version {
		return Activation{Layout: staged.Layout, Version: staged.Version, PreviousVersion: currentVersion, PreviousTarget: currentTarget, CurrentTarget: staged.Dir}, nil
	}
	if err := switchCurrent(staged.Layout, staged.Dir); err != nil {
		return Activation{}, err
	}
	return Activation{Layout: staged.Layout, Version: staged.Version, PreviousVersion: currentVersion, PreviousTarget: currentTarget, CurrentTarget: staged.Dir}, nil
}

func Rollback(activation Activation) error {
	if activation.PreviousTarget == "" {
		return removeCurrent(activation.Layout)
	}
	version, err := versionFromTarget(activation.Layout, activation.PreviousTarget)
	if err != nil {
		return err
	}
	if activation.PreviousVersion != "" && version != activation.PreviousVersion {
		return fmt.Errorf("rollback target version mismatch: got %s, want %s", version, activation.PreviousVersion)
	}
	target, err := activation.Layout.VersionDir(version)
	if err != nil {
		return err
	}
	return switchCurrent(activation.Layout, target)
}

func CurrentVersion(layout Layout) (string, string, error) {
	target, err := currentTarget(layout)
	if err != nil {
		return "", "", err
	}
	version, err := versionFromTarget(layout, target)
	if err != nil {
		return "", "", err
	}
	return version, target, nil
}

func Cleanup(layout Layout, keepVersions ...string) error {
	keep := make(map[string]struct{}, len(keepVersions))
	for _, version := range keepVersions {
		dir, err := layout.VersionDir(version)
		if err != nil {
			return err
		}
		keep[filepath.Clean(dir)] = struct{}{}
	}
	entries, err := os.ReadDir(layout.Versions)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(layout.Versions, entry.Name())
		if _, ok := keep[filepath.Clean(path)]; ok {
			continue
		}
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".staging-") {
			if err := removeAllInstallPath(path); err != nil {
				return err
			}
		}
	}
	return nil
}

func removeAllInstallPath(path string) error {
	attempts := 1
	if runtime.GOOS == "windows" {
		attempts = 6
	}
	return removeAllWithRetry(path, attempts, 25*time.Millisecond, os.RemoveAll, time.Sleep)
}

func removeAllWithRetry(path string, attempts int, delay time.Duration, remove func(string) error, sleep func(time.Duration)) error {
	attempts = max(1, attempts)
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		if err = remove(path); err == nil {
			return nil
		}
		if attempt+1 < attempts && delay > 0 {
			sleep(delay)
			delay *= 2
		}
	}
	return err
}

func resolveSourceBinary(source string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", errors.New("source binary is required")
	}
	absolute, err := filepath.Abs(source)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve source binary: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("source binary is not a regular file: %s", resolved)
	}
	if info.Size() == 0 {
		return "", errors.New("source binary is empty")
	}
	return filepath.Clean(resolved), nil
}

func copyBinary(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = output.Close()
		if !ok {
			_ = os.Remove(destination)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := output.Chmod(0755); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

func sameFileContent(left, right string) (bool, error) {
	leftHash, err := fileHash(left)
	if err != nil {
		return false, err
	}
	rightHash, err := fileHash(right)
	if err != nil {
		return false, err
	}
	return leftHash == rightHash, nil
}

func fileHash(path string) ([sha256.Size]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return [sha256.Size]byte{}, err
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result, nil
}

func versionFromTarget(layout Layout, target string) (string, error) {
	target = filepath.Clean(target)
	if version, ok := directVersionFromPath(layout, layout.Versions, target); ok {
		return version, nil
	}
	resolvedVersions, versionsErr := filepath.EvalSymlinks(layout.Versions)
	resolvedTarget, targetErr := filepath.EvalSymlinks(target)
	if versionsErr == nil && targetErr == nil {
		if version, ok := directVersionFromPath(layout, resolvedVersions, resolvedTarget); ok {
			return version, nil
		}
	}
	targetInfo, statErr := os.Stat(target)
	entries, readErr := os.ReadDir(layout.Versions)
	if statErr == nil && readErr == nil {
		for _, entry := range entries {
			candidate, err := layout.VersionDir(entry.Name())
			if err != nil {
				continue
			}
			candidateInfo, err := os.Stat(candidate)
			if err == nil && os.SameFile(targetInfo, candidateInfo) {
				return entry.Name(), nil
			}
		}
	}
	return "", fmt.Errorf("%w: %s", ErrCurrentNotManaged, target)
}

func directVersionFromPath(layout Layout, versionsRoot, target string) (string, bool) {
	relative, err := filepath.Rel(filepath.Clean(versionsRoot), filepath.Clean(target))
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.Dir(relative) != "." {
		return "", false
	}
	if _, err := layout.VersionDir(relative); err != nil {
		return "", false
	}
	return relative, true
}
