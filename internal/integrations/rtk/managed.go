package rtk

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	managedManifestSchema = 1
	maxManagedDownload    = int64(256 << 20)
	maxManagedExtracted   = int64(256 << 20)
	maxManagedEntries     = 4096
	maxManagedManifest    = int64(64 << 10)
	staleManagedStageAge  = time.Hour
)

type managedManifest struct {
	Schema       int        `json:"schema"`
	Name         string     `json:"name"`
	Version      string     `json:"version"`
	Platform     string     `json:"platform"`
	URL          string     `json:"url"`
	ArchiveSHA   string     `json:"archive_sha256"`
	BinarySHA256 string     `json:"binary_sha256"`
	Entrypoint   string     `json:"entrypoint"`
	Signature    *Signature `json:"signature,omitempty"`
}

func (m *Manager) managedPath(platform Platform) string {
	name := platform.Portable.Entrypoint
	return filepath.Join(m.managedRoot, "rtk", Version, m.goos+"-"+m.goarch, name)
}

func (m *Manager) validateManaged(platform Platform) (string, error) {
	if m == nil || strings.TrimSpace(m.managedRoot) == "" {
		return "", os.ErrNotExist
	}
	if err := validatePortable(platform.Portable); err != nil {
		return "", err
	}
	path := m.managedPath(platform)
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", os.ErrNotExist
		}
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("managed RTK executable must not be a symlink")
	}
	if err := validateManagedExecutable(path); err != nil {
		return "", err
	}
	manifestPath := filepath.Join(filepath.Dir(path), "manifest.json")
	data, err := readLimitedFile(manifestPath, maxManagedManifest)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", os.ErrNotExist
		}
		return "", fmt.Errorf("read managed RTK manifest: %w", err)
	}
	var manifest managedManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", fmt.Errorf("decode managed RTK manifest: %w", err)
	}
	wantPlatform := m.goos + "-" + m.goarch
	portable := platform.Portable
	if manifest.Schema != managedManifestSchema ||
		manifest.Name != "rtk" ||
		manifest.Version != Version ||
		manifest.Platform != wantPlatform ||
		manifest.URL != portable.URL ||
		!strings.EqualFold(manifest.ArchiveSHA, portable.SHA256) ||
		manifest.Entrypoint != portable.Entrypoint ||
		!equalSignature(manifest.Signature, portable.Signature) {
		return "", errors.New("managed RTK manifest does not match pinned asset metadata")
	}
	binarySHA, err := hashFile(path)
	if err != nil {
		return "", err
	}
	if manifest.BinarySHA256 == "" || !strings.EqualFold(manifest.BinarySHA256, binarySHA) {
		return "", errors.New("managed RTK executable checksum mismatch")
	}
	return path, nil
}

func (m *Manager) installManaged(ctx context.Context, platform Platform) (string, error) {
	if strings.TrimSpace(m.managedRoot) == "" {
		return "", errors.New("managed RTK root is required")
	}
	portable := platform.Portable
	if err := validatePortable(portable); err != nil {
		return "", err
	}
	target := filepath.Dir(m.managedPath(platform))
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return "", err
	}
	pruneStaleManagedStages(parent, time.Now())
	stage, err := os.MkdirTemp(parent, "."+filepath.Base(target)+".staging-")
	if err != nil {
		return "", err
	}
	keepStage := false
	defer func() {
		if !keepStage {
			_ = os.RemoveAll(stage)
		}
	}()

	archivePath := filepath.Join(stage, "asset."+strings.ReplaceAll(portable.Archive, ".", "-"))
	if err := downloadManaged(ctx, m.httpClient, portable.URL, archivePath); err != nil {
		return "", err
	}
	archiveSHA, err := hashFile(archivePath)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(archiveSHA, portable.SHA256) {
		return "", errors.New("managed RTK archive checksum mismatch")
	}
	if portable.Signature != nil {
		if m.signatureVerifier == nil {
			return "", errors.New("managed RTK asset declares a signature but no signature verifier is configured")
		}
		if err := m.signatureVerifier(ctx, archivePath, *portable.Signature, m.httpClient); err != nil {
			return "", fmt.Errorf("verify managed RTK signature: %w", err)
		}
	}

	payload := filepath.Join(stage, "payload")
	binary, err := extractEntrypoint(archivePath, payload, portable.Archive, portable.Entrypoint)
	if err != nil {
		return "", err
	}
	if err := validateManagedExecutable(binary); err != nil {
		return "", err
	}
	binarySHA, err := hashFile(binary)
	if err != nil {
		return "", err
	}
	manifest := managedManifest{
		Schema: managedManifestSchema, Name: "rtk", Version: Version, Platform: m.goos + "-" + m.goarch,
		URL: portable.URL, ArchiveSHA: strings.ToLower(portable.SHA256), BinarySHA256: binarySHA,
		Entrypoint: portable.Entrypoint, Signature: portable.Signature,
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(payload, "manifest.json"), append(data, '\n'), 0600); err != nil {
		return "", err
	}

	previous := filepath.Join(stage, "previous")
	hadPrevious := false
	if _, err := os.Lstat(target); err == nil {
		if err := os.Rename(target, previous); err != nil {
			return "", fmt.Errorf("backup managed RTK asset: %w", err)
		}
		hadPrevious = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.Rename(payload, target); err != nil {
		if hadPrevious {
			_ = os.Rename(previous, target)
		}
		return "", fmt.Errorf("activate managed RTK asset: %w", err)
	}
	validated, validateErr := m.validateManaged(platform)
	if validateErr == nil {
		return validated, nil
	}

	failed := filepath.Join(stage, "failed-active")
	if err := os.Rename(target, failed); err != nil {
		keepStage = true
		return "", errors.Join(fmt.Errorf("validate activated managed RTK asset: %w", validateErr), fmt.Errorf("quarantine failed managed RTK asset: %w", err))
	}
	if hadPrevious {
		if err := os.Rename(previous, target); err != nil {
			keepStage = true
			return "", errors.Join(fmt.Errorf("validate activated managed RTK asset: %w", validateErr), fmt.Errorf("restore previous managed RTK asset: %w", err))
		}
	}
	return "", fmt.Errorf("validate activated managed RTK asset: %w", validateErr)
}

func (m *Manager) pruneManagedVersions() error {
	if m == nil || strings.TrimSpace(m.managedRoot) == "" {
		return nil
	}
	base := filepath.Join(m.managedRoot, "rtk")
	entries, err := os.ReadDir(base)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == Version || !safeComponent(entry.Name()) {
			continue
		}
		path := filepath.Join(base, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("managed RTK version target is not a regular directory: %s", entry.Name())
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}

func pruneStaleManagedStages(parent string, now time.Time) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	cutoff := now.Add(-staleManagedStageAge)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), ".") || !strings.Contains(entry.Name(), ".staging-") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(parent, entry.Name()))
	}
}

func validatePortable(portable Portable) error {
	parsed, err := url.Parse(strings.TrimSpace(portable.URL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("managed RTK URL must use HTTPS")
	}
	if portable.Archive != "zip" && portable.Archive != "tar.gz" {
		return fmt.Errorf("unsupported managed RTK archive type %q", portable.Archive)
	}
	if !safeComponent(portable.Entrypoint) {
		return errors.New("managed RTK entrypoint must be a safe path component")
	}
	sha := strings.ToLower(strings.TrimSpace(portable.SHA256))
	decoded, err := hex.DecodeString(sha)
	if err != nil || len(decoded) != sha256.Size {
		return errors.New("managed RTK SHA-256 must be a 64-character hexadecimal digest")
	}
	if portable.Signature != nil {
		signatureURL, err := url.Parse(strings.TrimSpace(portable.Signature.URL))
		if err != nil || signatureURL.Scheme != "https" || signatureURL.Host == "" {
			return errors.New("managed RTK signature URL must use HTTPS")
		}
		if strings.TrimSpace(portable.Signature.Kind) == "" {
			return errors.New("managed RTK signature kind is required")
		}
	}
	return nil
}

func validateManagedExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return errors.New("RTK executable must be a non-empty regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
		return errors.New("RTK executable is not executable")
	}
	return nil
}

func downloadManaged(ctx context.Context, base *http.Client, rawURL, destination string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("managed RTK URL must use HTTPS")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return err
	}
	client := secureHTTPClient(base)
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download managed RTK asset: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download managed RTK asset: server returned %s", response.Status)
	}
	if response.ContentLength > maxManagedDownload {
		return fmt.Errorf("managed RTK download exceeds %d-byte limit", maxManagedDownload)
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(destination)
		}
	}()
	written, err := io.Copy(file, io.LimitReader(response.Body, maxManagedDownload+1))
	if err != nil {
		return fmt.Errorf("download managed RTK asset: %w", err)
	}
	if written == 0 {
		return errors.New("managed RTK download is empty")
	}
	if written > maxManagedDownload {
		return fmt.Errorf("managed RTK download exceeds %d-byte limit", maxManagedDownload)
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	keep = true
	return nil
}

func secureHTTPClient(base *http.Client) *http.Client {
	client := &http.Client{Timeout: 2 * time.Minute}
	if base != nil {
		*client = *base
		if client.Timeout <= 0 {
			client.Timeout = 2 * time.Minute
		}
	}
	previous := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != "https" {
			return errors.New("managed RTK redirect must use HTTPS")
		}
		if len(via) >= 10 {
			return errors.New("managed RTK redirect limit exceeded")
		}
		if previous != nil {
			return previous(request, via)
		}
		return nil
	}
	return client
}

func extractEntrypoint(archivePath, destination, archiveType, entrypoint string) (string, error) {
	if err := os.MkdirAll(destination, 0700); err != nil {
		return "", err
	}
	switch archiveType {
	case "zip":
		return extractZipEntrypoint(archivePath, destination, entrypoint)
	case "tar.gz":
		return extractTarEntrypoint(archivePath, destination, entrypoint)
	default:
		return "", fmt.Errorf("unsupported managed RTK archive type %q", archiveType)
	}
}

func extractZipEntrypoint(archivePath, destination, entrypoint string) (string, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", err
	}
	defer reader.Close()
	if len(reader.File) > maxManagedEntries {
		return "", errors.New("managed RTK archive has too many entries")
	}
	var total int64
	var result string
	seen := map[string]struct{}{}
	for _, entry := range reader.File {
		name, err := safeArchivePath(entry.Name)
		if err != nil {
			return "", err
		}
		if _, duplicate := seen[name]; duplicate {
			return "", fmt.Errorf("managed RTK archive contains duplicate entry %s", name)
		}
		seen[name] = struct{}{}
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 || !mode.IsRegular() && !mode.IsDir() {
			return "", fmt.Errorf("managed RTK archive contains unsupported entry type %s", name)
		}
		total, err = addExtractedBytes(total, entry.FileInfo().Size())
		if err != nil {
			return "", err
		}
		if mode.IsDir() || name != entrypoint {
			continue
		}
		stream, err := entry.Open()
		if err != nil {
			return "", err
		}
		result, err = writeEntrypoint(destination, entrypoint, stream, entry.FileInfo().Size())
		closeErr := stream.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	if result == "" {
		return "", fmt.Errorf("managed RTK archive is missing %s", entrypoint)
	}
	return result, nil
}

func extractTarEntrypoint(archivePath, destination, entrypoint string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return "", err
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	var total int64
	var entries int
	var result string
	seen := map[string]struct{}{}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		entries++
		if entries > maxManagedEntries {
			return "", errors.New("managed RTK archive has too many entries")
		}
		name, err := safeArchivePath(header.Name)
		if err != nil {
			return "", err
		}
		if _, duplicate := seen[name]; duplicate {
			return "", fmt.Errorf("managed RTK archive contains duplicate entry %s", name)
		}
		seen[name] = struct{}{}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir {
			return "", fmt.Errorf("managed RTK archive contains unsupported entry type %s", name)
		}
		total, err = addExtractedBytes(total, header.Size)
		if err != nil {
			return "", err
		}
		if header.Typeflag == tar.TypeDir || name != entrypoint {
			continue
		}
		result, err = writeEntrypoint(destination, entrypoint, reader, header.Size)
		if err != nil {
			return "", err
		}
	}
	if result == "" {
		return "", fmt.Errorf("managed RTK archive is missing %s", entrypoint)
	}
	return result, nil
}

func writeEntrypoint(destination, entrypoint string, source io.Reader, size int64) (string, error) {
	if size <= 0 || size > maxManagedExtracted {
		return "", errors.New("managed RTK entrypoint has invalid size")
	}
	path := filepath.Join(destination, filepath.FromSlash(entrypoint))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	if err != nil {
		return "", err
	}
	written, copyErr := io.Copy(file, io.LimitReader(source, size+1))
	closeErr := file.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if written != size {
		return "", errors.New("managed RTK entrypoint size mismatch")
	}
	return path, nil
}

func safeArchivePath(value string) (string, error) {
	value = filepath.ToSlash(strings.TrimSpace(value))
	clean := filepath.ToSlash(filepath.Clean(value))
	if value == "" || clean == "." || strings.HasPrefix(clean, "../") || clean == ".." || strings.HasPrefix(clean, "/") || filepath.IsAbs(value) {
		return "", fmt.Errorf("unsafe managed RTK archive path %q", value)
	}
	return clean, nil
}

func safeComponent(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && value != "." && value != ".." && filepath.Base(value) == value && !strings.ContainsAny(value, "/\\")
}

func addExtractedBytes(total, size int64) (int64, error) {
	if size < 0 || total > maxManagedExtracted-size {
		return 0, errors.New("managed RTK archive exceeds extracted size limit")
	}
	return total + size, nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func readLimitedFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("managed RTK metadata is not a bounded regular non-symlink file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, limit+1))
}

func equalSignature(left, right *Signature) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Kind == right.Kind && left.URL == right.URL && left.Identity == right.Identity
}
