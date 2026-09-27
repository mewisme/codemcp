package checkpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

const (
	archiveIndexVersion     = 1
	defaultArchiveListLimit = 50
	maxArchiveListLimit     = 1000
	maxArchiveIndexBytes    = 16 << 20
	maxArchiveManifestBytes = 64 << 20
	maxArchiveEntries       = 100_000
	maxArchiveFilesPerEntry = 100_000
	maxArchiveBytesPerEntry = int64(2 << 30)
)

type ArchiveReason string

const (
	ArchiveReasonRetentionAge   ArchiveReason = "retention_age"
	ArchiveReasonRetentionCount ArchiveReason = "retention_count"
	ArchiveReasonRestore        ArchiveReason = "restore"
	ArchiveReasonClear          ArchiveReason = "clear"
)

type ArchivedSummary struct {
	Checkpoint Summary       `json:"checkpoint"`
	ArchivedAt string        `json:"archived_at"`
	Reason     ArchiveReason `json:"reason"`
}

type ArchiveIndex struct {
	Version     int               `json:"version"`
	Checkpoints []ArchivedSummary `json:"checkpoints"`
}

type archiveCandidate struct {
	Summary Summary
	Reason  ArchiveReason
}

type archiveMutation struct {
	Previous        ArchiveIndex
	IndexExisted    bool
	CreatedPayloads []string
}

func (s *Store) ListArchived(workspaceID string, limit int) ([]ArchivedSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.readArchiveIndex(workspaceID)
	if err != nil {
		return nil, err
	}
	limit = boundedArchiveLimit(limit)
	result := make([]ArchivedSummary, 0, minInt(limit, len(index.Checkpoints)))
	for i := len(index.Checkpoints) - 1; i >= 0 && len(result) < limit; i-- {
		result = append(result, index.Checkpoints[i])
	}
	return result, nil
}

func (s *Store) GetArchived(workspaceID, id string) (*ArchivedSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.readArchiveIndex(workspaceID)
	if err != nil {
		return nil, err
	}
	for _, item := range index.Checkpoints {
		if item.Checkpoint.ID == id {
			value := item
			return &value, nil
		}
	}
	return nil, nil
}

func (s *Store) archiveRetentionLocked(workspaceID string, index *Index) ([]Summary, error) {
	candidates, kept := s.retentionCandidates(index.Checkpoints)
	if len(candidates) == 0 {
		return nil, nil
	}
	if _, err := s.appendArchiveCandidatesLocked(workspaceID, candidates); err != nil {
		return nil, err
	}
	index.Checkpoints = kept
	removed := make([]Summary, 0, len(candidates))
	for _, candidate := range candidates {
		removed = append(removed, candidate.Summary)
	}
	return removed, nil
}

func (s *Store) archiveAndReplaceActiveLocked(workspaceID string, current Index, kept []Summary, candidates []archiveCandidate) (int, error) {
	if len(candidates) == 0 {
		return 0, nil
	}
	mutation, err := s.appendArchiveCandidatesLocked(workspaceID, candidates)
	if err != nil {
		return 0, err
	}
	next := current
	next.Checkpoints = append([]Summary(nil), kept...)
	if err := s.writeIndex(workspaceID, next); err != nil {
		return 0, errors.Join(err, s.rollbackArchiveMutationLocked(workspaceID, mutation))
	}
	for _, candidate := range candidates {
		_ = os.RemoveAll(s.checkpointDir(workspaceID, candidate.Summary.ID))
	}
	return len(candidates), nil
}

func (s *Store) appendArchiveCandidatesLocked(workspaceID string, candidates []archiveCandidate) (archiveMutation, error) {
	archive, err := s.readArchiveIndex(workspaceID)
	if err != nil {
		return archiveMutation{}, err
	}
	_, statErr := os.Stat(s.archiveIndexPath(workspaceID))
	mutation := archiveMutation{
		Previous: ArchiveIndex{
			Version:     archive.Version,
			Checkpoints: append([]ArchivedSummary(nil), archive.Checkpoints...),
		},
		IndexExisted: statErr == nil,
	}
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return archiveMutation{}, statErr
	}
	known := make(map[string]int, len(archive.Checkpoints))
	for i, item := range archive.Checkpoints {
		id := strings.TrimSpace(item.Checkpoint.ID)
		if id == "" {
			return archiveMutation{}, errors.New("checkpoint archive contains empty id")
		}
		if _, exists := known[id]; exists {
			return archiveMutation{}, fmt.Errorf("checkpoint archive contains duplicate id: %s", id)
		}
		known[id] = i
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, candidate := range candidates {
		if existingIndex, ok := known[candidate.Summary.ID]; ok {
			existing := archive.Checkpoints[existingIndex]
			if !reflect.DeepEqual(existing.Checkpoint, candidate.Summary) {
				err := fmt.Errorf("checkpoint archive metadata mismatch: %s", candidate.Summary.ID)
				return archiveMutation{}, errors.Join(err, s.rollbackArchiveMutationLocked(workspaceID, mutation))
			}
			if err := s.validateArchivedPayload(workspaceID, candidate.Summary); err != nil {
				return archiveMutation{}, errors.Join(err, s.rollbackArchiveMutationLocked(workspaceID, mutation))
			}
			continue
		}
		_, payloadErr := os.Lstat(s.archiveCheckpointDir(workspaceID, candidate.Summary.ID))
		payloadExisted := payloadErr == nil
		if payloadErr != nil && !errors.Is(payloadErr, os.ErrNotExist) {
			return archiveMutation{}, errors.Join(payloadErr, s.rollbackArchiveMutationLocked(workspaceID, mutation))
		}
		if err := s.ensureArchivedPayload(workspaceID, candidate.Summary); err != nil {
			if !payloadExisted {
				if _, statErr := os.Lstat(s.archiveCheckpointDir(workspaceID, candidate.Summary.ID)); statErr == nil {
					mutation.CreatedPayloads = append(mutation.CreatedPayloads, candidate.Summary.ID)
				}
			}
			return archiveMutation{}, errors.Join(err, s.rollbackArchiveMutationLocked(workspaceID, mutation))
		}
		if !payloadExisted {
			mutation.CreatedPayloads = append(mutation.CreatedPayloads, candidate.Summary.ID)
		}
		archive.Checkpoints = append(archive.Checkpoints, ArchivedSummary{
			Checkpoint: candidate.Summary,
			ArchivedAt: now,
			Reason:     candidate.Reason,
		})
		known[candidate.Summary.ID] = len(archive.Checkpoints) - 1
	}
	if len(archive.Checkpoints) > maxArchiveEntries {
		err := fmt.Errorf("checkpoint archive exceeds %d entries", maxArchiveEntries)
		return archiveMutation{}, errors.Join(err, s.rollbackArchiveMutationLocked(workspaceID, mutation))
	}
	if err := s.writeArchiveIndex(workspaceID, archive); err != nil {
		return archiveMutation{}, errors.Join(err, s.rollbackArchiveMutationLocked(workspaceID, mutation))
	}
	return mutation, nil
}

func (s *Store) rollbackArchiveMutationLocked(workspaceID string, mutation archiveMutation) error {
	var result error
	if mutation.IndexExisted {
		result = errors.Join(result, s.writeArchiveIndex(workspaceID, mutation.Previous))
	} else if err := os.Remove(s.archiveIndexPath(workspaceID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		result = errors.Join(result, err)
	}
	for _, id := range mutation.CreatedPayloads {
		result = errors.Join(result, os.RemoveAll(s.archiveCheckpointDir(workspaceID, id)))
	}
	return result
}

func (s *Store) retentionCandidates(values []Summary) ([]archiveCandidate, []Summary) {
	cutoff := time.Now().UTC().Add(-s.retention())
	reasons := make(map[string]ArchiveReason)
	active := make([]Summary, 0, len(values))
	for _, summary := range values {
		created, err := time.Parse(time.RFC3339Nano, summary.CreatedAt)
		if err == nil && created.Before(cutoff) {
			reasons[summary.ID] = ArchiveReasonRetentionAge
			continue
		}
		active = append(active, summary)
	}
	for len(active) > s.maxCount() {
		summary := active[0]
		active = active[1:]
		if _, exists := reasons[summary.ID]; !exists {
			reasons[summary.ID] = ArchiveReasonRetentionCount
		}
	}
	candidates := make([]archiveCandidate, 0, len(reasons))
	kept := make([]Summary, 0, len(values)-len(reasons))
	for _, summary := range values {
		reason, archived := reasons[summary.ID]
		if archived {
			candidates = append(candidates, archiveCandidate{Summary: summary, Reason: reason})
			continue
		}
		kept = append(kept, summary)
	}
	return candidates, kept
}

func (s *Store) ensureArchivedPayload(workspaceID string, summary Summary) error {
	final := s.archiveCheckpointDir(workspaceID, summary.ID)
	if _, err := os.Lstat(final); err == nil {
		return s.validateArchivedPayload(workspaceID, summary)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	source := s.checkpointDir(workspaceID, summary.ID)
	if err := validateCheckpointPayloadDir(source, workspaceID, summary); err != nil {
		return fmt.Errorf("validate active checkpoint %s before archive: %w", summary.ID, err)
	}
	dataRoot := s.archiveDataRoot(workspaceID)
	if err := os.MkdirAll(dataRoot, 0700); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(dataRoot, "."+summary.ID+".tmp-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if err := copyCheckpointPayloadTree(source, staging); err != nil {
		return err
	}
	if err := validateCheckpointPayloadDir(staging, workspaceID, summary); err != nil {
		return fmt.Errorf("validate staged checkpoint archive %s: %w", summary.ID, err)
	}
	if err := os.Rename(staging, final); err != nil {
		if _, statErr := os.Lstat(final); statErr == nil {
			return s.validateArchivedPayload(workspaceID, summary)
		}
		return err
	}
	return s.validateArchivedPayload(workspaceID, summary)
}

func (s *Store) validateArchivedPayload(workspaceID string, summary Summary) error {
	return validateCheckpointPayloadDir(s.archiveCheckpointDir(workspaceID, summary.ID), workspaceID, summary)
}

func validateCheckpointPayloadDir(root, workspaceID string, summary Summary) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("checkpoint payload root is not a real directory: %s", root)
	}
	manifestPath := filepath.Join(root, "manifest.json")
	data, err := readBoundedRegularFile(manifestPath, maxArchiveManifestBytes)
	if err != nil {
		return err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return err
	}
	if manifest.Version != indexVersion || manifest.ID != summary.ID || manifest.WorkspaceID != workspaceID {
		return fmt.Errorf("checkpoint payload identity mismatch: %s", summary.ID)
	}
	if !reflect.DeepEqual(buildSummary(manifest), summary) {
		return fmt.Errorf("checkpoint payload summary mismatch: %s", summary.ID)
	}
	roots := effectiveRoots(manifest.WorkspaceRoot, manifest.AllowedRoots)
	for _, snapshot := range manifest.Files {
		if err := validateArchivedSnapshot(root, roots, snapshot); err != nil {
			return fmt.Errorf("checkpoint %s: %w", summary.ID, err)
		}
	}
	return validateCheckpointPayloadTree(root)
}

func readBoundedRegularFile(path string, maxBytes int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("checkpoint state file is not a regular non-symlink file: %s", path)
	}
	if info.Size() < 0 || info.Size() > maxBytes {
		return nil, fmt.Errorf("checkpoint state file exceeds safe bounds: %s", path)
	}
	return os.ReadFile(path)
}

func validateArchivedSnapshot(root string, roots []string, snapshot FileSnapshot) error {
	if strings.TrimSpace(snapshot.Path) == "" || !filepath.IsAbs(snapshot.Path) || !withinAny(roots, snapshot.Path) {
		return fmt.Errorf("snapshot path escapes checkpoint roots: %s", snapshot.Path)
	}
	if snapshot.Blob != "" {
		clean := filepath.Clean(filepath.FromSlash(snapshot.Blob))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("invalid checkpoint blob path: %s", snapshot.Blob)
		}
		path := filepath.Join(root, clean)
		if !within(root, path) {
			return fmt.Errorf("checkpoint blob escapes payload: %s", snapshot.Blob)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("checkpoint blob is not a regular non-symlink file: %s", snapshot.Blob)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		hash := sha256.New()
		size, copyErr := io.Copy(hash, io.LimitReader(file, maxArchiveBytesPerEntry+1))
		closeErr := file.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return err
		}
		if size > maxArchiveBytesPerEntry {
			return fmt.Errorf("checkpoint blob exceeds archive validation budget: %s", snapshot.Blob)
		}
		if snapshot.Size > 0 && size != snapshot.Size {
			return fmt.Errorf("checkpoint blob size mismatch: %s", snapshot.Blob)
		}
		if snapshot.BlobSHA256 != "" && !strings.EqualFold(snapshot.BlobSHA256, hex.EncodeToString(hash.Sum(nil))) {
			return fmt.Errorf("checkpoint blob hash mismatch (checksum mismatch): %s", snapshot.Blob)
		}
	}
	for _, child := range snapshot.Children {
		if err := validateArchivedSnapshot(root, roots, child); err != nil {
			return err
		}
	}
	return nil
}

func validateCheckpointPayloadTree(root string) error {
	files := 0
	var bytes int64
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("checkpoint payload contains symlink: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("checkpoint payload contains non-regular file: %s", path)
		}
		files++
		if files > maxArchiveFilesPerEntry {
			return fmt.Errorf("checkpoint payload exceeds %d files", maxArchiveFilesPerEntry)
		}
		if info.Size() < 0 || bytes > maxArchiveBytesPerEntry-info.Size() {
			return errors.New("checkpoint payload exceeds archive size budget")
		}
		bytes += info.Size()
		return nil
	})
}

func copyCheckpointPayloadTree(source, destination string) error {
	files := 0
	var bytes int64
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("checkpoint payload contains symlink: %s", path)
		}
		target := filepath.Join(destination, relative)
		if !within(destination, target) {
			return fmt.Errorf("checkpoint archive copy escapes destination: %s", relative)
		}
		if entry.IsDir() {
			if relative == "." {
				return nil
			}
			return os.MkdirAll(target, 0700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("checkpoint payload contains non-regular file: %s", path)
		}
		files++
		if files > maxArchiveFilesPerEntry {
			return fmt.Errorf("checkpoint payload exceeds %d files", maxArchiveFilesPerEntry)
		}
		if info.Size() < 0 || bytes > maxArchiveBytesPerEntry-info.Size() {
			return errors.New("checkpoint payload exceeds archive size budget")
		}
		bytes += info.Size()
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		sourceFile, err := os.Open(path)
		if err != nil {
			return err
		}
		targetFile, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			_ = sourceFile.Close()
			return err
		}
		written, copyErr := io.Copy(targetFile, io.LimitReader(sourceFile, info.Size()+1))
		syncErr := targetFile.Sync()
		closeTargetErr := targetFile.Close()
		closeSourceErr := sourceFile.Close()
		if err := errors.Join(copyErr, syncErr, closeTargetErr, closeSourceErr); err != nil {
			return err
		}
		if written != info.Size() {
			return fmt.Errorf("checkpoint payload changed while archiving: %s", path)
		}
		return nil
	})
}

func (s *Store) readArchiveIndex(workspaceID string) (ArchiveIndex, error) {
	path := s.archiveIndexPath(workspaceID)
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return ArchiveIndex{Version: archiveIndexVersion, Checkpoints: []ArchivedSummary{}}, nil
	}
	if err != nil {
		return ArchiveIndex{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ArchiveIndex{}, err
	}
	if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maxArchiveIndexBytes {
		return ArchiveIndex{}, fmt.Errorf("checkpoint archive index exceeds safe bounds: %s", path)
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxArchiveIndexBytes+1))
	decoder.DisallowUnknownFields()
	var index ArchiveIndex
	if err := decoder.Decode(&index); err != nil {
		return ArchiveIndex{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ArchiveIndex{}, errors.New("checkpoint archive index contains trailing data")
	}
	if index.Version != archiveIndexVersion {
		return ArchiveIndex{}, fmt.Errorf("unsupported checkpoint archive index version: %d", index.Version)
	}
	if len(index.Checkpoints) > maxArchiveEntries {
		return ArchiveIndex{}, fmt.Errorf("checkpoint archive exceeds %d entries", maxArchiveEntries)
	}
	if index.Checkpoints == nil {
		index.Checkpoints = []ArchivedSummary{}
	}
	seen := make(map[string]struct{}, len(index.Checkpoints))
	for _, item := range index.Checkpoints {
		if strings.TrimSpace(item.Checkpoint.ID) == "" {
			return ArchiveIndex{}, errors.New("checkpoint archive contains empty id")
		}
		if _, exists := seen[item.Checkpoint.ID]; exists {
			return ArchiveIndex{}, fmt.Errorf("checkpoint archive contains duplicate id: %s", item.Checkpoint.ID)
		}
		seen[item.Checkpoint.ID] = struct{}{}
		if _, err := time.Parse(time.RFC3339Nano, item.ArchivedAt); err != nil {
			return ArchiveIndex{}, fmt.Errorf("checkpoint archive has invalid archived_at for %s", item.Checkpoint.ID)
		}
		switch item.Reason {
		case ArchiveReasonRetentionAge, ArchiveReasonRetentionCount, ArchiveReasonRestore, ArchiveReasonClear:
		default:
			return ArchiveIndex{}, fmt.Errorf("checkpoint archive has unsupported reason %q", item.Reason)
		}
	}
	return index, nil
}

func (s *Store) writeArchiveIndex(workspaceID string, index ArchiveIndex) error {
	if len(index.Checkpoints) > maxArchiveEntries {
		return fmt.Errorf("checkpoint archive exceeds %d entries", maxArchiveEntries)
	}
	if err := os.MkdirAll(s.archiveRoot(workspaceID), 0700); err != nil {
		return err
	}
	index.Version = archiveIndexVersion
	if index.Checkpoints == nil {
		index.Checkpoints = []ArchivedSummary{}
	}
	return writeStructuredAtomic(s.archiveIndexPath(workspaceID), index, 0600)
}

func (s *Store) archiveRoot(workspaceID string) string {
	return filepath.Join(s.Path(workspaceID), "archive")
}

func (s *Store) archiveIndexPath(workspaceID string) string {
	return filepath.Join(s.archiveRoot(workspaceID), "index.json")
}

func (s *Store) archiveDataRoot(workspaceID string) string {
	return filepath.Join(s.archiveRoot(workspaceID), "data")
}

func (s *Store) archiveCheckpointDir(workspaceID, id string) string {
	return filepath.Join(s.archiveDataRoot(workspaceID), id)
}

func boundedArchiveLimit(limit int) int {
	if limit <= 0 {
		return defaultArchiveListLimit
	}
	if limit > maxArchiveListLimit {
		return maxArchiveListLimit
	}
	return limit
}
