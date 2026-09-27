package checkpoint

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/idgen"
	"go.mewis.me/codemcp/internal/state"
	"go.mewis.me/codemcp/internal/workspace"
)

const (
	indexVersion      = 1
	defaultMaxCount   = 500
	defaultRetention  = 30 * 24 * time.Hour
	defaultMaxFile    = 5 * 1024 * 1024
	maxDirectoryDepth = 32
	maxIndexBytes     = 16 << 20
	maxIndexEntries   = 100_000
)

type Store struct {
	Root              string
	Workspaces        *workspace.Manager
	MaxCount          int
	Retention         time.Duration
	MaxFileBytes      int64
	MaxDirectoryDepth int
	mu                sync.Mutex
}

type FileSnapshot struct {
	Path        string         `json:"path"`
	Existed     bool           `json:"existed"`
	IsDirectory bool           `json:"is_directory,omitempty"`
	IsSymlink   bool           `json:"is_symlink,omitempty"`
	Mode        uint32         `json:"mode,omitempty"`
	Encoding    string         `json:"encoding,omitempty"`
	Content     string         `json:"content,omitempty"`
	Blob        string         `json:"blob,omitempty"`
	BlobSHA256  string         `json:"blob_sha256,omitempty"`
	Size        int64          `json:"size,omitempty"`
	LinkTarget  string         `json:"link_target,omitempty"`
	Children    []FileSnapshot `json:"children,omitempty"`
	Skipped     bool           `json:"skipped,omitempty"`
	SkipReason  string         `json:"skip_reason,omitempty"`
}

type Summary struct {
	ID        string   `json:"id"`
	CreatedAt string   `json:"created_at"`
	Tool      string   `json:"tool"`
	Summary   string   `json:"summary"`
	Files     []string `json:"files"`
	FileCount int      `json:"file_count"`
}

type Manifest struct {
	Version       int            `json:"version"`
	ID            string         `json:"id"`
	WorkspaceID   string         `json:"workspace_id"`
	WorkspaceRoot string         `json:"workspace_root"`
	AllowedRoots  []string       `json:"allowed_roots,omitempty"`
	CreatedAt     string         `json:"created_at"`
	Tool          string         `json:"tool"`
	Summary       string         `json:"summary"`
	Files         []FileSnapshot `json:"files"`
}

type Index struct {
	Version     int       `json:"version"`
	Checkpoints []Summary `json:"checkpoints"`
}

type RestoreChange struct {
	Path              string `json:"path"`
	Action            string `json:"action"`
	ExistedBeforeEdit bool   `json:"existed_before_edit"`
	Reason            string `json:"reason,omitempty"`
}

type Preview struct {
	Checkpoint       Summary         `json:"checkpoint"`
	Changes          []RestoreChange `json:"changes"`
	SkippedSnapshots []RestoreChange `json:"skipped_snapshots"`
}

type RestoreResult struct {
	Checkpoint    Summary         `json:"checkpoint"`
	Restored      []string        `json:"restored"`
	RestoredCount int             `json:"restored_count"`
	Deleted       []string        `json:"deleted"`
	Skipped       []RestoreChange `json:"skipped"`
	Archived      int             `json:"archived"`
}

type ClearResult struct {
	Cleared  int `json:"cleared"`
	Archived int `json:"archived"`
}

type PurgeResult struct {
	Purged         int `json:"purged"`
	ActivePurged   int `json:"active_purged"`
	ArchivedPurged int `json:"archived_purged"`
}

type restoreSnapshot struct {
	CheckpointID string
	Snapshot     FileSnapshot
}

type restoreRollback struct {
	CheckpointID string
	Snapshots    map[string]FileSnapshot
}

func DefaultRoot() string {
	return configformat.RootPath()
}

func NewStore(root string) *Store {
	return &Store{Root: root, MaxCount: defaultMaxCount, Retention: defaultRetention, MaxFileBytes: defaultMaxFile, MaxDirectoryDepth: maxDirectoryDepth}
}

func NewWorkspaceStore(root string, workspaces *workspace.Manager) *Store {
	store := NewStore(root)
	store.Workspaces = workspaces
	return store
}

func (s *Store) Path(workspaceID string) string {
	if s.Workspaces != nil {
		if local, err := s.Workspaces.LocalState(workspaceID); err == nil {
			return local.CheckpointRoot()
		}
	}
	return filepath.Join(s.Root, "workspaces", workspaceID, "checkpoints")
}

func (s *Store) Ensure(workspaceID string) error {
	return os.MkdirAll(s.Path(workspaceID), 0700)
}

func (s *Store) Config(workspaceID string) map[string]any {
	return map[string]any{
		"enabled":           true,
		"store_path":        s.Path(workspaceID),
		"max_count":         s.maxCount(),
		"retention_days":    int(s.retention().Hours() / 24),
		"inline_file_bytes": s.maxFileBytes(),
		"max_file_bytes":    s.maxFileBytes(),
		"note":              "File-editing MCP tools are tracked. Retention, restore, and clear preserve history in the archive; purge permanently removes active and archived history. Large files use checkpoint blobs; shell command file changes are not captured.",
	}
}

func (s *Store) Before(workspaceID, workspaceRoot, tool string, paths []string, dryRun bool) (string, error) {
	return s.BeforeAllowed(workspaceID, workspaceRoot, []string{workspaceRoot}, tool, paths, dryRun)
}

func (s *Store) BeforeAllowed(workspaceID, workspaceRoot string, allowedRoots []string, tool string, paths []string, dryRun bool) (string, error) {
	if dryRun {
		return "", nil
	}
	allowedRoots = effectiveRoots(workspaceRoot, allowedRoots)
	unique := uniquePaths(paths)
	if len(unique) == 0 {
		return "", nil
	}
	for _, path := range unique {
		if !withinAny(allowedRoots, path) {
			return "", fmt.Errorf("checkpoint path escapes workspace: %s", path)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	roots, err := openRestoreRoots(allowedRoots)
	if err != nil {
		return "", err
	}
	defer roots.Close()

	id, err := checkpointID()
	if err != nil {
		return "", err
	}
	dir := s.checkpointDir(workspaceID, id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(dir)
		}
	}()

	snapshots := make([]FileSnapshot, 0, len(unique))
	for _, path := range unique {
		snapshot, err := s.snapshot(workspaceID, id, roots, path, 0)
		if err != nil {
			return "", err
		}
		snapshots = append(snapshots, snapshot)
	}

	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	names := make([]string, len(unique))
	for i, path := range unique {
		names[i] = filepath.Base(path)
	}
	manifest := Manifest{
		Version:       indexVersion,
		ID:            id,
		WorkspaceID:   workspaceID,
		WorkspaceRoot: filepath.Clean(workspaceRoot),
		AllowedRoots:  allowedRoots,
		CreatedAt:     createdAt,
		Tool:          tool,
		Summary:       fmt.Sprintf("%s: %d path(s) - %s", tool, len(unique), strings.Join(names, ", ")),
		Files:         snapshots,
	}

	if err := writeStructuredAtomic(s.manifestPath(workspaceID, id), manifest, 0600); err != nil {
		return "", err
	}

	index, err := s.readIndex(workspaceID)
	if err != nil {
		return "", err
	}
	index.Checkpoints = append(index.Checkpoints, buildSummary(manifest))
	archived, err := s.pruneLocked(workspaceID, &index)
	if err != nil {
		return "", err
	}
	if err := s.writeIndex(workspaceID, index); err != nil {
		return "", err
	}
	committed = true
	for _, summary := range archived {
		_ = os.RemoveAll(s.checkpointDir(workspaceID, summary.ID))
	}
	return id, nil
}

func (s *Store) List(workspaceID string, limit int) ([]Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.readIndex(workspaceID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	result := make([]Summary, 0, minInt(limit, len(index.Checkpoints)))
	for i := len(index.Checkpoints) - 1; i >= 0 && len(result) < limit; i-- {
		result = append(result, index.Checkpoints[i])
	}
	return result, nil
}

func (s *Store) Get(workspaceID, id string) (*Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.readIndex(workspaceID)
	if err != nil {
		return nil, err
	}
	for _, summary := range index.Checkpoints {
		if summary.ID == id {
			value := summary
			return &value, nil
		}
	}
	return nil, nil
}

func (s *Store) PreviewRestore(workspaceID, workspaceRoot, id string) (Preview, error) {
	return s.PreviewRestoreAllowed(workspaceID, workspaceRoot, []string{workspaceRoot}, id)
}

func (s *Store) PreviewRestoreAllowed(workspaceID, workspaceRoot string, allowedRoots []string, id string) (Preview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	allowedRoots = effectiveRoots(workspaceRoot, allowedRoots)
	target, snapshots, err := s.collectRestorePlanLocked(workspaceID, workspaceRoot, allowedRoots, id)
	if err != nil {
		return Preview{}, err
	}
	roots, err := openRestoreRoots(allowedRoots)
	if err != nil {
		return Preview{}, err
	}
	defer roots.Close()
	changes := make([]RestoreChange, 0, len(snapshots))
	skipped := make([]RestoreChange, 0)
	for path, stored := range snapshots {
		snapshot := stored.Snapshot
		if snapshot.Skipped {
			change := RestoreChange{Path: path, Action: "skip", ExistedBeforeEdit: snapshot.Existed, Reason: snapshot.SkipReason}
			changes = append(changes, change)
			skipped = append(skipped, change)
			continue
		}
		root, relative, err := roots.target(path)
		if err != nil {
			return Preview{}, err
		}
		_, statErr := root.Lstat(relative)
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return Preview{}, statErr
		}
		existsNow := statErr == nil
		if !snapshot.Existed {
			if existsNow {
				changes = append(changes, RestoreChange{Path: path, Action: "delete", ExistedBeforeEdit: false, Reason: "file was created after checkpoint"})
			} else {
				changes = append(changes, RestoreChange{Path: path, Action: "skip", ExistedBeforeEdit: false, Reason: "already absent"})
			}
			continue
		}
		changes = append(changes, RestoreChange{Path: path, Action: "restore", ExistedBeforeEdit: true})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].Path < skipped[j].Path })
	return Preview{Checkpoint: target, Changes: changes, SkippedSnapshots: skipped}, nil
}

func (s *Store) Restore(workspaceID, workspaceRoot, id string) (RestoreResult, error) {
	return s.RestoreAllowed(workspaceID, workspaceRoot, []string{workspaceRoot}, id)
}

func (s *Store) RestoreAllowed(workspaceID, workspaceRoot string, allowedRoots []string, id string) (RestoreResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	allowedRoots = effectiveRoots(workspaceRoot, allowedRoots)
	target, snapshots, err := s.collectRestorePlanLocked(workspaceID, workspaceRoot, allowedRoots, id)
	if err != nil {
		return RestoreResult{}, err
	}
	index, err := s.readIndex(workspaceID)
	if err != nil {
		return RestoreResult{}, err
	}
	targetIndex := summaryIndex(index.Checkpoints, id)
	if targetIndex < 0 {
		return RestoreResult{}, fmt.Errorf("checkpoint not found: %s", id)
	}
	tail := append([]Summary(nil), index.Checkpoints[targetIndex:]...)
	if err := s.validateActivePayloadsLocked(workspaceID, tail); err != nil {
		return RestoreResult{}, err
	}
	roots, err := openRestoreRoots(allowedRoots)
	if err != nil {
		return RestoreResult{}, err
	}
	defer roots.Close()
	for _, stored := range snapshots {
		if err := validateSnapshotPathAllowed(roots, stored.Snapshot); err != nil {
			return RestoreResult{}, err
		}
	}
	rollback, err := s.captureRestoreRollbackLocked(workspaceID, roots, snapshots)
	if err != nil {
		return RestoreResult{}, err
	}
	defer os.RemoveAll(s.checkpointDir(workspaceID, rollback.CheckpointID))
	type pair struct {
		path   string
		stored restoreSnapshot
	}
	ordered := make([]pair, 0, len(snapshots))
	for path, stored := range snapshots {
		ordered = append(ordered, pair{path: path, stored: stored})
	}
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i].path) > len(ordered[j].path) })

	result := RestoreResult{Checkpoint: target, Restored: []string{}, Deleted: []string{}, Skipped: []RestoreChange{}}
	for _, item := range ordered {
		snapshot := item.stored.Snapshot
		if snapshot.Skipped {
			result.Skipped = append(result.Skipped, RestoreChange{Path: item.path, Action: "skip", ExistedBeforeEdit: snapshot.Existed, Reason: snapshot.SkipReason})
			continue
		}
		if !snapshot.Existed {
			if err := roots.RemoveAll(item.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return RestoreResult{}, errors.Join(err, s.restoreRollbackLocked(workspaceID, roots, rollback))
			}
			result.Deleted = append(result.Deleted, item.path)
			continue
		}
		blobRoot := s.checkpointDir(workspaceID, item.stored.CheckpointID)
		if err := roots.Restore(snapshot, blobRoot); err != nil {
			return RestoreResult{}, errors.Join(err, s.restoreRollbackLocked(workspaceID, roots, rollback))
		}
		result.Restored = append(result.Restored, item.path)
	}
	candidates := make([]archiveCandidate, 0, len(tail))
	for _, summary := range tail {
		candidates = append(candidates, archiveCandidate{Summary: summary, Reason: ArchiveReasonRestore})
	}
	archived, err := s.archiveAndReplaceActiveLocked(workspaceID, index, index.Checkpoints[:targetIndex], candidates)
	if err != nil {
		return RestoreResult{}, errors.Join(err, s.restoreRollbackLocked(workspaceID, roots, rollback))
	}
	result.RestoredCount = len(result.Restored)
	result.Archived = archived
	return result, nil
}

func (s *Store) Clear(workspaceID string) (ClearResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.readIndex(workspaceID)
	if err != nil {
		return ClearResult{}, err
	}
	if len(index.Checkpoints) == 0 {
		return ClearResult{}, nil
	}
	candidates := make([]archiveCandidate, 0, len(index.Checkpoints))
	for _, summary := range index.Checkpoints {
		candidates = append(candidates, archiveCandidate{Summary: summary, Reason: ArchiveReasonClear})
	}
	archived, err := s.archiveAndReplaceActiveLocked(workspaceID, index, nil, candidates)
	if err != nil {
		return ClearResult{}, err
	}
	return ClearResult{Cleared: len(index.Checkpoints), Archived: archived}, nil
}

func (s *Store) Purge(workspaceID string) (PurgeResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	active, err := s.readIndex(workspaceID)
	if err != nil {
		return PurgeResult{}, err
	}
	archived, err := s.readArchiveIndex(workspaceID)
	if err != nil {
		return PurgeResult{}, err
	}
	result := PurgeResult{
		Purged:         len(active.Checkpoints) + len(archived.Checkpoints),
		ActivePurged:   len(active.Checkpoints),
		ArchivedPurged: len(archived.Checkpoints),
	}
	root := s.Path(workspaceID)
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		if err := s.writeIndex(workspaceID, Index{Version: indexVersion, Checkpoints: []Summary{}}); err != nil {
			return PurgeResult{}, err
		}
		return result, nil
	}
	if err != nil {
		return PurgeResult{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return PurgeResult{}, fmt.Errorf("checkpoint root is not a stable directory: %s", root)
	}
	purgeID, err := idgen.New("purge", 6)
	if err != nil {
		return PurgeResult{}, err
	}
	staging := filepath.Join(filepath.Dir(root), "."+filepath.Base(root)+"."+purgeID)
	if err := os.Rename(root, staging); err != nil {
		return PurgeResult{}, err
	}
	rollback := func(operationErr error) error {
		removeErr := os.RemoveAll(root)
		renameErr := os.Rename(staging, root)
		return errors.Join(operationErr, removeErr, renameErr)
	}
	if err := s.writeIndex(workspaceID, Index{Version: indexVersion, Checkpoints: []Summary{}}); err != nil {
		return PurgeResult{}, rollback(err)
	}
	if err := os.RemoveAll(staging); err != nil {
		return PurgeResult{}, fmt.Errorf("checkpoint history purge committed but staged cleanup failed: %w", err)
	}
	return result, nil
}

func (s *Store) validateActivePayloadsLocked(workspaceID string, summaries []Summary) error {
	for _, summary := range summaries {
		if err := validateCheckpointPayloadDir(s.checkpointDir(workspaceID, summary.ID), workspaceID, summary); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) captureRestoreRollbackLocked(workspaceID string, roots restoreRoots, snapshots map[string]restoreSnapshot) (restoreRollback, error) {
	id, err := checkpointID()
	if err != nil {
		return restoreRollback{}, err
	}
	dir := s.checkpointDir(workspaceID, id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return restoreRollback{}, err
	}
	rollback := restoreRollback{CheckpointID: id, Snapshots: make(map[string]FileSnapshot, len(snapshots))}
	for path := range snapshots {
		snapshot, err := s.snapshot(workspaceID, id, roots, path, 0)
		if err != nil {
			_ = os.RemoveAll(dir)
			return restoreRollback{}, err
		}
		rollback.Snapshots[path] = snapshot
	}
	return rollback, nil
}

func (s *Store) restoreRollbackLocked(workspaceID string, roots restoreRoots, rollback restoreRollback) error {
	paths := make([]string, 0, len(rollback.Snapshots))
	for path := range rollback.Snapshots {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool { return len(paths[i]) > len(paths[j]) })
	var result error
	for _, path := range paths {
		snapshot := rollback.Snapshots[path]
		if !snapshot.Existed {
			if err := roots.RemoveAll(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				result = errors.Join(result, err)
			}
			continue
		}
		if snapshot.IsDirectory {
			if root, relative, err := roots.target(path); err != nil {
				result = errors.Join(result, err)
				continue
			} else if err := pruneRollbackDirectory(root, relative, snapshot); err != nil {
				result = errors.Join(result, err)
				continue
			}
		}
		result = errors.Join(result, roots.Restore(snapshot, s.checkpointDir(workspaceID, rollback.CheckpointID)))
	}
	return result
}

func pruneRollbackDirectory(root *os.Root, relative string, snapshot FileSnapshot) error {
	info, err := root.Lstat(relative)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	dir, err := root.Open(relative)
	if err != nil {
		return err
	}
	entries, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	expected := make(map[string]FileSnapshot, len(snapshot.Children))
	for _, child := range snapshot.Children {
		expected[filepath.Base(child.Path)] = child
	}
	for _, entry := range entries {
		childRelative := entry.Name()
		if relative != "." {
			childRelative = filepath.Join(relative, entry.Name())
		}
		child, ok := expected[entry.Name()]
		if !ok {
			if err := root.RemoveAll(childRelative); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		if child.IsDirectory {
			if err := pruneRollbackDirectory(root, childRelative, child); err != nil {
				return err
			}
		}
	}
	return nil
}

func Fingerprint(paths []string) string {
	normalized := uniquePaths(paths)
	sort.Strings(normalized)
	sum := sha256.Sum256([]byte(strings.Join(normalized, "\n")))
	return hex.EncodeToString(sum[:])[:12]
}

func (s *Store) snapshot(workspaceID, checkpointID string, roots restoreRoots, path string, depth int) (FileSnapshot, error) {
	resolved := filepath.Clean(path)
	root, relative, err := roots.target(resolved)
	if err != nil {
		return FileSnapshot{}, err
	}
	return s.snapshotRooted(workspaceID, checkpointID, root, relative, resolved, depth)
}

func (s *Store) snapshotDirectory(workspaceID, checkpointID string, root *os.Root, relative, path string, info os.FileInfo, depth int) (FileSnapshot, error) {
	if depth > s.maxDepth() {
		return FileSnapshot{}, fmt.Errorf("checkpoint directory depth exceeds %d at %s", s.maxDepth(), path)
	}
	dir, err := root.Open(relative)
	if err != nil {
		return FileSnapshot{}, err
	}
	defer dir.Close()
	openedInfo, err := dir.Stat()
	if err != nil {
		return FileSnapshot{}, err
	}
	if !openedInfo.IsDir() || !os.SameFile(info, openedInfo) {
		return FileSnapshot{}, fmt.Errorf("checkpoint source changed while snapshotting %s", path)
	}
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return FileSnapshot{}, err
	}
	children := make([]FileSnapshot, 0, len(entries))
	for _, entry := range entries {
		childPath := filepath.Join(path, entry.Name())
		childRelative := filepath.Join(relative, entry.Name())
		child, err := s.snapshotRooted(workspaceID, checkpointID, root, childRelative, childPath, depth+1)
		if err != nil {
			return FileSnapshot{}, err
		}
		children = append(children, child)
	}
	return FileSnapshot{Path: path, Existed: true, IsDirectory: true, Mode: uint32(openedInfo.Mode().Perm()), Children: children}, nil
}

func (s *Store) snapshotRooted(workspaceID, checkpointID string, root *os.Root, relative, path string, depth int) (FileSnapshot, error) {
	info, err := root.Lstat(relative)
	if errors.Is(err, os.ErrNotExist) {
		return FileSnapshot{Path: path, Existed: false}, nil
	}
	if err != nil {
		return FileSnapshot{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := root.Readlink(relative)
		if err != nil {
			return FileSnapshot{}, err
		}
		if err := validateRootedSymlinkSnapshot(root, relative, path, target); err != nil {
			return FileSnapshot{}, err
		}
		return FileSnapshot{Path: path, Existed: true, IsSymlink: true, LinkTarget: target}, nil
	}
	if info.IsDir() {
		return s.snapshotDirectory(workspaceID, checkpointID, root, relative, path, info, depth)
	}
	if !info.Mode().IsRegular() {
		return FileSnapshot{}, fmt.Errorf("checkpoint cannot safely snapshot unsupported file type: %s", path)
	}
	file, err := root.Open(relative)
	if err != nil {
		return FileSnapshot{}, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return FileSnapshot{}, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return FileSnapshot{}, fmt.Errorf("checkpoint source changed while snapshotting %s", path)
	}
	if openedInfo.Size() > s.maxFileBytes() {
		blob, digest, size, err := s.writeBlob(workspaceID, checkpointID, path, file)
		if err != nil {
			return FileSnapshot{}, fmt.Errorf("checkpoint large file %s: %w", path, err)
		}
		if size != openedInfo.Size() {
			return FileSnapshot{}, fmt.Errorf("checkpoint source changed while snapshotting %s", path)
		}
		return FileSnapshot{Path: path, Existed: true, Mode: uint32(openedInfo.Mode().Perm()), Blob: blob, BlobSHA256: digest, Size: size}, nil
	}
	data, err := io.ReadAll(io.LimitReader(file, s.maxFileBytes()+1))
	if err != nil {
		return FileSnapshot{}, err
	}
	if int64(len(data)) != openedInfo.Size() {
		return FileSnapshot{}, fmt.Errorf("checkpoint source changed while snapshotting %s", path)
	}
	if utf8.Valid(data) {
		return FileSnapshot{Path: path, Existed: true, Mode: uint32(openedInfo.Mode().Perm()), Encoding: "utf-8", Content: string(data), Size: int64(len(data))}, nil
	}
	return FileSnapshot{Path: path, Existed: true, Mode: uint32(openedInfo.Mode().Perm()), Encoding: "base64", Content: base64.StdEncoding.EncodeToString(data), Size: int64(len(data))}, nil
}

func (s *Store) writeBlob(workspaceID, checkpointID, source string, input io.Reader) (string, string, int64, error) {
	sum := sha256.Sum256([]byte(filepath.Clean(source)))
	relative := filepath.ToSlash(filepath.Join("blobs", hex.EncodeToString(sum[:])))
	destination, err := s.blobPath(workspaceID, checkpointID, relative)
	if err != nil {
		return "", "", 0, err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return "", "", 0, err
	}
	temp, err := os.CreateTemp(filepath.Dir(destination), ".blob-*")
	if err != nil {
		return "", "", 0, err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0600); err != nil {
		_ = temp.Close()
		return "", "", 0, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(temp, hash), input)
	syncErr := temp.Sync()
	closeErr := temp.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return "", "", 0, err
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return "", "", 0, err
	}
	return relative, hex.EncodeToString(hash.Sum(nil)), size, nil
}

func (s *Store) blobPath(workspaceID, checkpointID, relative string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid checkpoint blob path: %s", relative)
	}
	root := s.checkpointDir(workspaceID, checkpointID)
	path := filepath.Join(root, clean)
	if !within(root, path) {
		return "", fmt.Errorf("checkpoint blob escapes checkpoint: %s", relative)
	}
	return path, nil
}

func (s *Store) collectRestorePlanLocked(workspaceID, workspaceRoot string, allowedRoots []string, id string) (Summary, map[string]restoreSnapshot, error) {
	index, err := s.readIndex(workspaceID)
	if err != nil {
		return Summary{}, nil, err
	}
	targetIndex := summaryIndex(index.Checkpoints, id)
	if targetIndex < 0 {
		return Summary{}, nil, fmt.Errorf("checkpoint not found: %s", id)
	}
	target := index.Checkpoints[targetIndex]
	files := map[string]restoreSnapshot{}
	for _, summary := range index.Checkpoints[targetIndex:] {
		manifest, err := s.readManifest(workspaceID, summary.ID)
		if err != nil {
			return Summary{}, nil, err
		}
		if manifest == nil {
			continue
		}
		if manifest.WorkspaceID != workspaceID || filepath.Clean(manifest.WorkspaceRoot) != filepath.Clean(workspaceRoot) {
			return Summary{}, nil, errors.New("checkpoint workspace binding mismatch")
		}
		manifestRoots := effectiveRoots(manifest.WorkspaceRoot, manifest.AllowedRoots)
		for _, snapshot := range manifest.Files {
			if !withinAny(manifestRoots, snapshot.Path) || !withinAny(allowedRoots, snapshot.Path) {
				return Summary{}, nil, fmt.Errorf("checkpoint path escapes workspace: %s", snapshot.Path)
			}
			if _, exists := files[snapshot.Path]; !exists {
				files[snapshot.Path] = restoreSnapshot{CheckpointID: summary.ID, Snapshot: snapshot}
			}
		}
	}
	return target, files, nil
}

func (s *Store) readIndex(workspaceID string) (Index, error) {
	path := s.indexPath(workspaceID)
	data, err := readBoundedRegularFile(path, maxIndexBytes)
	if errors.Is(err, os.ErrNotExist) {
		return Index{Version: indexVersion, Checkpoints: []Summary{}}, nil
	}
	if err != nil {
		return Index{}, err
	}
	var index Index
	if err := decodeStrictJSON(data, &index); err != nil {
		return Index{}, err
	}
	if index.Version != indexVersion {
		return Index{}, fmt.Errorf("unsupported checkpoint index version: %d", index.Version)
	}
	if len(index.Checkpoints) > maxIndexEntries {
		return Index{}, fmt.Errorf("checkpoint index exceeds %d entries", maxIndexEntries)
	}
	if index.Checkpoints == nil {
		index.Checkpoints = []Summary{}
	}
	seen := make(map[string]struct{}, len(index.Checkpoints))
	for _, summary := range index.Checkpoints {
		if strings.TrimSpace(summary.ID) == "" {
			return Index{}, errors.New("checkpoint index contains empty id")
		}
		if _, exists := seen[summary.ID]; exists {
			return Index{}, fmt.Errorf("checkpoint index contains duplicate id: %s", summary.ID)
		}
		seen[summary.ID] = struct{}{}
		if _, err := time.Parse(time.RFC3339Nano, summary.CreatedAt); err != nil {
			return Index{}, fmt.Errorf("checkpoint index has invalid created_at for %s", summary.ID)
		}
		if summary.FileCount != len(summary.Files) {
			return Index{}, fmt.Errorf("checkpoint index file count mismatch for %s", summary.ID)
		}
	}
	return index, nil
}

func (s *Store) writeIndex(workspaceID string, index Index) error {
	if err := s.Ensure(workspaceID); err != nil {
		return err
	}
	index.Version = indexVersion
	return writeStructuredAtomic(s.indexPath(workspaceID), index, 0600)
}

func (s *Store) readManifest(workspaceID, id string) (*Manifest, error) {
	path := s.manifestPath(workspaceID, id)
	data, err := readBoundedRegularFile(path, maxArchiveManifestBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err := decodeStrictJSON(data, &manifest); err != nil {
		return nil, err
	}
	if manifest.Version != indexVersion {
		return nil, fmt.Errorf("unsupported checkpoint manifest version: %d", manifest.Version)
	}
	if manifest.ID != id || manifest.WorkspaceID != workspaceID {
		return nil, fmt.Errorf("checkpoint manifest identity mismatch: %s", id)
	}
	if strings.TrimSpace(manifest.WorkspaceRoot) == "" || !filepath.IsAbs(manifest.WorkspaceRoot) {
		return nil, fmt.Errorf("checkpoint manifest has invalid workspace root: %s", id)
	}
	if _, err := time.Parse(time.RFC3339Nano, manifest.CreatedAt); err != nil {
		return nil, fmt.Errorf("checkpoint manifest has invalid created_at: %s", id)
	}
	return &manifest, nil
}

func (s *Store) pruneLocked(workspaceID string, index *Index) ([]Summary, error) {
	return s.archiveRetentionLocked(workspaceID, index)
}

func (s *Store) checkpointDir(workspaceID, id string) string {
	return filepath.Join(s.Path(workspaceID), "data", id)
}

func (s *Store) indexPath(workspaceID string) string {
	return filepath.Join(s.Path(workspaceID), "index.json")
}

func (s *Store) manifestPath(workspaceID, id string) string {
	return filepath.Join(s.checkpointDir(workspaceID, id), "manifest.json")
}

func (s *Store) maxCount() int {
	if s.MaxCount > 0 {
		return s.MaxCount
	}
	return defaultMaxCount
}

func (s *Store) retention() time.Duration {
	if s.Retention > 0 {
		return s.Retention
	}
	return defaultRetention
}

func (s *Store) maxFileBytes() int64 {
	if s.MaxFileBytes > 0 {
		return s.MaxFileBytes
	}
	return defaultMaxFile
}

func (s *Store) maxDepth() int {
	if s.MaxDirectoryDepth > 0 {
		return s.MaxDirectoryDepth
	}
	return maxDirectoryDepth
}

func checkpointID() (string, error) {
	return idgen.New("cp", 6)
}

func buildSummary(manifest Manifest) Summary {
	files := make([]string, len(manifest.Files))
	for i, snapshot := range manifest.Files {
		files[i] = snapshot.Path
	}
	return Summary{ID: manifest.ID, CreatedAt: manifest.CreatedAt, Tool: manifest.Tool, Summary: manifest.Summary, Files: files, FileCount: len(files)}
}

func summaryIndex(values []Summary, id string) int {
	for i, value := range values {
		if value.ID == id {
			return i
		}
	}
	return -1
}

func uniquePaths(paths []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		clean := filepath.Clean(path)
		if !seen[clean] {
			seen[clean] = true
			result = append(result, clean)
		}
	}
	return result
}

func effectiveRoots(workspaceRoot string, roots []string) []string {
	values := append([]string{workspaceRoot}, roots...)
	return uniquePaths(values)
}

func withinAny(roots []string, candidate string) bool {
	for _, root := range roots {
		if within(root, candidate) {
			return true
		}
	}
	return false
}

func within(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative))
}

func writeStructuredAtomic(path string, value any, mode os.FileMode) error {
	return state.WriteJSONAtomic(path, value, mode)
}

func decodeStrictJSON(data []byte, value any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("checkpoint JSON contains trailing data")
	}
	return nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
