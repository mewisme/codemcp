package configbundle

import (
	"bytes"
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
	Version              = 1
	SecretPolicyExcluded = "excluded"
	maxBundleBytes       = 256 << 20
	maxStateBytes        = 128 << 20
	maxBundleFileBytes   = 64 << 20
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

type Envelope struct {
	Version      int       `json:"version"`
	CreatedAt    time.Time `json:"created_at"`
	Source       Platform  `json:"source"`
	SecretPolicy string    `json:"secret_policy"`
	Files        []File    `json:"files"`
}

type Bundle = Envelope

type ExportOptions struct {
	Force bool
}

type ExportResult struct {
	Path         string
	Files        int
	SkippedFiles int
	Source       Platform
}

type ImportOptions struct {
	Force bool
}

type ImportResult struct {
	Files        int
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
	platform := currentPlatform()
	envelope := Envelope{Version: Version, CreatedAt: time.Now().UTC(), Source: platform, SecretPolicy: SecretPolicyExcluded, Files: files}
	encoded, err := encode(envelope)
	if err != nil {
		return ExportResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return ExportResult{}, err
	}
	if err := state.WriteFileAtomic(destination, encoded, 0600); err != nil {
		return ExportResult{}, err
	}
	return ExportResult{Path: destination, Files: len(files), SkippedFiles: skippedFiles, Source: platform}, nil
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
	bundle, err := readEnvelope(source)
	if err != nil {
		return ImportResult{}, err
	}
	if err := validateEnvelope(bundle); err != nil {
		return ImportResult{}, err
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
	stageActive := true
	defer func() {
		if stageActive {
			_ = os.RemoveAll(stage)
		}
	}()
	target := currentPlatform()
	materialized, err := materialize(stage, bundle, target)
	if err != nil {
		return ImportResult{}, err
	}
	if hasTarget {
		if err := mergeImportedMainConfig(root, stage); err != nil {
			return ImportResult{}, err
		}
		if err := preserveExistingSecrets(root, stage); err != nil {
			return ImportResult{}, err
		}
	}
	if err := configformat.MarkRoot(stage); err != nil {
		return ImportResult{}, err
	}
	if _, err := config.VerifyAt(stage); err != nil {
		return ImportResult{}, fmt.Errorf("verify imported configuration before activation: %w", err)
	}
	if hasTarget {
		if err := rebindExistingSecretStore(root, stage); err != nil {
			return ImportResult{}, fmt.Errorf("prepare preserved secrets for activation: %w", err)
		}
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
	stageActive = false
	if _, err := config.VerifyAt(root); err != nil {
		return ImportResult{}, rollback(fmt.Errorf("verify imported configuration: %w", err))
	}
	return ImportResult{
		Files: materialized.files, SkippedPaths: materialized.skippedPaths,
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
		data, err = presentationSafeFile(relative, data)
		if err != nil {
			return err
		}
		total += int64(len(data))
		if total > maxStateBytes {
			return errors.New("config envelope payload exceeds size limit")
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
	if relative == ".runtime-control.json" || relative == "tunnel.json" || relative == "state/instance.json" || relative == "state/update.json" {
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

func presentationSafeFile(relative string, data []byte) ([]byte, error) {
	switch pathpkg.Clean(relative) {
	case "config.json":
		return presentationSafeConfig(data)
	case "oauth.json":
		return presentationSafeOAuth(data)
	case "upstream.json":
		return presentationSafeUpstream(data)
	default:
		return data, nil
	}
}

func presentationSafeConfig(data []byte) ([]byte, error) {
	decoded, err := configformat.DecodeGeneric(configformat.JSON, data)
	if err != nil {
		return nil, fmt.Errorf("decode config for export: %w", err)
	}
	root, ok := decoded.(map[string]any)
	if !ok {
		return nil, errors.New("config export requires an object root")
	}
	if auth, ok := root["auth"].(map[string]any); ok {
		delete(auth, "mcp_token_hash")
		delete(auth, "admin_token_hash")
	}
	if tunnel, ok := root["tunnel"].(map[string]any); ok {
		delete(tunnel, "api_key")
		delete(tunnel, "admin_key")
	}
	return configformat.EncodeGeneric(configformat.JSON, root)
}

func presentationSafeOAuth(data []byte) ([]byte, error) {
	decoded, err := configformat.DecodeGeneric(configformat.JSON, data)
	if err != nil {
		return nil, fmt.Errorf("decode OAuth state for export: %w", err)
	}
	root, ok := decoded.(map[string]any)
	if !ok {
		return nil, errors.New("OAuth export requires an object root")
	}
	credentials, _ := root["credentials"].(map[string]any)
	for _, value := range credentials {
		credential, ok := value.(map[string]any)
		if !ok {
			continue
		}
		delete(credential, "client_secret")
		delete(credential, "access_token")
		delete(credential, "refresh_token")
	}
	return configformat.EncodeGeneric(configformat.JSON, root)
}

func presentationSafeUpstream(data []byte) ([]byte, error) {
	decoded, err := configformat.DecodeGeneric(configformat.JSON, data)
	if err != nil {
		return nil, fmt.Errorf("decode upstream state for export: %w", err)
	}
	root, ok := decoded.(map[string]any)
	if !ok {
		return nil, errors.New("upstream export requires an object root")
	}
	servers, _ := root["servers"].([]any)
	for _, value := range servers {
		server, ok := value.(map[string]any)
		if !ok {
			continue
		}
		for _, field := range []string{"headers", "env"} {
			values, _ := server[field].(map[string]any)
			for key := range values {
				if upstream.SensitiveConfigKey(key) {
					delete(values, key)
				}
			}
		}
	}
	return configformat.EncodeGeneric(configformat.JSON, root)
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
			return result, fmt.Errorf("config envelope contains unsafe path: %q", item.Path)
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
			return result, fmt.Errorf("config envelope path collision after platform normalization: %s (%s, %s)", relative, previous, item.Path)
		}
		written[relative] = item.Path
		destination := filepath.Join(root, filepath.FromSlash(relative))
		if !within(root, destination) {
			return result, fmt.Errorf("config envelope path escapes target root: %s", relative)
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
		return nil, 0, fmt.Errorf("decode envelope config: %w", err)
	}
	root, ok := raw.(map[string]any)
	if !ok {
		return nil, 0, errors.New("envelope config must be an object")
	}
	cfg := config.Default()
	if err := configformat.Unmarshal(configformat.JSON, data, &cfg); err != nil {
		return nil, 0, fmt.Errorf("decode envelope config: %w", err)
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
	var registry workspaceRegistry
	if err := configformat.Unmarshal(configformat.JSON, file.Data, &registry); err != nil {
		return nil, nil, nil, 0, fmt.Errorf("decode envelope workspace registry: %w", err)
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
		item.ID = oldID
		mapping[oldID] = item.ID
		roots[item.ID] = item.Path
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	registry.Workspaces = items
	encoded, err := configformat.Marshal(configformat.JSON, registry)
	if err != nil {
		return nil, nil, nil, 0, err
	}
	return encoded, mapping, roots, skipped, nil
}

func normalizeWorkspaceState(relative string, data []byte, oldID, newID, workspaceRoot string, source, target Platform) ([]byte, error) {
	if !strings.EqualFold(filepath.Ext(relative), ".json") {
		return data, nil
	}
	decoded, err := configformat.DecodeGeneric(configformat.JSON, data)
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
	return configformat.EncodeGeneric(configformat.JSON, object)
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
	return strings.EqualFold(filepath.Ext(relative), ".json")
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

func readEnvelope(file string) (Bundle, error) {
	info, err := os.Stat(file)
	if err != nil {
		return Bundle{}, err
	}
	if !info.Mode().IsRegular() {
		return Bundle{}, errors.New("config envelope is not a regular file")
	}
	if info.Size() > maxBundleBytes {
		return Bundle{}, errors.New("config envelope exceeds size limit")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return Bundle{}, err
	}
	return decode(data)
}

func encode(bundle Bundle) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(bundle); err != nil {
		return nil, err
	}
	if buffer.Len() > maxBundleBytes {
		return nil, errors.New("config envelope exceeds size limit")
	}
	return buffer.Bytes(), nil
}

func decode(data []byte) (Bundle, error) {
	if len(data) > maxBundleBytes {
		return Bundle{}, errors.New("config envelope exceeds size limit")
	}
	var bundle Bundle
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return Bundle{}, fmt.Errorf("decode config envelope: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Bundle{}, errors.New("config envelope contains multiple JSON values")
		}
		return Bundle{}, fmt.Errorf("decode config envelope trailing data: %w", err)
	}
	if bundle.Files == nil {
		bundle.Files = []File{}
	}
	return bundle, nil
}

func validateEnvelope(envelope Envelope) error {
	if envelope.Version != Version {
		return fmt.Errorf("unsupported config envelope version: %d", envelope.Version)
	}
	if envelope.CreatedAt.IsZero() {
		return errors.New("config envelope created_at is missing")
	}
	if strings.TrimSpace(envelope.Source.OS) == "" || strings.TrimSpace(envelope.Source.Arch) == "" {
		return errors.New("config envelope source platform is incomplete")
	}
	if envelope.SecretPolicy != SecretPolicyExcluded {
		return fmt.Errorf("unsupported config envelope secret policy: %q", envelope.SecretPolicy)
	}
	if len(envelope.Files) == 0 {
		return errors.New("config envelope contains no files")
	}
	seen := map[string]bool{}
	total := int64(0)
	hasConfig := false
	for _, file := range envelope.Files {
		relative, ok := safeRelative(file.Path)
		if !ok {
			return fmt.Errorf("config envelope contains unsafe path: %q", file.Path)
		}
		if excludedFile(relative) {
			return fmt.Errorf("config envelope contains non-portable or secret state: %s", relative)
		}
		if seen[relative] {
			return fmt.Errorf("config envelope contains duplicate path: %s", relative)
		}
		seen[relative] = true
		if relative == "config.json" {
			hasConfig = true
		}
		if len(file.Data) > maxBundleFileBytes {
			return fmt.Errorf("config envelope file exceeds size limit: %s", relative)
		}
		sensitive, err := containsSensitiveState(relative, file.Data)
		if err != nil {
			return err
		}
		if sensitive {
			return fmt.Errorf("config envelope contains sensitive state: %s", relative)
		}
		total += int64(len(file.Data))
		if total > maxStateBytes {
			return errors.New("config envelope state exceeds size limit")
		}
	}
	if !hasConfig {
		return errors.New("config envelope is missing config.json")
	}
	return nil
}

func containsSensitiveState(relative string, data []byte) (bool, error) {
	switch pathpkg.Clean(relative) {
	case "config.json":
		decoded, err := configformat.DecodeGeneric(configformat.JSON, data)
		if err != nil {
			return false, fmt.Errorf("decode config envelope file %s: %w", relative, err)
		}
		root, ok := decoded.(map[string]any)
		if !ok {
			return false, errors.New("config envelope config.json must contain an object")
		}
		if auth, ok := root["auth"].(map[string]any); ok {
			for _, key := range []string{"mcp_token_hash", "admin_token_hash"} {
				if value, exists := auth[key]; exists && strings.TrimSpace(fmt.Sprint(value)) != "" {
					return true, nil
				}
			}
		}
		if tunnel, ok := root["tunnel"].(map[string]any); ok {
			for _, key := range []string{"api_key", "admin_key"} {
				if value, exists := tunnel[key]; exists && strings.TrimSpace(fmt.Sprint(value)) != "" {
					return true, nil
				}
			}
		}
	case "oauth.json":
		decoded, err := configformat.DecodeGeneric(configformat.JSON, data)
		if err != nil {
			return false, fmt.Errorf("decode OAuth envelope file: %w", err)
		}
		root, _ := decoded.(map[string]any)
		credentials, _ := root["credentials"].(map[string]any)
		for _, value := range credentials {
			credential, _ := value.(map[string]any)
			for _, key := range []string{"client_secret", "access_token", "refresh_token"} {
				if value, exists := credential[key]; exists && strings.TrimSpace(fmt.Sprint(value)) != "" {
					return true, nil
				}
			}
		}
	case "upstream.json":
		decoded, err := configformat.DecodeGeneric(configformat.JSON, data)
		if err != nil {
			return false, fmt.Errorf("decode upstream envelope file: %w", err)
		}
		root, _ := decoded.(map[string]any)
		servers, _ := root["servers"].([]any)
		for _, value := range servers {
			server, _ := value.(map[string]any)
			for _, field := range []string{"headers", "env"} {
				values, _ := server[field].(map[string]any)
				for key, value := range values {
					if upstream.SensitiveConfigKey(key) && strings.TrimSpace(fmt.Sprint(value)) != "" {
						return true, nil
					}
				}
			}
		}
	}
	return false, nil
}

func preserveExistingSecrets(existingRoot, stagedRoot string) error {
	if err := preserveExistingSecretMetadata(existingRoot, stagedRoot); err != nil {
		return err
	}
	names := map[string]bool{}
	add := func(values []string) {
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				names[value] = true
			}
		}
	}
	tunnelNames, err := config.TunnelSecretEntries(existingRoot)
	if err != nil {
		return err
	}
	add(tunnelNames)
	oauthNames, err := oauth.NewStore(configformat.StructuredPath(existingRoot, "oauth")).SecretEntries()
	if err != nil {
		return err
	}
	add(oauthNames)
	upstreamNames, err := upstream.NewStore(configformat.StructuredPath(existingRoot, "upstream")).SecretEntries()
	if err != nil {
		return err
	}
	add(upstreamNames)
	optionalRelay := secretstore.Name("cluster", "relay-token")
	names[optionalRelay] = true
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	source := secretstore.New(existingRoot)
	changes := make([]secretstore.Change, 0, len(ordered))
	for _, name := range ordered {
		value, err := source.Get(name)
		if errors.Is(err, secretstore.ErrNotFound) && name == optionalRelay {
			continue
		}
		if err != nil {
			return fmt.Errorf("read existing secret %s: %w", name, err)
		}
		changes = append(changes, secretstore.Change{Name: name, Value: value})
	}
	if len(changes) == 0 {
		return nil
	}
	if err := secretstore.New(stagedRoot).Apply(changes); err != nil {
		return fmt.Errorf("stage existing secrets: %w", err)
	}
	return nil
}

func preserveExistingSecretMetadata(existingRoot, stagedRoot string) error {
	root, err := os.OpenRoot(existingRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	file, err := root.Open("tunnel.json")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maxBundleFileBytes {
		_ = file.Close()
		return errors.New("existing tunnel secret metadata is not a bounded regular file")
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxBundleFileBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	if len(data) > maxBundleFileBytes {
		return errors.New("existing tunnel secret metadata exceeds size limit")
	}
	return state.WriteFileAtomic(filepath.Join(stagedRoot, "tunnel.json"), data, 0600)
}

func rebindExistingSecretStore(existingRoot, stagedRoot string) error {
	source := filepath.Join(existingRoot, "state", "secrets")
	destination := filepath.Join(stagedRoot, "state", "secrets")
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	info, err := os.Stat(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("existing secret store is not a directory")
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		return err
	}
	defer root.Close()
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return os.MkdirAll(destination, 0700)
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("existing secret store contains symlink: %s", relative)
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("existing secret store contains non-regular file: %s", relative)
		}
		file, err := root.Open(relative)
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maxBundleFileBytes+1))
		closeErr := file.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if len(data) > maxBundleFileBytes {
			return fmt.Errorf("existing secret store file exceeds size limit: %s", relative)
		}
		return state.WriteFileAtomic(target, data, 0600)
	})
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
