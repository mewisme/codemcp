package workspace024

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.mewis.me/codemcp/internal/checkpoint"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	statepkg "go.mewis.me/codemcp/internal/state"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

const (
	SourceRelease      = "0.2.24"
	ManifestVersion    = 1
	manifestName       = "migration.json"
	maxStructuredBytes = 16 << 20
)

type Input struct {
	SourceConfigRoot  string
	SourceWorkspaceID string
	LegacyWorkspaceID string
	TargetWorkspaceID string
	WorkspaceRoot     string
	Destination       string
}

type FileRecord struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type Manifest struct {
	Version           int          `json:"version"`
	SourceRelease     string       `json:"source_release"`
	SourceSHA256      string       `json:"source_sha256"`
	LegacyWorkspaceID string       `json:"legacy_workspace_id"`
	TargetWorkspaceID string       `json:"target_workspace_id"`
	WorkspaceRoot     string       `json:"workspace_root"`
	Files             []FileRecord `json:"files"`
}

type Result struct {
	Destination    string   `json:"destination"`
	Manifest       Manifest `json:"manifest"`
	AlreadyApplied bool     `json:"already_applied"`
}

type legacyShellState struct {
	WorkspaceID    string   `json:"workspace_id"`
	CWD            string   `json:"cwd"`
	StartedAt      string   `json:"started_at"`
	UpdatedAt      string   `json:"updated_at"`
	RecentCommands []string `json:"recent_commands"`
}

func Transform(input Input) (Result, error) {
	normalized, source, err := normalizeInput(input)
	if err != nil {
		return Result{}, err
	}
	expected, err := inspectSource(normalized, source)
	if err != nil {
		return Result{}, err
	}
	if info, err := os.Lstat(normalized.Destination); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return Result{}, fmt.Errorf("migration destination conflict: %s is not a real directory", normalized.Destination)
		}
		manifest, err := loadManifest(normalized.Destination)
		if err != nil {
			return Result{}, fmt.Errorf("migration destination conflict: %w", err)
		}
		if manifest.SourceSHA256 != expected.SourceSHA256 || manifest.LegacyWorkspaceID != expected.LegacyWorkspaceID || manifest.TargetWorkspaceID != expected.TargetWorkspaceID || filepath.Clean(manifest.WorkspaceRoot) != filepath.Clean(expected.WorkspaceRoot) {
			return Result{}, errors.New("migration destination conflict: existing manifest belongs to different workspace state")
		}
		if err := verifyDestination(normalized.Destination, manifest); err != nil {
			return Result{}, fmt.Errorf("migration destination conflict: %w", err)
		}
		return Result{Destination: normalized.Destination, Manifest: manifest, AlreadyApplied: true}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, fmt.Errorf("inspect migration destination: %w", err)
	}

	parent := filepath.Dir(normalized.Destination)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return Result{}, err
	}
	stage, err := os.MkdirTemp(parent, "."+filepath.Base(normalized.Destination)+".workspace024-")
	if err != nil {
		return Result{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(stage)
		}
	}()

	manifest, err := materialize(normalized, source, stage)
	if err != nil {
		return Result{}, err
	}
	if err := writeJSON(filepath.Join(stage, manifestName), manifest); err != nil {
		return Result{}, err
	}
	if err := verifyDestination(stage, manifest); err != nil {
		return Result{}, err
	}
	if err := os.Rename(stage, normalized.Destination); err != nil {
		return Result{}, fmt.Errorf("activate workspace migration staging output: %w", err)
	}
	committed = true
	return Result{Destination: normalized.Destination, Manifest: manifest}, nil
}

func normalizeInput(input Input) (Input, string, error) {
	input.LegacyWorkspaceID = strings.TrimSpace(input.LegacyWorkspaceID)
	input.SourceWorkspaceID = strings.TrimSpace(input.SourceWorkspaceID)
	input.TargetWorkspaceID = strings.TrimSpace(input.TargetWorkspaceID)
	if input.LegacyWorkspaceID == "" || input.TargetWorkspaceID == "" {
		return Input{}, "", errors.New("legacy and target workspace ids are required")
	}
	if input.SourceWorkspaceID == "" {
		input.SourceWorkspaceID = input.LegacyWorkspaceID
	}
	for label, id := range map[string]string{"source workspace id": input.SourceWorkspaceID, "legacy workspace id": input.LegacyWorkspaceID} {
		if err := workspacestate.ValidateIdentityID(id); err != nil {
			return Input{}, "", fmt.Errorf("%s: %w", label, err)
		}
	}
	if err := workspacestate.ValidateIdentityID(input.TargetWorkspaceID); err != nil {
		return Input{}, "", fmt.Errorf("target workspace id: %w", err)
	}
	for name, value := range map[string]string{"source config root": input.SourceConfigRoot, "workspace root": input.WorkspaceRoot, "destination": input.Destination} {
		if strings.TrimSpace(value) == "" {
			return Input{}, "", fmt.Errorf("%s is required", name)
		}
		absolute, err := filepath.Abs(value)
		if err != nil {
			return Input{}, "", fmt.Errorf("resolve %s: %w", name, err)
		}
		switch name {
		case "source config root":
			input.SourceConfigRoot = filepath.Clean(absolute)
		case "workspace root":
			input.WorkspaceRoot = filepath.Clean(absolute)
		case "destination":
			input.Destination = filepath.Clean(absolute)
		}
	}
	for _, path := range []string{input.SourceConfigRoot, input.WorkspaceRoot} {
		info, err := os.Lstat(path)
		if err != nil {
			return Input{}, "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return Input{}, "", fmt.Errorf("migration input is not a real directory: %s", path)
		}
	}
	source := filepath.Join(input.SourceConfigRoot, "workspaces", input.SourceWorkspaceID)
	info, err := os.Lstat(source)
	if err != nil {
		return Input{}, "", fmt.Errorf("released workspace state unavailable: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Input{}, "", errors.New("released workspace state must be a real directory")
	}
	if within(source, input.Destination) || within(input.Destination, source) || within(input.WorkspaceRoot, input.Destination) {
		return Input{}, "", errors.New("migration destination must be an external staging directory")
	}
	return input, source, nil
}

func inspectSource(input Input, source string) (Manifest, error) {
	entries, err := os.ReadDir(source)
	if err != nil {
		return Manifest{}, err
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return Manifest{}, fmt.Errorf("released workspace state contains symlink: %s", entry.Name())
		}
		switch entry.Name() {
		case "MEMORY.md", "shell.json", "checkpoints":
		default:
			return Manifest{}, fmt.Errorf("unsupported released workspace state entry: %s", entry.Name())
		}
	}
	if shellPath := filepath.Join(source, "shell.json"); fileExists(shellPath) {
		if err := validateLegacyShell(shellPath, input.LegacyWorkspaceID); err != nil {
			return Manifest{}, err
		}
	}
	if checkpointRoot := filepath.Join(source, "checkpoints"); dirExists(checkpointRoot) {
		if err := validateLegacyCheckpoints(checkpointRoot, input.LegacyWorkspaceID, input.WorkspaceRoot); err != nil {
			return Manifest{}, err
		}
	}
	fingerprint, err := fingerprintTree(source)
	if err != nil {
		return Manifest{}, err
	}
	return Manifest{Version: ManifestVersion, SourceRelease: SourceRelease, SourceSHA256: fingerprint, LegacyWorkspaceID: input.LegacyWorkspaceID, TargetWorkspaceID: input.TargetWorkspaceID, WorkspaceRoot: input.WorkspaceRoot, Files: []FileRecord{}}, nil
}

func materialize(input Input, source, stage string) (Manifest, error) {
	fingerprint, err := fingerprintTree(source)
	if err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{Version: ManifestVersion, SourceRelease: SourceRelease, SourceSHA256: fingerprint, LegacyWorkspaceID: input.LegacyWorkspaceID, TargetWorkspaceID: input.TargetWorkspaceID, WorkspaceRoot: input.WorkspaceRoot, Files: []FileRecord{}}
	if src := filepath.Join(source, "MEMORY.md"); fileExists(src) {
		if err := copyFileRecord(src, filepath.Join(stage, "memory", "MEMORY.md"), "memory", stage, &manifest); err != nil {
			return Manifest{}, err
		}
	}
	if src := filepath.Join(source, "shell.json"); fileExists(src) {
		var legacy legacyShellState
		if err := decodeStrictFile(src, &legacy); err != nil {
			return Manifest{}, err
		}
		current := shellruntime.SessionState{Version: 1, WorkspaceID: input.TargetWorkspaceID, CWD: legacy.CWD, StartedAt: legacy.StartedAt, UpdatedAt: legacy.UpdatedAt, RecentCommands: append([]string(nil), legacy.RecentCommands...)}
		dst := filepath.Join(stage, "state", "shell.json")
		if err := writeJSON(dst, current); err != nil {
			return Manifest{}, err
		}
		if err := appendRecord(dst, "shell", stage, &manifest); err != nil {
			return Manifest{}, err
		}
	}
	if src := filepath.Join(source, "checkpoints"); dirExists(src) {
		if err := copyCheckpointTree(src, filepath.Join(stage, "checkpoints"), input, stage, &manifest); err != nil {
			return Manifest{}, err
		}
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	return manifest, nil
}

func validateLegacyShell(path, legacyID string) error {
	var state legacyShellState
	if err := decodeStrictFile(path, &state); err != nil {
		return err
	}
	if state.WorkspaceID != legacyID {
		return fmt.Errorf("released shell state belongs to unexpected workspace %q", state.WorkspaceID)
	}
	if strings.TrimSpace(state.CWD) == "" {
		return errors.New("released shell state has empty cwd")
	}
	return nil
}

func validateLegacyCheckpoints(root, legacyID, workspaceRoot string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("released checkpoints must be a real directory")
	}
	indexPath := filepath.Join(root, "index.json")
	var index checkpoint.Index
	if err := decodeStrictFile(indexPath, &index); err != nil {
		return err
	}
	if index.Version != 1 {
		return fmt.Errorf("unsupported released checkpoint index version: %d", index.Version)
	}
	allowed := map[string]struct{}{"index.json": {}}
	for _, summary := range index.Checkpoints {
		if strings.TrimSpace(summary.ID) == "" {
			return errors.New("released checkpoint index contains empty id")
		}
		manifestRel := filepath.Join("data", summary.ID, "manifest.json")
		manifestPath := filepath.Join(root, manifestRel)
		var manifest checkpoint.Manifest
		if err := decodeStrictFile(manifestPath, &manifest); err != nil {
			return fmt.Errorf("load released checkpoint %s manifest: %w", summary.ID, err)
		}
		if manifest.Version != 1 || manifest.ID != summary.ID || manifest.WorkspaceID != legacyID {
			return fmt.Errorf("released checkpoint manifest ownership mismatch: %s", manifestPath)
		}
		if filepath.Clean(manifest.WorkspaceRoot) != filepath.Clean(workspaceRoot) {
			return fmt.Errorf("released checkpoint manifest root mismatch: %s", manifestPath)
		}
		allowed[filepath.ToSlash(manifestRel)] = struct{}{}
		for _, snapshot := range manifest.Files {
			if err := addSnapshotBlobs(allowed, summary.ID, snapshot); err != nil {
				return err
			}
		}
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("released checkpoint state contains symlink: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, ok := allowed[filepath.ToSlash(rel)]; !ok {
			return fmt.Errorf("unsupported released checkpoint state entry: %s", filepath.ToSlash(rel))
		}
		return nil
	})
}

func addSnapshotBlobs(allowed map[string]struct{}, checkpointID string, snapshot checkpoint.FileSnapshot) error {
	if snapshot.Blob != "" {
		clean := filepath.Clean(filepath.FromSlash(snapshot.Blob))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe released checkpoint blob path: %s", snapshot.Blob)
		}
		allowed[filepath.ToSlash(filepath.Join("data", checkpointID, clean))] = struct{}{}
	}
	for _, child := range snapshot.Children {
		if err := addSnapshotBlobs(allowed, checkpointID, child); err != nil {
			return err
		}
	}
	return nil
}

func copyCheckpointTree(srcRoot, dstRoot string, input Input, stage string, manifest *Manifest) error {
	return filepath.WalkDir(srcRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("released checkpoint state contains symlink: %s", path)
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dst := filepath.Join(dstRoot, rel)
		if entry.IsDir() {
			return os.MkdirAll(dst, 0700)
		}
		switch entry.Name() {
		case "manifest.json":
			var value checkpoint.Manifest
			if err := decodeStrictFile(path, &value); err != nil {
				return err
			}
			value.WorkspaceID = input.TargetWorkspaceID
			if err := writeJSON(dst, value); err != nil {
				return err
			}
			return appendRecord(dst, "checkpoint_manifest", stage, manifest)
		case "index.json":
			var value checkpoint.Index
			if err := decodeStrictFile(path, &value); err != nil {
				return err
			}
			if value.Version == 0 {
				value.Version = 1
			}
			if err := writeJSON(dst, value); err != nil {
				return err
			}
			return appendRecord(dst, "checkpoint_index", stage, manifest)
		default:
			return copyFileRecord(path, dst, "checkpoint_blob", stage, manifest)
		}
	})
}

func copyFileRecord(src, dst, kind, stage string, manifest *Manifest) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("migration source file is not regular: %s", src)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return appendRecord(dst, kind, stage, manifest)
}

func appendRecord(path, kind, stage string, manifest *Manifest) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(stage, path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	manifest.Files = append(manifest.Files, FileRecord{Kind: kind, Path: filepath.ToSlash(rel), SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(data))})
	return nil
}

func verifyDestination(root string, manifest Manifest) error {
	if manifest.Version != ManifestVersion || manifest.SourceRelease != SourceRelease || strings.TrimSpace(manifest.SourceSHA256) == "" {
		return errors.New("invalid workspace migration manifest")
	}
	expected := map[string]struct{}{manifestName: {}}
	for _, record := range manifest.Files {
		clean := filepath.Clean(filepath.FromSlash(record.Path))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe migration record path: %s", record.Path)
		}
		key := filepath.ToSlash(clean)
		if key == manifestName {
			return errors.New("migration manifest cannot describe itself as payload")
		}
		if _, duplicate := expected[key]; duplicate {
			return fmt.Errorf("duplicate migration record path: %s", record.Path)
		}
		path := filepath.Join(root, clean)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != record.SHA256 || int64(len(data)) != record.Bytes {
			return fmt.Errorf("migration record integrity mismatch: %s", record.Path)
		}
		expected[key] = struct{}{}
	}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("migration output contains symlink: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if _, ok := expected[key]; !ok {
			return fmt.Errorf("migration output contains unexpected file: %s", key)
		}
		delete(expected, key)
		return nil
	}); err != nil {
		return err
	}
	for path := range expected {
		return fmt.Errorf("migration output is missing file: %s", path)
	}
	return nil
}

func fingerprintTree(root string) (string, error) {
	type entryHash struct {
		path string
		sum  string
	}
	entries := []entryHash{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("released workspace state contains symlink: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("released workspace state contains non-regular file: %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		entries = append(entries, entryHash{path: filepath.ToSlash(rel), sum: hex.EncodeToString(sum[:])})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	hash := sha256.New()
	for _, entry := range entries {
		_, _ = io.WriteString(hash, entry.path)
		_, _ = io.WriteString(hash, "\x00")
		_, _ = io.WriteString(hash, entry.sum)
		_, _ = io.WriteString(hash, "\n")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func loadManifest(root string) (Manifest, error) {
	var manifest Manifest
	if err := decodeStrictFile(filepath.Join(root, manifestName), &manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func decodeStrictFile(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() > maxStructuredBytes {
		return fmt.Errorf("decode %s: structured state exceeds %d bytes", path, maxStructuredBytes)
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxStructuredBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	if decoder.More() {
		return fmt.Errorf("decode %s: multiple JSON values", path)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("decode %s: multiple JSON values", path)
		}
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(bytes.TrimSpace(data), '\n')
	return statepkg.WriteFileAtomic(path, data, 0600)
}

func fileExists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
}

func dirExists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

func within(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
