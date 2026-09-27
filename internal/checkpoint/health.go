package checkpoint

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

const (
	DiagnosticComponentID = "checkpoint_history"
	defaultRecoveryMaxOps = 64
	maxRecoveryMaxOps     = 1000

	healthSeverityWarning = "warning"
	healthSeverityError   = "error"
)

type HealthStatus string

const (
	HealthHealthy  HealthStatus = "healthy"
	HealthDegraded HealthStatus = "degraded"
	HealthCorrupt  HealthStatus = "corrupt"
)

type StorageIssue struct {
	Kind        string `json:"kind"`
	Severity    string `json:"severity"`
	Scope       string `json:"scope,omitempty"`
	Path        string `json:"path,omitempty"`
	ID          string `json:"id,omitempty"`
	Recoverable bool   `json:"recoverable,omitempty"`
	Error       string `json:"error,omitempty"`
}

type StorageHealth struct {
	ComponentID           string         `json:"component_id"`
	WorkspaceID           string         `json:"workspace_id"`
	Status                HealthStatus   `json:"status"`
	Healthy               bool           `json:"healthy"`
	ActiveIndexPresent    bool           `json:"active_index_present"`
	ArchiveIndexPresent   bool           `json:"archive_index_present"`
	ActiveIndexed         int            `json:"active_indexed"`
	ArchivedIndexed       int            `json:"archived_indexed"`
	ActivePayloads        int            `json:"active_payloads"`
	ArchivedPayloads      int            `json:"archived_payloads"`
	OrphanActivePayloads  int            `json:"orphan_active_payloads"`
	OrphanArchivePayloads int            `json:"orphan_archive_payloads"`
	TemporaryPayloads     int            `json:"temporary_payloads"`
	Issues                []StorageIssue `json:"issues,omitempty"`
}

type DiagnosticProvider interface {
	Diagnose(workspaceID string) StorageHealth
}

type RecoveryAction struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
	Path string `json:"path,omitempty"`
}

type RecoveryResult struct {
	WorkspaceID   string           `json:"workspace_id"`
	Scanned       int              `json:"scanned"`
	Recovered     int              `json:"recovered"`
	RemovedStale  int              `json:"removed_stale"`
	Pending       int              `json:"pending"`
	RebuiltActive bool             `json:"rebuilt_active"`
	Actions       []RecoveryAction `json:"actions,omitempty"`
	Health        StorageHealth    `json:"health"`
}

type payloadInspection struct {
	Path    string
	Summary Summary
	Valid   bool
	Kind    string
	Err     error
}

var _ DiagnosticProvider = (*Store)(nil)

func (s *Store) Diagnose(workspaceID string) StorageHealth {
	if s == nil {
		return StorageHealth{
			ComponentID: DiagnosticComponentID,
			WorkspaceID: strings.TrimSpace(workspaceID),
			Status:      HealthCorrupt,
			Issues: []StorageIssue{{
				Kind: "store_unavailable", Severity: healthSeverityError, Scope: "checkpoint", Error: "checkpoint store is unavailable",
			}},
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.diagnoseLocked(strings.TrimSpace(workspaceID))
}

func (s *Store) diagnoseLocked(workspaceID string) StorageHealth {
	health := StorageHealth{
		ComponentID: DiagnosticComponentID,
		WorkspaceID: workspaceID,
		Status:      HealthHealthy,
		Healthy:     true,
		Issues:      []StorageIssue{},
	}

	activePresent, activePresentErr := regularStatePathPresent(s.indexPath(workspaceID))
	if activePresentErr != nil {
		health.addIssue(StorageIssue{Kind: "active_index_unreadable", Severity: healthSeverityError, Scope: "active", Path: s.indexPath(workspaceID), Error: activePresentErr.Error()})
	}
	health.ActiveIndexPresent = activePresent
	archivePresent, archivePresentErr := regularStatePathPresent(s.archiveIndexPath(workspaceID))
	if archivePresentErr != nil {
		health.addIssue(StorageIssue{Kind: "archive_index_unreadable", Severity: healthSeverityError, Scope: "archive", Path: s.archiveIndexPath(workspaceID), Error: archivePresentErr.Error()})
	}
	health.ArchiveIndexPresent = archivePresent

	activeIndex, activeErr := s.readIndex(workspaceID)
	if activeErr != nil {
		health.addIssue(StorageIssue{Kind: "active_index_corrupt", Severity: healthSeverityError, Scope: "active", Path: s.indexPath(workspaceID), Error: activeErr.Error()})
	}
	archiveIndex, archiveErr := s.readArchiveIndex(workspaceID)
	if archiveErr != nil {
		health.addIssue(StorageIssue{Kind: "archive_index_corrupt", Severity: healthSeverityError, Scope: "archive", Path: s.archiveIndexPath(workspaceID), Error: archiveErr.Error()})
	}

	activePayloads := s.scanPayloadRootLocked(workspaceID, s.activeDataRoot(workspaceID), "active", &health)
	archivePayloads := s.scanPayloadRootLocked(workspaceID, s.archiveDataRoot(workspaceID), "archive", &health)

	activeKnown := map[string]Summary{}
	if activeErr == nil {
		health.ActiveIndexed = len(activeIndex.Checkpoints)
		for _, summary := range activeIndex.Checkpoints {
			activeKnown[summary.ID] = summary
		}
	}
	archiveKnown := map[string]ArchivedSummary{}
	if archiveErr == nil {
		health.ArchivedIndexed = len(archiveIndex.Checkpoints)
		for _, archived := range archiveIndex.Checkpoints {
			archiveKnown[archived.Checkpoint.ID] = archived
		}
	}

	if activeErr == nil && archiveErr == nil {
		for id := range activeKnown {
			if _, exists := archiveKnown[id]; exists {
				health.addIssue(StorageIssue{
					Kind: "index_ownership_conflict", Severity: healthSeverityError, Scope: "checkpoint", ID: id,
					Error: "checkpoint id is referenced by both active and archive indexes",
				})
			}
		}
	}

	if activeErr == nil {
		for id, summary := range activeKnown {
			payload, ok := activePayloads[id]
			if !ok {
				health.addIssue(StorageIssue{Kind: "active_payload_missing", Severity: healthSeverityError, Scope: "active", ID: id, Path: s.checkpointDir(workspaceID, id), Error: "active index references a missing checkpoint payload"})
				continue
			}
			s.compareIndexedPayload(&health, "active", summary, payload)
		}
	}
	if archiveErr == nil {
		for id, archived := range archiveKnown {
			payload, ok := archivePayloads[id]
			if !ok {
				health.addIssue(StorageIssue{Kind: "archive_payload_missing", Severity: healthSeverityError, Scope: "archive", ID: id, Path: s.archiveCheckpointDir(workspaceID, id), Recoverable: true, Error: "archive index references a missing checkpoint payload"})
				continue
			}
			s.compareIndexedPayload(&health, "archive", archived.Checkpoint, payload)
		}
	}

	for id, payload := range activePayloads {
		if _, indexed := activeKnown[id]; indexed {
			continue
		}
		health.OrphanActivePayloads++
		if archived, archivedOwner := archiveKnown[id]; archivedOwner {
			if payload.Valid && reflect.DeepEqual(payload.Summary, archived.Checkpoint) {
				health.addIssue(StorageIssue{
					Kind: "orphan_active_duplicate", Severity: healthSeverityWarning, Scope: "active", ID: id, Path: payload.Path,
					Recoverable: true, Error: "active payload is not indexed and matches an archived checkpoint",
				})
				continue
			}
			health.addIssue(StorageIssue{
				Kind: "orphan_active_conflict", Severity: healthSeverityError, Scope: "active", ID: id, Path: payload.Path,
				Error: payloadError(payload, "active orphan conflicts with archived ownership"),
			})
			continue
		}
		if payload.Valid {
			health.addIssue(StorageIssue{
				Kind: "orphan_active_payload", Severity: healthSeverityWarning, Scope: "active", ID: id, Path: payload.Path,
				Recoverable: !activePresent, Error: "valid checkpoint payload is not referenced by the active index",
			})
		} else {
			health.addIssue(StorageIssue{
				Kind: "orphan_active_corrupt", Severity: healthSeverityError, Scope: "active", ID: id, Path: payload.Path,
				Error: payloadError(payload, "orphan active payload is corrupt"),
			})
		}
	}
	for id, payload := range archivePayloads {
		if _, indexed := archiveKnown[id]; indexed {
			continue
		}
		health.OrphanArchivePayloads++
		if payload.Valid {
			health.addIssue(StorageIssue{
				Kind: "orphan_archive_payload", Severity: healthSeverityError, Scope: "archive", ID: id, Path: payload.Path,
				Error: "valid archive payload has no archive metadata; ownership reason and archive timestamp are ambiguous",
			})
		} else {
			health.addIssue(StorageIssue{
				Kind: "orphan_archive_corrupt", Severity: healthSeverityError, Scope: "archive", ID: id, Path: payload.Path,
				Error: payloadError(payload, "orphan archive payload is corrupt"),
			})
		}
	}

	if !activePresent && len(activePayloads) > 0 && activeErr == nil {
		health.addIssue(StorageIssue{
			Kind: "active_index_missing", Severity: healthSeverityWarning, Scope: "active", Path: s.indexPath(workspaceID),
			Recoverable: true, Error: "active payloads exist without an active index",
		})
	}
	if !archivePresent && len(archivePayloads) > 0 && archiveErr == nil {
		health.addIssue(StorageIssue{
			Kind: "archive_index_missing", Severity: healthSeverityError, Scope: "archive", Path: s.archiveIndexPath(workspaceID),
			Error: "archive payloads exist without archive metadata and cannot be reconstructed safely",
		})
	}

	health.Healthy = health.Status == HealthHealthy
	return health
}

func (s *Store) compareIndexedPayload(health *StorageHealth, scope string, expected Summary, payload payloadInspection) {
	if !payload.Valid {
		kind := scope + "_payload_corrupt"
		if payload.Kind != "" {
			kind = scope + "_" + payload.Kind
		}
		health.addIssue(StorageIssue{
			Kind: kind, Severity: healthSeverityError, Scope: scope, ID: expected.ID, Path: payload.Path,
			Error: payloadError(payload, "checkpoint payload is corrupt"),
		})
		return
	}
	if !reflect.DeepEqual(payload.Summary, expected) {
		health.addIssue(StorageIssue{
			Kind: scope + "_manifest_mismatch", Severity: healthSeverityError, Scope: scope, ID: expected.ID, Path: payload.Path,
			Error: "checkpoint manifest summary does not match the canonical index",
		})
	}
}

func (s *Store) scanPayloadRootLocked(workspaceID, root, scope string, health *StorageHealth) map[string]payloadInspection {
	result := map[string]payloadInspection{}
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return result
	}
	if err != nil {
		health.addIssue(StorageIssue{Kind: scope + "_data_unreadable", Severity: healthSeverityError, Scope: scope, Path: root, Error: err.Error()})
		return result
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		health.addIssue(StorageIssue{Kind: scope + "_data_invalid", Severity: healthSeverityError, Scope: scope, Path: root, Error: "checkpoint payload root is not a real directory"})
		return result
	}
	entries, err := boundedDirEntries(root, maxIndexEntries)
	if err != nil {
		health.addIssue(StorageIssue{Kind: scope + "_data_scan_error", Severity: healthSeverityError, Scope: scope, Path: root, Error: err.Error()})
		return result
	}
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(root, name)
		if scope == "archive" && isArchiveTemporaryName(name) {
			health.TemporaryPayloads++
			health.addIssue(StorageIssue{
				Kind: "archive_temporary_payload", Severity: healthSeverityWarning, Scope: scope, Path: path,
				Recoverable: true, Error: "archive staging payload remains from an interrupted transition",
			})
			continue
		}
		entryInfo, err := os.Lstat(path)
		if err != nil {
			health.addIssue(StorageIssue{Kind: scope + "_payload_unreadable", Severity: healthSeverityError, Scope: scope, ID: name, Path: path, Error: err.Error()})
			continue
		}
		if !entryInfo.IsDir() || entryInfo.Mode()&os.ModeSymlink != 0 {
			health.addIssue(StorageIssue{Kind: scope + "_payload_invalid", Severity: healthSeverityError, Scope: scope, ID: name, Path: path, Error: "checkpoint payload entry is not a real directory"})
			continue
		}
		if scope == "active" {
			health.ActivePayloads++
		} else {
			health.ArchivedPayloads++
		}
		result[name] = inspectPayloadDirectory(path, workspaceID, name)
	}
	return result
}

func inspectPayloadDirectory(root, workspaceID, expectedID string) payloadInspection {
	result := payloadInspection{Path: root}
	data, err := readBoundedRegularFile(filepath.Join(root, "manifest.json"), maxArchiveManifestBytes)
	if err != nil {
		result.Kind = "manifest_corrupt"
		result.Err = err
		return result
	}
	var manifest Manifest
	if err := decodeStrictJSON(data, &manifest); err != nil {
		result.Kind = "manifest_corrupt"
		result.Err = err
		return result
	}
	if manifest.Version != indexVersion || manifest.ID != expectedID || manifest.WorkspaceID != workspaceID {
		result.Kind = "manifest_corrupt"
		result.Err = fmt.Errorf("checkpoint manifest identity mismatch: %s", expectedID)
		return result
	}
	if strings.TrimSpace(manifest.WorkspaceRoot) == "" || !filepath.IsAbs(manifest.WorkspaceRoot) {
		result.Kind = "manifest_corrupt"
		result.Err = fmt.Errorf("checkpoint manifest has invalid workspace root: %s", expectedID)
		return result
	}
	if _, err := time.Parse(time.RFC3339Nano, manifest.CreatedAt); err != nil {
		result.Kind = "manifest_corrupt"
		result.Err = fmt.Errorf("checkpoint manifest has invalid created_at: %s", expectedID)
		return result
	}
	result.Summary = buildSummary(manifest)
	if err := validateCheckpointPayloadDir(root, workspaceID, result.Summary); err != nil {
		result.Kind = classifyPayloadValidationError(err)
		result.Err = err
		return result
	}
	result.Valid = true
	return result
}

func classifyPayloadValidationError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "hash mismatch"), strings.Contains(message, "checksum mismatch"):
		return "blob_checksum_failure"
	case strings.Contains(message, "blob size mismatch"), strings.Contains(message, "blob exceeds"), strings.Contains(message, "blob is not"):
		return "blob_integrity_failure"
	case strings.Contains(message, "manifest"), strings.Contains(message, "summary mismatch"), strings.Contains(message, "identity mismatch"):
		return "manifest_corrupt"
	default:
		return "payload_corrupt"
	}
}

func payloadError(payload payloadInspection, fallback string) string {
	if payload.Err != nil {
		return payload.Err.Error()
	}
	return fallback
}

func (h *StorageHealth) addIssue(issue StorageIssue) {
	if h == nil {
		return
	}
	h.Issues = append(h.Issues, issue)
	switch issue.Severity {
	case healthSeverityError:
		h.Status = HealthCorrupt
	case healthSeverityWarning:
		if h.Status == HealthHealthy {
			h.Status = HealthDegraded
		}
	}
	h.Healthy = h.Status == HealthHealthy
}

func (s *Store) Recover(workspaceID string, maxOps int) (RecoveryResult, error) {
	if s == nil {
		return RecoveryResult{}, errors.New("checkpoint store is unavailable")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if maxOps <= 0 {
		maxOps = defaultRecoveryMaxOps
	}
	if maxOps > maxRecoveryMaxOps {
		maxOps = maxRecoveryMaxOps
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	result := RecoveryResult{WorkspaceID: workspaceID, Actions: []RecoveryAction{}}
	archivePresent, err := regularStatePathPresent(s.archiveIndexPath(workspaceID))
	if err != nil {
		return result, err
	}
	archive, err := s.readArchiveIndex(workspaceID)
	if err != nil {
		return result, fmt.Errorf("checkpoint recovery refused because archive index is corrupt: %w", err)
	}
	archiveKnown := make(map[string]ArchivedSummary, len(archive.Checkpoints))
	for _, item := range archive.Checkpoints {
		archiveKnown[item.Checkpoint.ID] = item
	}

	archiveEntries, err := boundedDirEntriesIfExists(s.archiveDataRoot(workspaceID), maxIndexEntries)
	if err != nil {
		return result, err
	}
	if !archivePresent {
		for _, entry := range archiveEntries {
			if isArchiveTemporaryName(entry.Name()) {
				continue
			}
			return result, fmt.Errorf("checkpoint recovery refused: archive payload %s has no canonical archive metadata", entry.Name())
		}
	}

	activePresent, err := regularStatePathPresent(s.indexPath(workspaceID))
	if err != nil {
		return result, err
	}
	active, activeErr := s.readIndex(workspaceID)
	if activeErr != nil {
		return result, fmt.Errorf("checkpoint recovery refused because active index is corrupt: %w", activeErr)
	}
	activeKnown := make(map[string]Summary, len(active.Checkpoints))
	for _, summary := range active.Checkpoints {
		activeKnown[summary.ID] = summary
		if _, conflict := archiveKnown[summary.ID]; conflict {
			return result, fmt.Errorf("checkpoint recovery refused: checkpoint %s is owned by both active and archive indexes", summary.ID)
		}
	}

	activeEntries, err := boundedDirEntriesIfExists(s.activeDataRoot(workspaceID), maxIndexEntries)
	if err != nil {
		return result, err
	}
	result.Scanned += len(activeEntries) + len(archiveEntries)

	if !activePresent && len(activeEntries) > 0 {
		if len(activeEntries) > maxOps {
			result.Pending = len(activeEntries) - maxOps
			result.Health = s.diagnoseLocked(workspaceID)
			return result, fmt.Errorf("checkpoint recovery requires %d active payload scans but max_ops is %d", len(activeEntries), maxOps)
		}
		rebuilt := make([]Summary, 0, len(activeEntries))
		for _, entry := range activeEntries {
			name := entry.Name()
			path := filepath.Join(s.activeDataRoot(workspaceID), name)
			if _, archived := archiveKnown[name]; archived {
				return result, fmt.Errorf("checkpoint recovery refused: active payload %s conflicts with archived ownership", name)
			}
			payload := inspectPayloadDirectory(path, workspaceID, name)
			if !payload.Valid {
				return result, fmt.Errorf("checkpoint recovery refused: active payload %s is not provably valid: %w", name, payload.Err)
			}
			rebuilt = append(rebuilt, payload.Summary)
		}
		sort.Slice(rebuilt, func(i, j int) bool {
			if rebuilt[i].CreatedAt == rebuilt[j].CreatedAt {
				return rebuilt[i].ID < rebuilt[j].ID
			}
			return rebuilt[i].CreatedAt < rebuilt[j].CreatedAt
		})
		if err := s.writeIndex(workspaceID, Index{Version: indexVersion, Checkpoints: rebuilt}); err != nil {
			return result, err
		}
		result.RebuiltActive = true
		result.Recovered += len(rebuilt)
		result.Actions = append(result.Actions, RecoveryAction{Kind: "rebuild_active_index", Path: s.indexPath(workspaceID)})
		activeKnown = make(map[string]Summary, len(rebuilt))
		for _, summary := range rebuilt {
			activeKnown[summary.ID] = summary
		}
	}

	ops := result.Recovered
	for _, entry := range archiveEntries {
		name := entry.Name()
		id, ok := archiveTemporaryID(name)
		if !ok {
			continue
		}
		if ops >= maxOps {
			result.Pending++
			continue
		}
		archived, owned := archiveKnown[id]
		if !owned {
			continue
		}
		tempPath := filepath.Join(s.archiveDataRoot(workspaceID), name)
		tempPayload := inspectPayloadDirectory(tempPath, workspaceID, id)
		if !tempPayload.Valid || !reflect.DeepEqual(tempPayload.Summary, archived.Checkpoint) {
			continue
		}
		final := s.archiveCheckpointDir(workspaceID, id)
		finalPayload := inspectPayloadDirectory(final, workspaceID, id)
		if finalPayload.Valid && reflect.DeepEqual(finalPayload.Summary, archived.Checkpoint) {
			if err := os.RemoveAll(tempPath); err != nil {
				return result, err
			}
			result.RemovedStale++
			ops++
			result.Actions = append(result.Actions, RecoveryAction{Kind: "remove_stale_archive_staging", ID: id, Path: tempPath})
			continue
		}
		if _, err := os.Lstat(final); errors.Is(err, os.ErrNotExist) {
			if err := os.Rename(tempPath, final); err != nil {
				return result, err
			}
			result.Recovered++
			ops++
			result.Actions = append(result.Actions, RecoveryAction{Kind: "publish_archive_staging", ID: id, Path: final})
		}
	}

	for _, item := range archive.Checkpoints {
		final := s.archiveCheckpointDir(workspaceID, item.Checkpoint.ID)
		if _, err := os.Lstat(final); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return result, err
		}
		if ops >= maxOps {
			result.Pending++
			continue
		}
		if _, activeOwner := activeKnown[item.Checkpoint.ID]; activeOwner {
			return result, fmt.Errorf("checkpoint recovery refused: missing archive payload %s is also active-indexed", item.Checkpoint.ID)
		}
		source := s.checkpointDir(workspaceID, item.Checkpoint.ID)
		payload := inspectPayloadDirectory(source, workspaceID, item.Checkpoint.ID)
		if !payload.Valid || !reflect.DeepEqual(payload.Summary, item.Checkpoint) {
			if payload.Err != nil {
				return result, fmt.Errorf("checkpoint recovery refused: missing archive payload %s has no valid active copy: %w", item.Checkpoint.ID, payload.Err)
			}
			return result, fmt.Errorf("checkpoint recovery refused: missing archive payload %s has no matching active copy", item.Checkpoint.ID)
		}
		if err := s.ensureArchivedPayload(workspaceID, item.Checkpoint); err != nil {
			return result, err
		}
		if err := os.RemoveAll(source); err != nil {
			return result, err
		}
		result.Recovered++
		result.RemovedStale++
		ops++
		result.Actions = append(result.Actions, RecoveryAction{Kind: "recover_archive_payload", ID: item.Checkpoint.ID, Path: final})
	}

	result.Health = s.diagnoseLocked(workspaceID)
	return result, nil
}

func regularStatePathPresent(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return true, fmt.Errorf("checkpoint state path is not a regular non-symlink file: %s", path)
	}
	return true, nil
}

func boundedDirEntriesIfExists(path string, max int) ([]os.DirEntry, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("checkpoint data path is not a real directory: %s", path)
	}
	return boundedDirEntries(path, max)
}

func boundedDirEntries(path string, max int) ([]os.DirEntry, error) {
	dir, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries := make([]os.DirEntry, 0)
	for len(entries) <= max {
		batch, readErr := dir.ReadDir(256)
		entries = append(entries, batch...)
		if len(entries) > max {
			return nil, fmt.Errorf("checkpoint data directory exceeds %d entries: %s", max, path)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

func (s *Store) activeDataRoot(workspaceID string) string {
	return filepath.Join(s.Path(workspaceID), "data")
}

func isArchiveTemporaryName(name string) bool {
	_, ok := archiveTemporaryID(name)
	return ok
}

func archiveTemporaryID(name string) (string, bool) {
	if !strings.HasPrefix(name, ".") {
		return "", false
	}
	value := strings.TrimPrefix(name, ".")
	index := strings.Index(value, ".tmp-")
	if index <= 0 {
		return "", false
	}
	id := value[:index]
	return id, strings.TrimSpace(id) != ""
}
