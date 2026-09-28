package managedasset

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/semver"
)

type InstalledAsset struct {
	Name       string
	Version    string
	Platform   string
	URL        string
	SHA256     string
	Entrypoint string
	Path       string
}

func (m Manager) LatestInstalled(name, platform string) (InstalledAsset, error) {
	name, platform = strings.TrimSpace(name), strings.TrimSpace(platform)
	if !safeComponent(name) || !safeComponent(platform) {
		return InstalledAsset{}, errors.New("managed asset name and platform must be safe path components")
	}
	root := strings.TrimSpace(m.Root)
	if root == "" {
		return InstalledAsset{}, errors.New("managed asset root is required")
	}
	base := filepath.Join(root, name)
	entries, err := os.ReadDir(base)
	if err != nil {
		return InstalledAsset{}, err
	}
	versions := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && safeComponent(entry.Name()) {
			versions = append(versions, entry.Name())
		}
	}
	sort.SliceStable(versions, func(i, j int) bool {
		left, right := normalizeSemver(versions[i]), normalizeSemver(versions[j])
		if semver.IsValid(left) && semver.IsValid(right) {
			return semver.Compare(left, right) > 0
		}
		return versions[i] > versions[j]
	})
	var firstErr error
	for _, version := range versions {
		item, validateErr := m.validateInstalled(name, version, platform)
		if validateErr == nil {
			return item, nil
		}
		if firstErr == nil {
			firstErr = validateErr
		}
	}
	if firstErr != nil {
		return InstalledAsset{}, firstErr
	}
	return InstalledAsset{}, os.ErrNotExist
}

func (m Manager) RemoveAll(name string) (bool, error) {
	name = strings.TrimSpace(name)
	if !safeComponent(name) {
		return false, errors.New("managed asset name must be a safe path component")
	}
	root := strings.TrimSpace(m.Root)
	if root == "" {
		return false, errors.New("managed asset root is required")
	}
	target := filepath.Join(root, name)
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, errors.New("managed asset target is not a regular directory")
	}
	if err := os.RemoveAll(target); err != nil {
		return false, err
	}
	return true, nil
}

func (m Manager) RemoveOtherVersions(name, keepVersion string) error {
	name, keepVersion = strings.TrimSpace(name), strings.TrimSpace(keepVersion)
	if !safeComponent(name) || !safeComponent(keepVersion) {
		return errors.New("managed asset name and version must be safe path components")
	}
	root := strings.TrimSpace(m.Root)
	if root == "" {
		return errors.New("managed asset root is required")
	}
	base := filepath.Join(root, name)
	entries, err := os.ReadDir(base)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == keepVersion || !safeComponent(entry.Name()) {
			continue
		}
		target := filepath.Join(base, entry.Name())
		info, statErr := os.Lstat(target)
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("managed asset version target is not a regular directory: %s", entry.Name())
		}
		if err := os.RemoveAll(target); err != nil {
			return err
		}
	}
	return nil
}

func (m Manager) validateInstalled(name, version, platform string) (InstalledAsset, error) {
	target := filepath.Join(strings.TrimSpace(m.Root), name, version, platform)
	data, err := readLimitedFile(filepath.Join(target, "manifest.json"), maxManifestBytes)
	if err != nil {
		return InstalledAsset{}, err
	}
	var value manifest
	if err := json.Unmarshal(data, &value); err != nil {
		return InstalledAsset{}, err
	}
	if value.Schema != manifestSchema || value.Name != name || value.Version != version || value.Platform != platform || !safeArchiveEntrypoint(value.Entrypoint) {
		return InstalledAsset{}, errors.New("managed asset manifest identity is invalid")
	}
	path := filepath.Join(target, filepath.FromSlash(value.Entrypoint))
	info, err := os.Lstat(path)
	if err != nil {
		return InstalledAsset{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 {
		return InstalledAsset{}, errors.New("managed asset executable is not a non-empty regular file")
	}
	if runtimeExecutableRequired(platform) && info.Mode().Perm()&0111 == 0 {
		return InstalledAsset{}, errors.New("managed asset executable is not executable")
	}
	actual, err := hashFile(path)
	if err != nil {
		return InstalledAsset{}, err
	}
	if value.BinarySHA256 == "" || !strings.EqualFold(actual, value.BinarySHA256) {
		return InstalledAsset{}, errors.New("managed asset executable checksum mismatch")
	}
	return InstalledAsset{
		Name: value.Name, Version: value.Version, Platform: value.Platform, URL: value.URL,
		SHA256: value.ArchiveSHA, Entrypoint: value.Entrypoint, Path: path,
	}, nil
}

func safeArchiveEntrypoint(value string) bool {
	clean, err := safeArchivePath(value)
	return err == nil && clean == filepath.ToSlash(strings.TrimSpace(value))
}

func runtimeExecutableRequired(platform string) bool {
	return !strings.HasPrefix(strings.ToLower(strings.TrimSpace(platform)), "windows-")
}

func normalizeSemver(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if !strings.HasPrefix(value, "v") {
		value = "v" + value
	}
	return value
}
