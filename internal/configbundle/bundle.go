package configbundle

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/oauth"
	"go.mewis.me/codemcp/internal/secretstore"
	"go.mewis.me/codemcp/internal/state"
	"go.mewis.me/codemcp/internal/upstream"
	"go.mewis.me/codemcp/internal/workspace"
)

const (
	Version            = 1
	magic              = "CGMCFG\x00\x01"
	maxBundleBytes     = 256 << 20
	maxStateBytes      = 128 << 20
	maxBundleFileBytes = 64 << 20
	// Keep the released v1 key material stable so CodeMCP can import bundles
	// produced by released v0.2.24 installations.
	legacyBundleKeyMaterialV1 = "chatgpt-mcp portable config bundle v1 / mewis.me"
)

type Platform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	Home string `json:"home,omitempty"`
}

type File struct {
	Path string `json:"path"`
	Mode uint32 `json:"mode,omitempty"`
	Data []byte `json:"data"`
}

type Bundle struct {
	Version   int               `json:"version"`
	CreatedAt time.Time         `json:"created_at"`
	Source    Platform          `json:"source"`
	Files     []File            `json:"files"`
	Secrets   map[string]string `json:"secrets,omitempty"`
}

type ExportOptions struct {
	Force bool
}

type ExportResult struct {
	Path         string
	Files        int
	Secrets      int
	SkippedFiles int
	Source       Platform
}

type ImportOptions struct {
	Force bool
}

type ImportResult struct {
	Files        int
	Secrets      int
	SkippedPaths int
	SkippedFiles int
	BackupPath   string
	Source       Platform
	Target       Platform
}

type workspaceRegistry struct {
	Version    int                   `json:"version"`
	Workspaces []workspace.Workspace `json:"workspaces"`
}

type materializeResult struct {
	files        int
	skippedPaths int
	skippedFiles int
}

func Export(root, destination string, options ExportOptions) (ExportResult, error) {
	root, err := absoluteClean(root)
	if err != nil {
		return ExportResult{}, err
	}
	destination, err = absoluteClean(destination)
	if err != nil {
		return ExportResult{}, err
	}
	if within(root, destination) {
		return ExportResult{}, errors.New("config export file must be outside the selected config root")
	}
	source, err := config.SourceAt(root)
	if err != nil {
		return ExportResult{}, err
	}
	if !source.Exists {
		return ExportResult{}, errors.New("configuration is not initialized")
	}
	if !options.Force {
		if _, err := os.Stat(destination); err == nil {
			return ExportResult{}, fmt.Errorf("export file already exists: %s; use --force to overwrite", destination)
		} else if !errors.Is(err, os.ErrNotExist) {
			return ExportResult{}, err
		}
	}
	files, skippedFiles, err := collectFiles(root)
	if err != nil {
		return ExportResult{}, err
	}
	secrets, err := collectSecrets(root)
	if err != nil {
		return ExportResult{}, err
	}
	platform := currentPlatform()
	bundle := Bundle{Version: Version, CreatedAt: time.Now().UTC(), Source: platform, Files: files, Secrets: secrets}
	encoded, err := encode(bundle)
	if err != nil {
		return ExportResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return ExportResult{}, err
	}
	if err := state.WriteFileAtomic(destination, encoded, 0600); err != nil {
		return ExportResult{}, err
	}
	return ExportResult{Path: destination, Files: len(files), Secrets: len(secrets), SkippedFiles: skippedFiles, Source: platform}, nil
}

func Import(root, source string, options ImportOptions) (ImportResult, error) {
	root, err := absoluteClean(root)
	if err != nil {
		return ImportResult{}, err
	}
	source, err = absoluteClean(source)
	if err != nil {
		return ImportResult{}, err
	}
	if within(root, source) {
		return ImportResult{}, errors.New("config import file must be outside the selected config root")
	}
	bundle, err := readBundle(source)
	if err != nil {
		return ImportResult{}, err
	}
	if bundle.Version != Version {
		return ImportResult{}, fmt.Errorf("unsupported config bundle version: %d", bundle.Version)
	}
	if strings.TrimSpace(bundle.Source.OS) == "" {
		return ImportResult{}, errors.New("config bundle source platform is missing")
	}
	hasTarget, err := directoryHasContent(root)
	if err != nil {
		return ImportResult{}, err
	}
	if hasTarget && !options.Force {
		return ImportResult{}, errors.New("configuration/state already exists; use --force to merge imported state")
	}
	parent := filepath.Dir(root)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return ImportResult{}, err
	}
	stage, err := os.MkdirTemp(parent, "."+filepath.Base(root)+"-import-")
	if err != nil {
		return ImportResult{}, err
	}
	target := currentPlatform()
	materialized, err := materialize(stage, bundle, target)
	if err != nil {
		return ImportResult{}, err
	}
	if hasTarget {
		if err := mergeImportedMainConfig(root, stage); err != nil {
			return ImportResult{}, err
		}
	}
	if err := configformat.MarkRoot(stage); err != nil {
		return ImportResult{}, err
	}
	backup := ""
	if _, err := os.Stat(root); err == nil {
		backup = uniqueSibling(root, "backup")
		if err := os.Rename(root, backup); err != nil {
			return ImportResult{}, fmt.Errorf("backup existing config root: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return ImportResult{}, err
	}
	rollback := func(cause error) error {
		failed := uniqueSibling(root, "failed-import")
		if _, statErr := os.Stat(root); statErr == nil {
			if renameErr := os.Rename(root, failed); renameErr != nil {
				return errors.Join(cause, fmt.Errorf("preserve failed imported config root: %w", renameErr))
			}
		}
		if backup != "" {
			if restoreErr := os.Rename(backup, root); restoreErr != nil {
				return errors.Join(cause, fmt.Errorf("restore previous config root: %w", restoreErr))
			}
		}
		return cause
	}
	if err := os.Rename(stage, root); err != nil {
		if backup != "" {
			_ = os.Rename(backup, root)
		}
		return ImportResult{}, fmt.Errorf("activate imported config root: %w", err)
	}
	changes := make([]secretstore.Change, 0, len(bundle.Secrets))
	secretNames := make([]string, 0, len(bundle.Secrets))
	for name := range bundle.Secrets {
		secretNames = append(secretNames, name)
	}
	sort.Strings(secretNames)
	for _, name := range secretNames {
		changes = append(changes, secretstore.Change{Name: name, Value: bundle.Secrets[name]})
	}
	if err := secretstore.New(root).Apply(changes); err != nil {
		return ImportResult{}, rollback(fmt.Errorf("restore imported secrets: %w", err))
	}
	if _, err := config.VerifyAt(root); err != nil {
		return ImportResult{}, rollback(fmt.Errorf("verify imported configuration: %w", err))
	}
	return ImportResult{
		Files: materialized.files, Secrets: len(bundle.Secrets), SkippedPaths: materialized.skippedPaths,
		SkippedFiles: materialized.skippedFiles, BackupPath: backup, Source: bundle.Source, Target: target,
	}, nil
}

func collectFiles(root string) ([]File, int, error) {
	files := []File{}
	skipped := 0
	total := int64(0)
	rootFS, err := os.OpenRoot(root)
	if err != nil {
		return nil, skipped, err
	}
	defer rootFS.Close()
	err = filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filePath == root || entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if excludedFile(relative) || entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			skipped++
			return nil
		}
		file, err := rootFS.Open(filepath.FromSlash(relative))
		if err != nil {
			return err
		}
		info, err := file.Stat()
		if err != nil {
			_ = file.Close()
			return err
		}
		if !info.Mode().IsRegular() {
			_ = file.Close()
			skipped++
			return nil
		}
		if info.Size() > maxBundleFileBytes {
			_ = file.Close()
			return fmt.Errorf("config state file is too large to export: %s", relative)
		}
		data, err := io.ReadAll(io.LimitReader(file, maxBundleFileBytes+1))
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if len(data) > maxBundleFileBytes {
			return fmt.Errorf("config state file is too large to export: %s", relative)
		}
		total += int64(len(data))
		if total > maxStateBytes {
			return errors.New("config bundle payload exceeds size limit")
		}
		files = append(files, File{Path: relative, Mode: uint32(info.Mode().Perm()), Data: data})
		return nil
	})
	if err != nil {
		return nil, skipped, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, skipped, nil
}

func excludedFile(relative string) bool {
	relative = pathpkg.Clean(strings.TrimPrefix(relative, "./"))
	if relative == ".runtime-control.json" || relative == "state/instance.json" || relative == "state/update.json" {
		return true
	}
	for _, prefix := range []string{"logs/", "runtime/", "state/secrets/"} {
		if strings.HasPrefix(relative, prefix) {
			return true
		}
	}
	parts := strings.Split(relative, "/")
	if len(parts) >= 3 && parts[0] == "workspaces" {
		if parts[2] == "checkpoints" || strings.HasPrefix(parts[2], "shell.") {
			return true
		}
	}
	return false
}

func collectSecrets(root string) (map[string]string, error) {
	required := map[string]bool{}
	add := func(values []string) {
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				required[value] = true
			}
		}
	}
	tunnelEntries, err := config.TunnelSecretEntries(root)
	if err != nil {
		return nil, err
	}
	add(tunnelEntries)
	oauthEntries, err := oauth.NewStore(configformat.StructuredPath(root, "oauth")).SecretEntries()
	if err != nil {
		return nil, err
	}
	add(oauthEntries)
	upstreamEntries, err := upstream.NewStore(configformat.StructuredPath(root, "upstream")).SecretEntries()
	if err != nil {
		return nil, err
	}
	add(upstreamEntries)
	optionalRelay := secretstore.Name("cluster", "relay-token")
	names := make([]string, 0, len(required)+1)
	for name := range required {
		names = append(names, name)
	}
	names = append(names, optionalRelay)
	sort.Strings(names)
	store := secretstore.New(root)
	result := map[string]string{}
	for _, name := range names {
		value, err := store.Get(name)
		if errors.Is(err, secretstore.ErrNotFound) && name == optionalRelay {
			continue
		}
		if err != nil {
			return nil, err
		}
		result[name] = value
	}
	return result, nil
}

func materialize(root string, bundle Bundle, target Platform) (materializeResult, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return materializeResult{}, err
	}
	files := append([]File(nil), bundle.Files...)
	var workspaceFileIndex = -1
	for index := range files {
		if topLevelStructured(files[index].Path, "workspaces") {
			workspaceFileIndex = index
			break
		}
	}
	idMap := map[string]string{}
	workspaceRoots := map[string]string{}
	result := materializeResult{}
	if workspaceFileIndex >= 0 {
		data, mapping, roots, skipped, err := normalizeWorkspaceRegistry(files[workspaceFileIndex], bundle.Source, target)
		if err != nil {
			return result, err
		}
		files[workspaceFileIndex].Data = data
		idMap, workspaceRoots = mapping, roots
		result.skippedPaths += skipped
	}
	written := map[string]string{}
	for _, item := range files {
		relative, ok := safeRelative(item.Path)
		if !ok {
			return result, fmt.Errorf("config bundle contains unsafe path: %q", item.Path)
		}
		data := item.Data
		if topLevelStructured(relative, "config") {
			normalized, skipped, err := normalizeMainConfig(data, bundle.Source, target)
			if err != nil {
				return result, err
			}
			data = normalized
			result.skippedPaths += skipped
		}
		parts := strings.Split(relative, "/")
		if len(parts) >= 2 && parts[0] == "workspaces" && workspaceFileIndex >= 0 {
			oldID := parts[1]
			newID, exists := idMap[oldID]
			if !exists {
				result.skippedFiles++
				continue
			}
			parts[1] = newID
			relative = strings.Join(parts, "/")
			normalized, err := normalizeWorkspaceState(relative, data, oldID, newID, workspaceRoots[newID], bundle.Source, target)
			if err != nil {
				return result, err
			}
			data = normalized
		}
		if previous, exists := written[relative]; exists {
			return result, fmt.Errorf("config bundle path collision after platform normalization: %s (%s, %s)", relative, previous, item.Path)
		}
		written[relative] = item.Path
		destination := filepath.Join(root, filepath.FromSlash(relative))
		if !within(root, destination) {
			return result, fmt.Errorf("config bundle path escapes target root: %s", relative)
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return result, err
		}
		if err := state.WriteFileAtomic(destination, data, 0600); err != nil {
			return result, err
		}
		result.files++
	}
	return result, nil
}

func normalizeMainConfig(data []byte, source, target Platform) ([]byte, int, error) {
	raw, err := configformat.DecodeGeneric(configformat.JSON, data)
	if err != nil {
		return nil, 0, fmt.Errorf("decode bundled config: %w", err)
	}
	root, ok := raw.(map[string]any)
	if !ok {
		return nil, 0, errors.New("bundled config must be an object")
	}
	cfg := config.Default()
	if err := configformat.Unmarshal(configformat.JSON, data, &cfg); err != nil {
		return nil, 0, fmt.Errorf("decode bundled config: %w", err)
	}
	skipped := 0
	if permissions, ok := root["permissions"].(map[string]any); ok {
		if _, exists := permissions["allow_dirs"]; exists {
			allowDirs, count := portableDirectories(cfg.Permissions.AllowDirs, source, target)
			permissions["allow_dirs"] = allowDirs
			skipped += count
		}
	}
	if shell, ok := root["shell"].(map[string]any); ok {
		if _, exists := shell["path"]; exists {
			shellPath, count := portableDirectories(cfg.Shell.Path, source, target)
			shell["path"] = shellPath
			skipped += count
		}
	}
	if source.OS != target.OS && cfg.Server.Expose.Mode == config.ExposureInterfaces {
		if server, ok := root["server"].(map[string]any); ok {
			if _, exists := server["expose"]; exists {
				server["expose"] = map[string]any{"mode": string(config.ExposureNone), "interfaces": []any{}}
			}
		}
	}
	encoded, err := configformat.EncodeGeneric(configformat.JSON, root)
	if err != nil {
		return nil, 0, err
	}
	return encoded, skipped, nil
}

func mergeImportedMainConfig(existingRoot, stagedRoot string) error {
	existing, err := config.SourceAt(existingRoot)
	if err != nil {
		return fmt.Errorf("discover existing configuration for merge: %w", err)
	}
	staged, err := config.SourceAt(stagedRoot)
	if err != nil {
		return fmt.Errorf("discover imported configuration for merge: %w", err)
	}
	if !existing.Exists || !staged.Exists {
		return nil
	}
	existingData, err := os.ReadFile(existing.Path)
	if err != nil {
		return err
	}
	stagedData, err := os.ReadFile(staged.Path)
	if err != nil {
		return err
	}
	existingRaw, err := configformat.DecodeGeneric(configformat.JSON, existingData)
	if err != nil {
		return fmt.Errorf("decode existing configuration for import merge: %w", err)
	}
	stagedRaw, err := configformat.DecodeGeneric(configformat.JSON, stagedData)
	if err != nil {
		return fmt.Errorf("decode imported configuration for merge: %w", err)
	}
	merged, ok := configformat.MergeGeneric(existingRaw, stagedRaw).(map[string]any)
	if !ok {
		return errors.New("configuration import merge requires object roots")
	}
	data, err := configformat.EncodeGeneric(configformat.JSON, merged)
	if err != nil {
		return err
	}
	return state.WriteFileAtomic(staged.Path, data, 0600)
}

func normalizeWorkspaceRegistry(file File, source, target Platform) ([]byte, map[string]string, map[string]string, int, error) {
	format, err := configformat.Detect(file.Path)
	if err != nil {
		return nil, nil, nil, 0, err
	}
	var registry workspaceRegistry
	if err := configformat.Unmarshal(format, file.Data, &registry); err != nil {
		return nil, nil, nil, 0, fmt.Errorf("decode bundled workspace registry: %w", err)
	}
	mapping := map[string]string{}
	roots := map[string]string{}
	items := make([]workspace.Workspace, 0, len(registry.Workspaces))
	skipped := 0
	for _, item := range registry.Workspaces {
		mapped, ok := portableDirectory(item.Path, source, target)
		if !ok {
			skipped += 1 + len(item.AllowDirs)
			continue
		}
		allowDirs, allowSkipped := portableDirectories(item.AllowDirs, source, target)
		skipped += allowSkipped
		oldID := item.ID
		item.Path = mapped
		item.AllowDirs = allowDirs
		item.ID = workspace.IDForPath(mapped)
		if oldID != "" && oldID != item.ID {
			item.LegacyIDs = appendUnique(item.LegacyIDs, oldID)
		}
		mapping[oldID] = item.ID
		roots[item.ID] = item.Path
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	registry.Workspaces = items
	encoded, err := configformat.Marshal(format, registry)
	if err != nil {
		return nil, nil, nil, 0, err
	}
	return encoded, mapping, roots, skipped, nil
}

func normalizeWorkspaceState(relative string, data []byte, oldID, newID, workspaceRoot string, source, target Platform) ([]byte, error) {
	format, err := configformat.Detect(relative)
	if err != nil {
		return data, nil
	}
	decoded, err := configformat.DecodeGeneric(format, data)
	if err != nil {
		return data, nil
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return data, nil
	}
	if value, _ := object["workspace_id"].(string); value == oldID {
		object["workspace_id"] = newID
	}
	if value, _ := object["cwd"].(string); value != "" {
		if mapped, ok := portableDirectory(value, source, target); ok {
			object["cwd"] = mapped
		} else if workspaceRoot != "" {
			object["cwd"] = workspaceRoot
		}
	}
	return configformat.EncodeGeneric(format, object)
}

func portableDirectories(values []string, source, target Platform) ([]string, int) {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	skipped := 0
	for _, value := range values {
		mapped, ok := portableDirectory(value, source, target)
		if !ok {
			skipped++
			continue
		}
		key := mapped
		if target.OS == "windows" {
			key = strings.ToLower(key)
		}
		if !seen[key] {
			seen[key] = true
			result = append(result, mapped)
		}
	}
	sort.Strings(result)
	return result, skipped
}

func portableDirectory(value string, source, target Platform) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if relative, ok := relativeToHome(value, source.Home, source.OS); ok && target.Home != "" {
		candidate := filepath.Join(target.Home, filepath.FromSlash(relative))
		if directoryExists(candidate) {
			return filepath.Clean(candidate), true
		}
	}
	if source.OS != target.OS || !filepath.IsAbs(value) || !directoryExists(value) {
		return "", false
	}
	return filepath.Clean(value), true
}

func relativeToHome(value, home, sourceOS string) (string, bool) {
	value = strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(value), "\\", "/"), "/")
	home = strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(home), "\\", "/"), "/")
	if value == "" || home == "" {
		return "", false
	}
	compareValue, compareHome := value, home
	if sourceOS == "windows" {
		compareValue, compareHome = strings.ToLower(compareValue), strings.ToLower(compareHome)
	}
	if compareValue == compareHome {
		return "", true
	}
	prefix := compareHome + "/"
	if !strings.HasPrefix(compareValue, prefix) {
		return "", false
	}
	return strings.TrimPrefix(value[len(home):], "/"), true
}

func directoryExists(value string) bool {
	info, err := os.Stat(value)
	return err == nil && info.IsDir()
}

func topLevelStructured(relative, name string) bool {
	if strings.Contains(filepath.ToSlash(relative), "/") {
		return false
	}
	base := strings.TrimSuffix(filepath.Base(relative), filepath.Ext(relative))
	if base != name {
		return false
	}
	_, err := configformat.Detect(relative)
	return err == nil
}

func safeRelative(value string) (string, bool) {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	clean := pathpkg.Clean(value)
	if clean == "." || clean == "" || strings.HasPrefix(clean, "../") || clean == ".." || pathpkg.IsAbs(clean) {
		return "", false
	}
	return clean, true
}

func currentPlatform() Platform {
	home, _ := os.UserHomeDir()
	return Platform{OS: runtime.GOOS, Arch: runtime.GOARCH, Home: home}
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func readBundle(file string) (Bundle, error) {
	info, err := os.Stat(file)
	if err != nil {
		return Bundle{}, err
	}
	if !info.Mode().IsRegular() {
		return Bundle{}, errors.New("config bundle is not a regular file")
	}
	if info.Size() > maxBundleBytes {
		return Bundle{}, errors.New("config bundle exceeds size limit")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return Bundle{}, err
	}
	return decode(data)
}

func encode(bundle Bundle) ([]byte, error) {
	plain, err := json.Marshal(bundle)
	if err != nil {
		return nil, err
	}
	if len(plain) > maxBundleBytes {
		return nil, errors.New("config bundle payload exceeds size limit")
	}
	var compressed bytes.Buffer
	zipper := gzip.NewWriter(&compressed)
	if _, err := zipper.Write(plain); err != nil {
		return nil, err
	}
	if err := zipper.Close(); err != nil {
		return nil, err
	}
	aead, err := bundleAEAD()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	sealed := aead.Seal(nil, nonce, compressed.Bytes(), []byte(magic))
	result := make([]byte, 0, len(magic)+len(nonce)+len(sealed))
	result = append(result, []byte(magic)...)
	result = append(result, nonce...)
	result = append(result, sealed...)
	if len(result) > maxBundleBytes {
		return nil, errors.New("config bundle exceeds size limit")
	}
	return result, nil
}

func decode(data []byte) (Bundle, error) {
	if len(data) < len(magic) || string(data[:len(magic)]) != magic {
		return Bundle{}, errors.New("invalid config bundle header")
	}
	aead, err := bundleAEAD()
	if err != nil {
		return Bundle{}, err
	}
	offset := len(magic)
	if len(data) < offset+aead.NonceSize()+aead.Overhead() {
		return Bundle{}, errors.New("config bundle is truncated")
	}
	nonce := data[offset : offset+aead.NonceSize()]
	ciphertext := data[offset+aead.NonceSize():]
	compressed, err := aead.Open(nil, nonce, ciphertext, []byte(magic))
	if err != nil {
		return Bundle{}, errors.New("config bundle authentication failed")
	}
	zipper, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return Bundle{}, fmt.Errorf("open config bundle payload: %w", err)
	}
	defer zipper.Close()
	plain, err := io.ReadAll(io.LimitReader(zipper, maxBundleBytes+1))
	if err != nil {
		return Bundle{}, err
	}
	if len(plain) > maxBundleBytes {
		return Bundle{}, errors.New("config bundle payload exceeds size limit")
	}
	var bundle Bundle
	if err := json.Unmarshal(plain, &bundle); err != nil {
		return Bundle{}, fmt.Errorf("decode config bundle payload: %w", err)
	}
	if bundle.Files == nil {
		bundle.Files = []File{}
	}
	if bundle.Secrets == nil {
		bundle.Secrets = map[string]string{}
	}
	return bundle, nil
}

func bundleAEAD() (cipher.AEAD, error) {
	key := sha256.Sum256([]byte(legacyBundleKeyMaterialV1))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func directoryHasContent(root string) (bool, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return len(entries) > 0, nil
}

func uniqueSibling(root, kind string) string {
	parent, base := filepath.Dir(root), filepath.Base(root)
	for index := 0; ; index++ {
		candidate := filepath.Join(parent, fmt.Sprintf(".%s-%s-%d-%d", base, kind, time.Now().UnixNano(), index))
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
}

func absoluteClean(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("path is required")
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func within(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && (relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative))
}
