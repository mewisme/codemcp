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
	"sort"
	"strings"
	"time"
)

var ErrWorkspaceMergeConflict = errors.New("checkpoint merge conflict")

type mergeCheckpoint struct {
	manifest Manifest
	root     string
	blobs    map[string]string
}

type mergeArchivedCheckpoint struct {
	record     ArchivedSummary
	checkpoint mergeCheckpoint
}

func MergeWorkspaceState(registeredRoot, destinationRoot, outputRoot, workspaceID, registeredWorkspaceRoot, destinationWorkspaceRoot string) error {
	registered, err := loadMergeCheckpoints(registeredRoot, workspaceID, registeredWorkspaceRoot, destinationWorkspaceRoot)
	if err != nil {
		return fmt.Errorf("registered checkpoints: %w", err)
	}
	destination, err := loadMergeCheckpoints(destinationRoot, workspaceID, destinationWorkspaceRoot, destinationWorkspaceRoot)
	if err != nil {
		return fmt.Errorf("destination checkpoints: %w", err)
	}
	registeredArchive, err := loadMergeArchivedCheckpoints(registeredRoot, workspaceID, registeredWorkspaceRoot, destinationWorkspaceRoot)
	if err != nil {
		return fmt.Errorf("registered checkpoint archive: %w", err)
	}
	destinationArchive, err := loadMergeArchivedCheckpoints(destinationRoot, workspaceID, destinationWorkspaceRoot, destinationWorkspaceRoot)
	if err != nil {
		return fmt.Errorf("destination checkpoint archive: %w", err)
	}
	merged := map[string]mergeCheckpoint{}
	for _, group := range []map[string]mergeCheckpoint{registered, destination} {
		for id, candidate := range group {
			if current, ok := merged[id]; ok {
				if !reflect.DeepEqual(current.manifest, candidate.manifest) || !reflect.DeepEqual(current.blobs, candidate.blobs) {
					return fmt.Errorf("%w: divergent checkpoint id %s", ErrWorkspaceMergeConflict, id)
				}
				continue
			}
			merged[id] = candidate
		}
	}
	if len(merged) > 0 {
		if err := os.MkdirAll(filepath.Join(outputRoot, "data"), 0700); err != nil {
			return err
		}
		ids := sortedMergeCheckpointIDs(merged)
		index := Index{Version: indexVersion, Checkpoints: make([]Summary, 0, len(ids))}
		for _, id := range ids {
			candidate := merged[id]
			target := filepath.Join(outputRoot, "data", id)
			if err := writeMergeCheckpoint(target, candidate); err != nil {
				return err
			}
			index.Checkpoints = append(index.Checkpoints, buildSummary(candidate.manifest))
		}
		if err := writeStructuredAtomic(filepath.Join(outputRoot, "index.json"), index, 0600); err != nil {
			return err
		}
	}
	if err := mergeArchivedWorkspaceState(outputRoot, merged, registeredArchive, destinationArchive); err != nil {
		return err
	}
	if _, err = loadMergeCheckpoints(outputRoot, workspaceID, destinationWorkspaceRoot, destinationWorkspaceRoot); err != nil {
		return err
	}
	_, err = loadMergeArchivedCheckpoints(outputRoot, workspaceID, destinationWorkspaceRoot, destinationWorkspaceRoot)
	return err
}

func sortedMergeCheckpointIDs(values map[string]mergeCheckpoint) []string {
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		left, right := values[ids[i]].manifest, values[ids[j]].manifest
		if left.CreatedAt == right.CreatedAt {
			return ids[i] < ids[j]
		}
		return left.CreatedAt < right.CreatedAt
	})
	return ids
}

func writeMergeCheckpoint(target string, candidate mergeCheckpoint) error {
	if err := os.MkdirAll(target, 0700); err != nil {
		return err
	}
	for relative := range candidate.blobs {
		source := filepath.Join(candidate.root, filepath.FromSlash(relative))
		destination := filepath.Join(target, filepath.FromSlash(relative))
		if err := copyMergeCheckpointFile(source, destination); err != nil {
			return err
		}
	}
	return writeStructuredAtomic(filepath.Join(target, "manifest.json"), candidate.manifest, 0600)
}

func loadMergeCheckpoints(root, workspaceID, expectedRoot, destinationRoot string) (map[string]mergeCheckpoint, error) {
	result := map[string]mergeCheckpoint{}
	indexPath := filepath.Join(root, "index.json")
	data, err := os.ReadFile(indexPath)
	if errors.Is(err, os.ErrNotExist) {
		if _, statErr := os.Stat(root); errors.Is(statErr, os.ErrNotExist) {
			return result, nil
		}
		entries, readErr := os.ReadDir(root)
		if readErr != nil {
			return nil, readErr
		}
		if len(entries) == 0 {
			return result, nil
		}
		archiveOnly := true
		for _, entry := range entries {
			if entry.Name() != "archive" || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				archiveOnly = false
				break
			}
		}
		if archiveOnly {
			return result, nil
		}
		return nil, errors.New("checkpoint state exists without index")
	}
	if err != nil {
		return nil, err
	}
	var index Index
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}
	if index.Version != indexVersion {
		return nil, fmt.Errorf("unsupported checkpoint index version: %d", index.Version)
	}
	allowedFiles := map[string]struct{}{"index.json": {}}
	for _, summary := range index.Checkpoints {
		if strings.TrimSpace(summary.ID) == "" {
			return nil, errors.New("checkpoint index contains empty id")
		}
		if _, exists := result[summary.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate checkpoint id %s", ErrWorkspaceMergeConflict, summary.ID)
		}
		checkpointRoot := filepath.Join(root, "data", summary.ID)
		manifestPath := filepath.Join(checkpointRoot, "manifest.json")
		manifestData, err := os.ReadFile(manifestPath)
		if err != nil {
			return nil, err
		}
		var manifest Manifest
		if err := json.Unmarshal(manifestData, &manifest); err != nil {
			return nil, err
		}
		if manifest.Version != indexVersion || manifest.ID != summary.ID || manifest.WorkspaceID != workspaceID {
			return nil, fmt.Errorf("checkpoint %s workspace binding is invalid", summary.ID)
		}
		if filepath.Clean(manifest.WorkspaceRoot) != filepath.Clean(expectedRoot) {
			return nil, fmt.Errorf("checkpoint %s workspace root mismatch", summary.ID)
		}
		manifest.WorkspaceRoot = filepath.Clean(destinationRoot)
		for i, allowed := range manifest.AllowedRoots {
			manifest.AllowedRoots[i] = relocateCheckpointPath(allowed, expectedRoot, destinationRoot)
		}
		blobs := map[string]string{}
		for i := range manifest.Files {
			if err := rewriteMergeSnapshot(&manifest.Files[i], expectedRoot, destinationRoot, checkpointRoot, blobs); err != nil {
				return nil, fmt.Errorf("checkpoint %s: %w", summary.ID, err)
			}
		}
		roots := effectiveRoots(manifest.WorkspaceRoot, manifest.AllowedRoots)
		for i := range manifest.Files {
			if err := validateMergeSnapshotPaths(manifest.Files[i], roots); err != nil {
				return nil, fmt.Errorf("checkpoint %s: %w", summary.ID, err)
			}
		}
		result[summary.ID] = mergeCheckpoint{manifest: manifest, root: checkpointRoot, blobs: blobs}
		allowedFiles[filepath.ToSlash(filepath.Join("data", summary.ID, "manifest.json"))] = struct{}{}
		for relative := range blobs {
			allowedFiles[filepath.ToSlash(filepath.Join("data", summary.ID, relative))] = struct{}{}
		}
	}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("checkpoint state contains symlink: %s", path)
		}
		if entry.IsDir() {
			if path == filepath.Join(root, "archive") {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("checkpoint state contains non-regular file: %s", path)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, ok := allowedFiles[filepath.ToSlash(relative)]; !ok {
			return fmt.Errorf("unsupported checkpoint state entry: %s", filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func loadMergeArchivedCheckpoints(root, workspaceID, expectedRoot, destinationRoot string) (map[string]mergeArchivedCheckpoint, error) {
	result := map[string]mergeArchivedCheckpoint{}
	archiveRoot := filepath.Join(root, "archive")
	indexPath := filepath.Join(archiveRoot, "index.json")
	data, err := readBoundedRegularFile(indexPath, maxArchiveIndexBytes)
	if errors.Is(err, os.ErrNotExist) {
		entries, readErr := os.ReadDir(archiveRoot)
		if errors.Is(readErr, os.ErrNotExist) {
			return result, nil
		}
		if readErr != nil {
			return nil, readErr
		}
		if len(entries) == 0 {
			return result, nil
		}
		return nil, errors.New("checkpoint archive exists without index")
	}
	if err != nil {
		return nil, err
	}
	var index ArchiveIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}
	if index.Version != archiveIndexVersion {
		return nil, fmt.Errorf("unsupported checkpoint archive index version: %d", index.Version)
	}
	if len(index.Checkpoints) > maxArchiveEntries {
		return nil, fmt.Errorf("checkpoint archive exceeds %d entries", maxArchiveEntries)
	}
	allowedFiles := map[string]struct{}{"index.json": {}}
	for _, archived := range index.Checkpoints {
		summary := archived.Checkpoint
		if strings.TrimSpace(summary.ID) == "" {
			return nil, errors.New("checkpoint archive contains empty id")
		}
		if _, exists := result[summary.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate archived checkpoint id %s", ErrWorkspaceMergeConflict, summary.ID)
		}
		if _, err := time.Parse(time.RFC3339Nano, archived.ArchivedAt); err != nil {
			return nil, fmt.Errorf("archived checkpoint %s has invalid archived_at", summary.ID)
		}
		switch archived.Reason {
		case ArchiveReasonRetentionAge, ArchiveReasonRetentionCount, ArchiveReasonRestore, ArchiveReasonClear:
		default:
			return nil, fmt.Errorf("archived checkpoint %s has unsupported reason %q", summary.ID, archived.Reason)
		}
		checkpointRoot := filepath.Join(archiveRoot, "data", summary.ID)
		manifestData, err := readBoundedRegularFile(filepath.Join(checkpointRoot, "manifest.json"), maxArchiveManifestBytes)
		if err != nil {
			return nil, err
		}
		var manifest Manifest
		if err := json.Unmarshal(manifestData, &manifest); err != nil {
			return nil, err
		}
		if manifest.Version != indexVersion || manifest.ID != summary.ID || manifest.WorkspaceID != workspaceID {
			return nil, fmt.Errorf("archived checkpoint %s workspace binding is invalid", summary.ID)
		}
		if filepath.Clean(manifest.WorkspaceRoot) != filepath.Clean(expectedRoot) {
			return nil, fmt.Errorf("archived checkpoint %s workspace root mismatch", summary.ID)
		}
		manifest.WorkspaceRoot = filepath.Clean(destinationRoot)
		for i, allowed := range manifest.AllowedRoots {
			manifest.AllowedRoots[i] = relocateCheckpointPath(allowed, expectedRoot, destinationRoot)
		}
		blobs := map[string]string{}
		for i := range manifest.Files {
			if err := rewriteMergeSnapshot(&manifest.Files[i], expectedRoot, destinationRoot, checkpointRoot, blobs); err != nil {
				return nil, fmt.Errorf("archived checkpoint %s: %w", summary.ID, err)
			}
		}
		roots := effectiveRoots(manifest.WorkspaceRoot, manifest.AllowedRoots)
		for i := range manifest.Files {
			if err := validateMergeSnapshotPaths(manifest.Files[i], roots); err != nil {
				return nil, fmt.Errorf("archived checkpoint %s: %w", summary.ID, err)
			}
		}
		archived.Checkpoint = buildSummary(manifest)
		result[summary.ID] = mergeArchivedCheckpoint{
			record:     archived,
			checkpoint: mergeCheckpoint{manifest: manifest, root: checkpointRoot, blobs: blobs},
		}
		allowedFiles[filepath.ToSlash(filepath.Join("data", summary.ID, "manifest.json"))] = struct{}{}
		for relative := range blobs {
			allowedFiles[filepath.ToSlash(filepath.Join("data", summary.ID, relative))] = struct{}{}
		}
	}
	err = filepath.WalkDir(archiveRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("checkpoint archive contains symlink: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("checkpoint archive contains non-regular file: %s", path)
		}
		relative, err := filepath.Rel(archiveRoot, path)
		if err != nil {
			return err
		}
		if _, ok := allowedFiles[filepath.ToSlash(relative)]; !ok {
			return fmt.Errorf("unsupported checkpoint archive entry: %s", filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func mergeArchivedWorkspaceState(outputRoot string, active map[string]mergeCheckpoint, groups ...map[string]mergeArchivedCheckpoint) error {
	merged := map[string]mergeArchivedCheckpoint{}
	for _, group := range groups {
		for id, candidate := range group {
			if activeCandidate, ok := active[id]; ok {
				if !reflect.DeepEqual(activeCandidate.manifest, candidate.checkpoint.manifest) || !reflect.DeepEqual(activeCandidate.blobs, candidate.checkpoint.blobs) {
					return fmt.Errorf("%w: checkpoint %s is active and archived with divergent payloads", ErrWorkspaceMergeConflict, id)
				}
				continue
			}
			if current, ok := merged[id]; ok {
				if !reflect.DeepEqual(current.checkpoint.manifest, candidate.checkpoint.manifest) ||
					!reflect.DeepEqual(current.checkpoint.blobs, candidate.checkpoint.blobs) ||
					current.record.ArchivedAt != candidate.record.ArchivedAt ||
					current.record.Reason != candidate.record.Reason {
					return fmt.Errorf("%w: divergent archived checkpoint id %s", ErrWorkspaceMergeConflict, id)
				}
				continue
			}
			merged[id] = candidate
		}
	}
	if len(merged) == 0 {
		return nil
	}
	archiveRoot := filepath.Join(outputRoot, "archive")
	if err := os.MkdirAll(filepath.Join(archiveRoot, "data"), 0700); err != nil {
		return err
	}
	checkpoints := make(map[string]mergeCheckpoint, len(merged))
	for id, item := range merged {
		checkpoints[id] = item.checkpoint
	}
	ids := sortedMergeCheckpointIDs(checkpoints)
	index := ArchiveIndex{Version: archiveIndexVersion, Checkpoints: make([]ArchivedSummary, 0, len(ids))}
	for _, id := range ids {
		candidate := merged[id]
		target := filepath.Join(archiveRoot, "data", id)
		if err := writeMergeCheckpoint(target, candidate.checkpoint); err != nil {
			return err
		}
		record := candidate.record
		record.Checkpoint = buildSummary(candidate.checkpoint.manifest)
		index.Checkpoints = append(index.Checkpoints, record)
	}
	return writeStructuredAtomic(filepath.Join(archiveRoot, "index.json"), index, 0600)
}

func validateMergeSnapshotPaths(snapshot FileSnapshot, roots []string) error {
	if strings.TrimSpace(snapshot.Path) == "" || !filepath.IsAbs(snapshot.Path) || !withinAny(roots, snapshot.Path) {
		return fmt.Errorf("checkpoint snapshot escapes destination access roots: %s", snapshot.Path)
	}
	for _, child := range snapshot.Children {
		if err := validateMergeSnapshotPaths(child, roots); err != nil {
			return err
		}
	}
	return nil
}

func rewriteMergeSnapshot(snapshot *FileSnapshot, oldRoot, newRoot, checkpointRoot string, blobs map[string]string) error {
	if snapshot == nil {
		return nil
	}
	snapshot.Path = relocateCheckpointPath(snapshot.Path, oldRoot, newRoot)
	if strings.TrimSpace(snapshot.LinkTarget) != "" && filepath.IsAbs(snapshot.LinkTarget) {
		snapshot.LinkTarget = relocateCheckpointPath(snapshot.LinkTarget, oldRoot, newRoot)
	}
	if snapshot.Blob != "" {
		clean := filepath.Clean(filepath.FromSlash(snapshot.Blob))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe checkpoint blob path %s", snapshot.Blob)
		}
		path := filepath.Join(checkpointRoot, clean)
		if !within(checkpointRoot, path) {
			return fmt.Errorf("checkpoint blob escapes checkpoint: %s", snapshot.Blob)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("checkpoint blob is not a regular non-symlink file: %s", snapshot.Blob)
		}
		digest, err := mergeCheckpointDigest(path)
		if err != nil {
			return err
		}
		if snapshot.BlobSHA256 != "" && !strings.EqualFold(snapshot.BlobSHA256, digest) {
			return fmt.Errorf("checkpoint blob hash mismatch: %s", snapshot.Blob)
		}
		snapshot.BlobSHA256 = digest
		blobs[filepath.ToSlash(clean)] = digest
	}
	for i := range snapshot.Children {
		if err := rewriteMergeSnapshot(&snapshot.Children[i], oldRoot, newRoot, checkpointRoot, blobs); err != nil {
			return err
		}
	}
	return nil
}

func relocateCheckpointPath(value, oldRoot, newRoot string) string {
	if strings.TrimSpace(value) == "" || !filepath.IsAbs(value) {
		return value
	}
	value, oldRoot, newRoot = filepath.Clean(value), filepath.Clean(oldRoot), filepath.Clean(newRoot)
	relative, err := filepath.Rel(oldRoot, value)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return value
	}
	return filepath.Join(newRoot, relative)
}

func mergeCheckpointDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func copyMergeCheckpointFile(source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	syncErr := output.Sync()
	closeErr := output.Close()
	return errors.Join(copyErr, syncErr, closeErr)
}
