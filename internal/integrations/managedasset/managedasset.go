package managedasset

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
	"sort"
	"strings"
	"time"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const (
	manifestSchema        = 1
	defaultDownloadLimit  = int64(256 << 20)
	maxExtractedBytes     = int64(256 << 20)
	maxArchiveEntries     = 4096
	maxManifestBytes      = int64(64 << 10)
	maxTreeDownloadLimit  = int64(512 << 20)
	maxTreeExtractedBytes = int64(1 << 30)
	maxTreeArchiveEntries = 32768
	maxTreeManifestBytes  = int64(8 << 20)
)

type Spec struct {
	Name       string
	Version    string
	Platform   string
	URL        string
	SHA256     string
	Archive    string
	Entrypoint string
}

type TreeSpec struct {
	Name       string
	Version    string
	Platform   string
	URL        string
	SHA256     string
	Archive    string
	Entrypoint string
	Required   []string
}

type Manager struct {
	Root          string
	HTTPClient    *http.Client
	DownloadLimit int64
	rename        func(string, string) error
	postActivate  func(Spec) (string, error)
}

type manifest struct {
	Schema       int    `json:"schema"`
	Name         string `json:"name"`
	Version      string `json:"version"`
	Platform     string `json:"platform"`
	URL          string `json:"url"`
	ArchiveSHA   string `json:"archive_sha256"`
	BinarySHA256 string `json:"binary_sha256"`
	Entrypoint   string `json:"entrypoint"`
}

type treeManifest struct {
	Schema     int            `json:"schema"`
	Name       string         `json:"name"`
	Version    string         `json:"version"`
	Platform   string         `json:"platform"`
	URL        string         `json:"url"`
	ArchiveSHA string         `json:"archive_sha256"`
	Entrypoint string         `json:"entrypoint"`
	Files      []treeFileHash `json:"files"`
}

type treeFileHash struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func (s Spec) Validate() error {
	for label, value := range map[string]string{"name": s.Name, "version": s.Version, "platform": s.Platform, "entrypoint": s.Entrypoint} {
		if !safeComponent(value) {
			return fmt.Errorf("managed asset %s must be a single safe path component", label)
		}
	}
	parsed, err := url.Parse(strings.TrimSpace(s.URL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("managed asset URL must use HTTPS")
	}
	if s.Archive != "zip" && s.Archive != "tar.gz" {
		return fmt.Errorf("unsupported managed asset archive type %q", s.Archive)
	}
	sha := strings.ToLower(strings.TrimSpace(s.SHA256))
	decoded, err := hex.DecodeString(sha)
	if err != nil || len(decoded) != sha256.Size {
		return errors.New("managed asset SHA-256 must be a 64-character hexadecimal digest")
	}
	return nil
}

func (s TreeSpec) Validate() error {
	for label, value := range map[string]string{"name": s.Name, "version": s.Version, "platform": s.Platform} {
		if !safeComponent(value) {
			return fmt.Errorf("managed tree %s must be a single safe path component", label)
		}
	}
	entrypoint, err := safeArchivePath(s.Entrypoint)
	if err != nil || entrypoint != filepath.ToSlash(strings.TrimSpace(s.Entrypoint)) {
		return errors.New("managed tree entrypoint must be a safe relative path")
	}
	for _, required := range s.Required {
		clean, err := safeArchivePath(required)
		if err != nil || clean != filepath.ToSlash(strings.TrimSpace(required)) {
			return fmt.Errorf("managed tree required path must be safe: %q", required)
		}
	}
	parsed, err := url.Parse(strings.TrimSpace(s.URL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("managed tree URL must use HTTPS")
	}
	if s.Archive != "zip" && s.Archive != "tar.gz" {
		return fmt.Errorf("unsupported managed tree archive type %q", s.Archive)
	}
	sha := strings.ToLower(strings.TrimSpace(s.SHA256))
	decoded, err := hex.DecodeString(sha)
	if err != nil || len(decoded) != sha256.Size {
		return errors.New("managed tree SHA-256 must be a 64-character hexadecimal digest")
	}
	return nil
}

func (m Manager) Path(spec Spec) (string, error) {
	if err := spec.Validate(); err != nil {
		return "", err
	}
	root := strings.TrimSpace(m.Root)
	if root == "" {
		return "", errors.New("managed asset root is required")
	}
	return filepath.Join(root, spec.Name, spec.Version, spec.Platform, spec.Entrypoint), nil
}

func (m Manager) Validate(spec Spec) (string, error) {
	path, err := m.Path(spec)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 {
		return "", errors.New("managed asset executable is not a non-empty regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
		return "", errors.New("managed asset executable is not executable")
	}
	manifestPath := filepath.Join(filepath.Dir(path), "manifest.json")
	data, err := readLimitedFile(manifestPath, maxManifestBytes)
	if err != nil {
		return "", fmt.Errorf("read managed asset manifest: %w", err)
	}
	var value manifest
	if err := json.Unmarshal(data, &value); err != nil {
		return "", fmt.Errorf("decode managed asset manifest: %w", err)
	}
	expectedArchiveSHA := strings.ToLower(strings.TrimSpace(spec.SHA256))
	if value.Schema != manifestSchema || value.Name != spec.Name || value.Version != spec.Version || value.Platform != spec.Platform || value.URL != spec.URL || value.ArchiveSHA != expectedArchiveSHA || value.Entrypoint != spec.Entrypoint {
		return "", errors.New("managed asset manifest does not match requested asset")
	}
	binarySHA, err := hashFile(path)
	if err != nil {
		return "", err
	}
	if value.BinarySHA256 == "" || !strings.EqualFold(value.BinarySHA256, binarySHA) {
		return "", errors.New("managed asset executable checksum mismatch")
	}
	return path, nil
}

func (m Manager) Install(ctx context.Context, spec Spec) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	path, err := m.Path(spec)
	if err != nil {
		return "", err
	}
	if existing, err := m.Validate(spec); err == nil {
		return existing, nil
	}
	parent := filepath.Dir(filepath.Dir(path))
	if err := os.MkdirAll(parent, 0700); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(parent, "."+spec.Platform+"-staging-")
	if err != nil {
		return "", err
	}
	cleanupStage := true
	defer func() {
		if cleanupStage {
			cleanupSpan := tracepkg.Start(ctx, "MANAGED_ASSET", "managed_asset.cleanup", "Cleaning managed asset staging directory",
				tracepkg.String("name", spec.Name), tracepkg.String("version", spec.Version))
			cleanupSpan.Finish(os.RemoveAll(stage))
		}
	}()
	archivePath := filepath.Join(stage, "asset."+strings.ReplaceAll(spec.Archive, ".", "-"))
	limit := m.DownloadLimit
	if limit <= 0 {
		limit = defaultDownloadLimit
	}
	downloadSpan := tracepkg.Start(ctx, "MANAGED_ASSET", "managed_asset.download", "Downloading managed asset",
		tracepkg.String("name", spec.Name), tracepkg.String("version", spec.Version), tracepkg.URL("url", spec.URL))
	if err := download(ctx, m.HTTPClient, spec.URL, archivePath, limit); err != nil {
		downloadSpan.Fail(err)
		return "", err
	}
	downloadSpan.End()
	archiveSpan := tracepkg.Start(ctx, "MANAGED_ASSET", "managed_asset.verify.archive", "Verifying managed asset archive",
		tracepkg.String("name", spec.Name), tracepkg.String("version", spec.Version))
	archiveSHA, err := hashFile(archivePath)
	if err != nil {
		archiveSpan.Fail(err)
		return "", err
	}
	if !strings.EqualFold(archiveSHA, spec.SHA256) {
		err := errors.New("managed asset archive checksum mismatch")
		archiveSpan.Fail(err)
		return "", err
	}
	archiveSpan.End()
	payload := filepath.Join(stage, "payload")
	extractSpan := tracepkg.Start(ctx, "MANAGED_ASSET", "managed_asset.extract", "Extracting managed asset",
		tracepkg.String("name", spec.Name), tracepkg.String("archive", spec.Archive))
	binary, err := extractEntrypoint(archivePath, payload, spec.Archive, spec.Entrypoint)
	if err != nil {
		extractSpan.Fail(err)
		return "", err
	}
	extractSpan.End()
	payloadSpan := tracepkg.Start(ctx, "MANAGED_ASSET", "managed_asset.verify.payload", "Verifying managed asset payload",
		tracepkg.String("name", spec.Name), tracepkg.String("version", spec.Version))
	if err := validateExecutable(binary); err != nil {
		payloadSpan.Fail(err)
		return "", err
	}
	binarySHA, err := hashFile(binary)
	if err != nil {
		payloadSpan.Fail(err)
		return "", err
	}
	manifestData, err := json.MarshalIndent(manifest{Schema: manifestSchema, Name: spec.Name, Version: spec.Version, Platform: spec.Platform, URL: spec.URL, ArchiveSHA: strings.ToLower(spec.SHA256), BinarySHA256: binarySHA, Entrypoint: spec.Entrypoint}, "", "  ")
	if err != nil {
		payloadSpan.Fail(err)
		return "", err
	}
	manifestData = append(manifestData, '\n')
	if err := os.WriteFile(filepath.Join(payload, "manifest.json"), manifestData, 0600); err != nil {
		payloadSpan.Fail(err)
		return "", err
	}
	payloadSpan.End()
	target := filepath.Dir(path)
	activateSpan := tracepkg.Start(ctx, "MANAGED_ASSET", "managed_asset.activate", "Activating managed asset",
		tracepkg.String("name", spec.Name), tracepkg.String("version", spec.Version))
	validated, keepStage, err := m.activateDirectory(ctx, stage, payload, target, func() (string, error) { return m.validateActivated(spec) })
	cleanupStage = !keepStage
	activateSpan.Finish(err)
	return validated, err
}

func (m Manager) Remove(spec Spec) (bool, error) {
	path, err := m.Path(spec)
	if err != nil {
		return false, err
	}
	target := filepath.Dir(path)
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
	pruneEmptyManagedParents(filepath.Dir(target), strings.TrimSpace(m.Root))
	return true, nil
}

func pruneEmptyManagedParents(path, root string) {
	root = filepath.Clean(root)
	for path != "" {
		clean := filepath.Clean(path)
		if clean == root || clean == "." || clean == string(filepath.Separator) {
			return
		}
		if err := os.Remove(clean); err != nil {
			return
		}
		path = filepath.Dir(clean)
	}
}

func (m Manager) TreePath(spec TreeSpec) (string, error) {
	if err := spec.Validate(); err != nil {
		return "", err
	}
	root := strings.TrimSpace(m.Root)
	if root == "" {
		return "", errors.New("managed asset root is required")
	}
	return filepath.Join(root, spec.Name, spec.Version, spec.Platform), nil
}

func (m Manager) ValidateTree(spec TreeSpec) (string, error) {
	target, err := m.TreePath(spec)
	if err != nil {
		return "", err
	}
	data, err := readLimitedFile(filepath.Join(target, "tree-manifest.json"), maxTreeManifestBytes)
	if err != nil {
		return "", fmt.Errorf("read managed tree manifest: %w", err)
	}
	var value treeManifest
	if err := json.Unmarshal(data, &value); err != nil {
		return "", fmt.Errorf("decode managed tree manifest: %w", err)
	}
	if value.Schema != manifestSchema || value.Name != spec.Name || value.Version != spec.Version || value.Platform != spec.Platform || value.URL != spec.URL || !strings.EqualFold(value.ArchiveSHA, spec.SHA256) || value.Entrypoint != spec.Entrypoint {
		return "", errors.New("managed tree manifest does not match requested asset")
	}
	actual, err := treeHashes(target, "tree-manifest.json")
	if err != nil {
		return "", err
	}
	if !equalTreeHashes(value.Files, actual) {
		return "", errors.New("managed tree content checksum mismatch")
	}
	entrypoint := filepath.Join(target, filepath.FromSlash(spec.Entrypoint))
	if err := validateTreeEntrypoint(entrypoint, spec.Platform); err != nil {
		return "", err
	}
	if err := validateRequiredTreeFiles(target, spec.Required); err != nil {
		return "", err
	}
	return entrypoint, nil
}

func (m Manager) InstallTree(ctx context.Context, spec TreeSpec) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	target, err := m.TreePath(spec)
	if err != nil {
		return "", err
	}
	if existing, err := m.ValidateTree(spec); err == nil {
		return existing, nil
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(parent, "."+spec.Platform+"-tree-staging-")
	if err != nil {
		return "", err
	}
	cleanupStage := true
	defer func() {
		if cleanupStage {
			cleanupSpan := tracepkg.Start(ctx, "MANAGED_ASSET", "managed_asset.cleanup", "Cleaning managed asset tree staging directory",
				tracepkg.String("name", spec.Name), tracepkg.String("version", spec.Version))
			cleanupSpan.Finish(os.RemoveAll(stage))
		}
	}()
	archivePath := filepath.Join(stage, "asset."+strings.ReplaceAll(spec.Archive, ".", "-"))
	limit := m.DownloadLimit
	if limit <= 0 {
		limit = maxTreeDownloadLimit
	}
	downloadSpan := tracepkg.Start(ctx, "MANAGED_ASSET", "managed_asset.download", "Downloading managed asset tree",
		tracepkg.String("name", spec.Name), tracepkg.String("version", spec.Version), tracepkg.URL("url", spec.URL))
	if err := download(ctx, m.HTTPClient, spec.URL, archivePath, limit); err != nil {
		downloadSpan.Fail(err)
		return "", err
	}
	downloadSpan.End()
	archiveSpan := tracepkg.Start(ctx, "MANAGED_ASSET", "managed_asset.verify.archive", "Verifying managed asset archive",
		tracepkg.String("name", spec.Name), tracepkg.String("version", spec.Version))
	archiveSHA, err := hashFile(archivePath)
	if err != nil {
		archiveSpan.Fail(err)
		return "", err
	}
	if !strings.EqualFold(archiveSHA, spec.SHA256) {
		err := errors.New("managed tree archive checksum mismatch")
		archiveSpan.Fail(err)
		return "", err
	}
	archiveSpan.End()
	payload := filepath.Join(stage, "payload")
	extractSpan := tracepkg.Start(ctx, "MANAGED_ASSET", "managed_asset.extract", "Extracting managed asset tree",
		tracepkg.String("name", spec.Name), tracepkg.String("archive", spec.Archive))
	if err := extractTree(archivePath, payload, spec.Archive); err != nil {
		extractSpan.Fail(err)
		return "", err
	}
	extractSpan.End()
	payloadSpan := tracepkg.Start(ctx, "MANAGED_ASSET", "managed_asset.verify.payload", "Verifying managed asset tree payload",
		tracepkg.String("name", spec.Name), tracepkg.String("version", spec.Version))
	entrypoint := filepath.Join(payload, filepath.FromSlash(spec.Entrypoint))
	if err := validateTreeEntrypoint(entrypoint, spec.Platform); err != nil {
		payloadSpan.Fail(err)
		return "", err
	}
	if err := validateRequiredTreeFiles(payload, spec.Required); err != nil {
		payloadSpan.Fail(err)
		return "", err
	}
	files, err := treeHashes(payload, "tree-manifest.json")
	if err != nil {
		payloadSpan.Fail(err)
		return "", err
	}
	manifestData, err := json.MarshalIndent(treeManifest{Schema: manifestSchema, Name: spec.Name, Version: spec.Version, Platform: spec.Platform, URL: spec.URL, ArchiveSHA: strings.ToLower(spec.SHA256), Entrypoint: spec.Entrypoint, Files: files}, "", "  ")
	if err != nil {
		payloadSpan.Fail(err)
		return "", err
	}
	if err := os.WriteFile(filepath.Join(payload, "tree-manifest.json"), append(manifestData, '\n'), 0600); err != nil {
		payloadSpan.Fail(err)
		return "", err
	}
	payloadSpan.EndMessage("Managed asset tree payload verified", tracepkg.Int("file_count", len(files)))
	activateSpan := tracepkg.Start(ctx, "MANAGED_ASSET", "managed_asset.activate", "Activating managed asset tree",
		tracepkg.String("name", spec.Name), tracepkg.String("version", spec.Version))
	validated, keepStage, err := m.activateDirectory(ctx, stage, payload, target, func() (string, error) { return m.ValidateTree(spec) })
	cleanupStage = !keepStage
	activateSpan.Finish(err)
	return validated, err
}

func (m Manager) activateDirectory(ctx context.Context, stage, payload, target string, validate func() (string, error)) (string, bool, error) {
	previous := filepath.Join(stage, "previous")
	hadPrevious := false
	if _, err := os.Lstat(target); err == nil {
		if err := m.renamePath(target, previous); err != nil {
			return "", false, fmt.Errorf("backup managed asset: %w", err)
		}
		hadPrevious = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	if err := m.renamePath(payload, target); err != nil {
		activateErr := fmt.Errorf("activate managed asset: %w", err)
		if hadPrevious {
			restoreSpan := tracepkg.Start(ctx, "MANAGED_ASSET", "managed_asset.activate.rollback", "Restoring previous managed asset")
			if restoreErr := m.renamePath(previous, target); restoreErr != nil {
				restoreSpan.Fail(restoreErr)
				return "", true, errors.Join(activateErr, fmt.Errorf("restore previous managed asset: %w", restoreErr))
			}
			restoreSpan.End()
		}
		return "", false, activateErr
	}
	validated, err := validate()
	if err == nil {
		return validated, false, nil
	}
	failed := filepath.Join(stage, "failed-active")
	if moveErr := m.renamePath(target, failed); moveErr != nil {
		return "", true, errors.Join(fmt.Errorf("validate activated managed asset: %w", err), fmt.Errorf("quarantine failed managed asset: %w", moveErr))
	}
	if hadPrevious {
		restoreSpan := tracepkg.Start(ctx, "MANAGED_ASSET", "managed_asset.activate.rollback", "Restoring previous managed asset")
		if restoreErr := m.renamePath(previous, target); restoreErr != nil {
			restoreSpan.Fail(restoreErr)
			return "", true, errors.Join(fmt.Errorf("validate activated managed asset: %w", err), fmt.Errorf("restore previous managed asset: %w", restoreErr))
		}
		restoreSpan.End()
	}
	return "", false, fmt.Errorf("validate activated managed asset: %w", err)
}

func (m Manager) renamePath(oldPath, newPath string) error {
	if m.rename != nil {
		return m.rename(oldPath, newPath)
	}
	return os.Rename(oldPath, newPath)
}

func (m Manager) validateActivated(spec Spec) (string, error) {
	if m.postActivate != nil {
		return m.postActivate(spec)
	}
	return m.Validate(spec)
}

func download(ctx context.Context, base *http.Client, rawURL, destination string, limit int64) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("managed asset URL must use HTTPS")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return err
	}
	client := secureHTTPClient(base)
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download managed asset: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download managed asset: server returned %s", response.Status)
	}
	if response.ContentLength > limit {
		return fmt.Errorf("managed asset download exceeds %d-byte limit", limit)
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
	written, err := io.Copy(file, io.LimitReader(response.Body, limit+1))
	if err != nil {
		return fmt.Errorf("download managed asset: %w", err)
	}
	if written == 0 {
		return errors.New("managed asset download is empty")
	}
	if written > limit {
		return fmt.Errorf("managed asset download exceeds %d-byte limit", limit)
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
			return errors.New("managed asset redirect must use HTTPS")
		}
		if len(via) >= 10 {
			return errors.New("managed asset redirect limit exceeded")
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
		return "", fmt.Errorf("unsupported managed asset archive type %q", archiveType)
	}
}

func extractZipEntrypoint(archivePath, destination, entrypoint string) (string, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", err
	}
	defer reader.Close()
	if len(reader.File) > maxArchiveEntries {
		return "", errors.New("managed asset archive has too many entries")
	}
	seen := map[string]struct{}{}
	var total int64
	var result string
	for _, entry := range reader.File {
		name, err := safeArchivePath(entry.Name)
		if err != nil {
			return "", err
		}
		if _, duplicate := seen[name]; duplicate {
			return "", fmt.Errorf("managed asset archive contains duplicate entry %s", name)
		}
		seen[name] = struct{}{}
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 || !mode.IsRegular() && !mode.IsDir() {
			return "", fmt.Errorf("managed asset archive contains unsupported entry type %s", name)
		}
		size := entry.FileInfo().Size()
		total, err = addExtractedBytes(total, size)
		if err != nil {
			return "", err
		}
		if mode.IsDir() || name != entrypoint {
			continue
		}
		if result != "" {
			return "", fmt.Errorf("managed asset archive contains duplicate entrypoint %s", entrypoint)
		}
		stream, err := entry.Open()
		if err != nil {
			return "", err
		}
		result, err = writeEntrypoint(destination, entrypoint, stream, size)
		closeErr := stream.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	if result == "" {
		return "", fmt.Errorf("managed asset archive is missing %s", entrypoint)
	}
	return result, nil
}

func extractZipTree(archivePath, destination string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()
	if len(reader.File) > maxTreeArchiveEntries {
		return errors.New("managed tree archive has too many entries")
	}
	if err := os.MkdirAll(destination, 0700); err != nil {
		return err
	}
	seen := map[string]struct{}{}
	var total int64
	for _, entry := range reader.File {
		name, err := safeArchivePath(entry.Name)
		if err != nil {
			return err
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("managed tree archive contains duplicate entry %s", name)
		}
		seen[name] = struct{}{}
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 || !mode.IsRegular() && !mode.IsDir() {
			return fmt.Errorf("managed tree archive contains unsupported entry type %s", name)
		}
		size := entry.FileInfo().Size()
		if size < 0 || size > maxTreeExtractedBytes || total > maxTreeExtractedBytes-size {
			return errors.New("managed tree archive exceeds extracted size limit")
		}
		total += size
		path := filepath.Join(destination, filepath.FromSlash(name))
		if mode.IsDir() {
			if err := os.MkdirAll(path, 0700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		stream, err := entry.Open()
		if err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if err != nil {
			_ = stream.Close()
			return err
		}
		written, copyErr := io.Copy(file, io.LimitReader(stream, size+1))
		closeErr := file.Close()
		streamErr := stream.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if streamErr != nil {
			return streamErr
		}
		if written != size {
			return fmt.Errorf("managed tree entry %s size mismatch", name)
		}
		if err := os.Chmod(path, mode.Perm()|0600); err != nil {
			return err
		}
	}
	return nil
}

func extractTree(archivePath, destination, archiveType string) error {
	switch archiveType {
	case "zip":
		return extractZipTree(archivePath, destination)
	case "tar.gz":
		return extractTarTree(archivePath, destination)
	default:
		return fmt.Errorf("unsupported managed tree archive type %q", archiveType)
	}
}

func extractTarTree(archivePath, destination string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	if err := os.MkdirAll(destination, 0700); err != nil {
		return err
	}
	seen := map[string]struct{}{}
	var total int64
	entries := 0
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		entries++
		if entries > maxTreeArchiveEntries {
			return errors.New("managed tree archive has too many entries")
		}
		name, err := safeArchivePath(header.Name)
		if err != nil {
			return err
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("managed tree archive contains duplicate entry %s", name)
		}
		seen[name] = struct{}{}
		switch header.Typeflag {
		case tar.TypeDir:
			path := filepath.Join(destination, filepath.FromSlash(name))
			if err := os.MkdirAll(path, 0700); err != nil {
				return err
			}
			continue
		case tar.TypeReg, 0:
		default:
			return fmt.Errorf("managed tree archive contains unsupported entry type %s", name)
		}
		size := header.Size
		if size < 0 || size > maxTreeExtractedBytes || total > maxTreeExtractedBytes-size {
			return errors.New("managed tree archive exceeds extracted size limit")
		}
		total += size
		path := filepath.Join(destination, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if err != nil {
			return err
		}
		written, copyErr := io.Copy(file, io.LimitReader(reader, size+1))
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if written != size {
			return fmt.Errorf("managed tree entry %s size mismatch", name)
		}
		mode := os.FileMode(header.Mode).Perm()
		if err := os.Chmod(path, mode|0600); err != nil {
			return err
		}
	}
	return nil
}

func extractTarEntrypoint(archivePath, destination, entrypoint string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	seen := map[string]struct{}{}
	var total int64
	var result string
	entries := 0
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		entries++
		if entries > maxArchiveEntries {
			return "", errors.New("managed asset archive has too many entries")
		}
		name, err := safeArchivePath(header.Name)
		if err != nil {
			return "", err
		}
		if _, duplicate := seen[name]; duplicate {
			return "", fmt.Errorf("managed asset archive contains duplicate entry %s", name)
		}
		seen[name] = struct{}{}
		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg, 0:
		default:
			return "", fmt.Errorf("managed asset archive contains unsupported entry type %s", name)
		}
		total, err = addExtractedBytes(total, header.Size)
		if err != nil {
			return "", err
		}
		if name != entrypoint {
			continue
		}
		if result != "" {
			return "", fmt.Errorf("managed asset archive contains duplicate entrypoint %s", entrypoint)
		}
		result, err = writeEntrypoint(destination, entrypoint, reader, header.Size)
		if err != nil {
			return "", err
		}
	}
	if result == "" {
		return "", fmt.Errorf("managed asset archive is missing %s", entrypoint)
	}
	return result, nil
}

func safeArchivePath(name string) (string, error) {
	name = strings.ReplaceAll(strings.TrimSpace(name), "\\", "/")
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return "", fmt.Errorf("unsafe managed asset archive path %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", fmt.Errorf("unsafe managed asset archive path %q", name)
		}
	}
	clean := filepath.ToSlash(filepath.Clean(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("unsafe managed asset archive path %q", name)
	}
	return clean, nil
}

func writeEntrypoint(destination, entrypoint string, reader io.Reader, size int64) (string, error) {
	if size <= 0 || size > maxExtractedBytes {
		return "", fmt.Errorf("managed asset entrypoint has invalid size %d", size)
	}
	path := filepath.Join(destination, entrypoint)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	if err != nil {
		return "", err
	}
	written, copyErr := io.Copy(file, io.LimitReader(reader, size+1))
	closeErr := file.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if written != size {
		return "", errors.New("managed asset entrypoint size mismatch")
	}
	if err := os.Chmod(path, 0755); err != nil {
		return "", err
	}
	return path, nil
}

func addExtractedBytes(total, size int64) (int64, error) {
	if size < 0 || size > maxExtractedBytes || total > maxExtractedBytes-size {
		return 0, errors.New("managed asset archive exceeds extracted size limit")
	}
	return total + size, nil
}

func validateExecutable(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 {
		return errors.New("managed asset entrypoint is not a non-empty regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
		return errors.New("managed asset entrypoint is not executable")
	}
	return nil
}

func validateTreeEntrypoint(path, platform string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 {
		return errors.New("managed tree entrypoint is not a non-empty regular file")
	}
	if !strings.HasPrefix(platform, "windows-") && info.Mode().Perm()&0111 == 0 {
		return errors.New("managed tree entrypoint is not executable")
	}
	return nil
}

func validateRequiredTreeFiles(root string, required []string) error {
	for _, name := range required {
		path := filepath.Join(root, filepath.FromSlash(name))
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("managed tree required file %s: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 {
			return fmt.Errorf("managed tree required file %s is not a non-empty regular file", name)
		}
	}
	return nil
}

func readLimitedFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("managed asset manifest must be a regular non-symlink file")
	}
	if info.Size() > limit {
		return nil, errors.New("managed asset manifest exceeds size limit")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("managed asset manifest exceeds size limit")
	}
	return data, nil
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

func treeHashes(root, excluded string) ([]treeFileHash, error) {
	files := []treeFileHash{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == excluded {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !entry.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("managed tree contains unsupported entry type %s", relative)
		}
		if entry.IsDir() {
			return nil
		}
		sha, err := hashFile(path)
		if err != nil {
			return err
		}
		files = append(files, treeFileHash{Path: relative, SHA256: sha, Size: info.Size()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func equalTreeHashes(left, right []treeFileHash) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func safeComponent(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && value != "." && value != ".." && filepath.Base(value) == value && !strings.ContainsAny(value, `/\\:`)
}
