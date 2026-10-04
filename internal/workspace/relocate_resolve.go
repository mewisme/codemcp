package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/oslock"
	"go.mewis.me/codemcp/internal/state"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

const staleMergeArtifactAge = 24 * time.Hour

type RelocationResolution string

const (
	RelocationResolutionDestination RelocationResolution = "destination"
	RelocationResolutionRegistered  RelocationResolution = "registered"
	RelocationResolutionMerge       RelocationResolution = "merge"
)

var (
	ErrInvalidRelocationResolution = errors.New("invalid workspace relocation resolution")
	ErrRelocationMergeUnavailable  = errors.New("workspace relocation merge requires typed domain reconciliation")
)

type DuplicateMergeRequest struct {
	WorkspaceID          string
	RegisteredRoot       string
	DestinationRoot      string
	RegisteredStateRoot  string
	DestinationStateRoot string
	OutputStateRoot      string
	AllowedRoots         []string
}

type DuplicateMergeResolver func(DuplicateMergeRequest) error

func ParseRelocationResolution(value string) (RelocationResolution, error) {
	resolution := RelocationResolution(strings.ToLower(strings.TrimSpace(value)))
	switch resolution {
	case "", RelocationResolutionDestination, RelocationResolutionRegistered, RelocationResolutionMerge:
		return resolution, nil
	default:
		return "", fmt.Errorf("%w: %q (expected destination, registered, or merge)", ErrInvalidRelocationResolution, value)
	}
}

func (e *DuplicateWorkspaceIdentityError) Resolutions() []RelocationResolution {
	if e == nil {
		return nil
	}
	return []RelocationResolution{
		RelocationResolutionDestination,
		RelocationResolutionRegistered,
		RelocationResolutionMerge,
	}
}

type relocationFingerprint struct {
	SHA256 string `json:"sha256"`
	Files  int    `json:"files"`
	Bytes  int64  `json:"bytes"`
}

type relocationJournal struct {
	Version                int                   `json:"version"`
	WorkspaceID            string                `json:"workspace_id"`
	RegisteredRoot         string                `json:"registered_root"`
	DestinationRoot        string                `json:"destination_root"`
	Resolution             RelocationResolution  `json:"resolution"`
	RegisteredFingerprint  relocationFingerprint `json:"registered_fingerprint"`
	DestinationFingerprint relocationFingerprint `json:"destination_fingerprint"`
	Status                 string                `json:"status"`
}

const (
	relocationJournalVersion       = 1
	relocationMaxFiles             = 100000
	relocationMaxBytes       int64 = 1 << 30
)

var duplicateRelocationFailureHook = func(string) error { return nil }

func (m *Manager) ResolveDuplicateRelocation(id, path string, resolution RelocationResolution) (Workspace, error) {
	return m.ResolveDuplicateRelocationWithMerge(id, path, resolution, nil)
}

func (m *Manager) ResolveDuplicateRelocationWithMerge(id, path string, resolution RelocationResolution, merge DuplicateMergeResolver) (Workspace, error) {
	if m == nil {
		return Workspace{}, errors.New("workspace manager is unavailable")
	}
	parsed, err := ParseRelocationResolution(string(resolution))
	if err != nil {
		return Workspace{}, err
	}
	if parsed == "" {
		return m.Relocate(id, path)
	}
	if parsed == RelocationResolutionMerge && merge == nil {
		return Workspace{}, ErrRelocationMergeUnavailable
	}

	span := tracepkg.StartObserver(m.trace, "WORKSPACE", "workspace.relocate.resolve", "Resolving duplicate workspace identity", tracepkg.String("workspace_id", strings.TrimSpace(id)), tracepkg.String("input_path", path), tracepkg.String("resolution", string(parsed)))
	m.runtimeMu.Lock()
	defer m.runtimeMu.Unlock()
	mutation, err := m.beginRegistryMutation()
	if err != nil {
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}
	defer mutation.Release()

	destinationRoot, err := canonicalExistingDirectory(path)
	if err != nil {
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}
	if m.protected(destinationRoot) || m.workspaceLocalRootAliasesProtected(destinationRoot) {
		err := fmt.Errorf("workspace relocation destination aliases protected control-plane state: %s", destinationRoot)
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}

	m.mu.RLock()
	canonical := m.canonicalIDLocked(strings.TrimSpace(id))
	item, ok := m.items[canonical]
	active := m.runtime != nil && m.runtime.active && m.runtime.locks[canonical] != nil
	m.mu.RUnlock()
	if !ok {
		err := fmt.Errorf("%w: %s", ErrNotFound, id)
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}
	if active {
		err := fmt.Errorf("%w: %s: duplicate resolution requires the workspace runtime to release its local state first", ErrAlreadyActive, item.ID)
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}
	registeredRoot := filepath.Clean(item.Path)
	if sameCanonicalRoot(registeredRoot, destinationRoot) {
		return item, nil
	}

	registeredIdentity, err := workspacestate.New(registeredRoot).LoadIdentity()
	if err != nil {
		err = fmt.Errorf("registered workspace state is unavailable for duplicate resolution: %w", err)
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}
	destinationIdentity, err := workspacestate.New(destinationRoot).LoadIdentity()
	if err != nil {
		err = fmt.Errorf("destination workspace state is unavailable for duplicate resolution: %w", err)
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}
	if registeredIdentity.ID != item.ID || destinationIdentity.ID != item.ID {
		err := fmt.Errorf("duplicate resolution requires the same workspace identity at both roots: expected %s", item.ID)
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}

	if err := probeWorkspaceRuntimeLock(item); err != nil {
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}
	destinationItem := Workspace{ID: item.ID, Path: destinationRoot}
	if err := probeWorkspaceRuntimeLock(destinationItem); err != nil {
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}
	if parsed == RelocationResolutionMerge {
		for _, root := range []string{registeredRoot, destinationRoot} {
			if err := probeMergeTransientLocks(root, item.ID); err != nil {
				span.FailMessage("Workspace relocation resolution failed", err)
				return Workspace{}, err
			}
		}
	}

	registeredLocal := workspacestate.New(registeredRoot).Root()
	destinationLocal := workspacestate.New(destinationRoot).Root()
	registeredFingerprint, err := fingerprintRelocationState(registeredLocal)
	if err != nil {
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}
	destinationFingerprint, err := fingerprintRelocationState(destinationLocal)
	if err != nil {
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}

	transactionRoot := filepath.Join(filepath.Dir(m.path), "workspace-reconciliation")
	if err := os.MkdirAll(transactionRoot, 0700); err != nil {
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}
	transactionDir, err := os.MkdirTemp(transactionRoot, "txn-")
	if err != nil {
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}
	removeTransaction := true
	defer func() {
		if removeTransaction {
			_ = os.RemoveAll(transactionDir)
		}
	}()

	journal := relocationJournal{
		Version:                relocationJournalVersion,
		WorkspaceID:            item.ID,
		RegisteredRoot:         registeredRoot,
		DestinationRoot:        destinationRoot,
		Resolution:             parsed,
		RegisteredFingerprint:  registeredFingerprint,
		DestinationFingerprint: destinationFingerprint,
		Status:                 "planned",
	}
	journalPath := filepath.Join(transactionDir, "journal.json")
	if err := writeRelocationJournal(journalPath, journal); err != nil {
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}

	registeredBackup := filepath.Join(transactionDir, "registered-workspace", workspacestate.DirectoryName)
	destinationBackup := filepath.Join(transactionDir, "destination-workspace", workspacestate.DirectoryName)
	if err := copyRelocationState(registeredLocal, registeredBackup); err != nil {
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}
	if err := copyRelocationState(destinationLocal, destinationBackup); err != nil {
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}
	journal.Status = "staged"
	if err := writeRelocationJournal(journalPath, journal); err != nil {
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}
	if err := duplicateRelocationFailureHook("after_staging"); err != nil {
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}

	if err := verifyRelocationFingerprint(registeredLocal, registeredFingerprint); err != nil {
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}
	if err := verifyRelocationFingerprint(destinationLocal, destinationFingerprint); err != nil {
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}

	mergedStateRoot := ""
	if parsed == RelocationResolutionMerge {
		mergedWorkspaceRoot := filepath.Join(transactionDir, "merged-workspace")
		mergedStateRoot = filepath.Join(mergedWorkspaceRoot, workspacestate.DirectoryName)
		if err := os.MkdirAll(mergedWorkspaceRoot, 0700); err != nil {
			span.FailMessage("Workspace relocation resolution failed", err)
			return Workspace{}, err
		}
		if err := merge(DuplicateMergeRequest{
			WorkspaceID:          item.ID,
			RegisteredRoot:       registeredRoot,
			DestinationRoot:      destinationRoot,
			RegisteredStateRoot:  registeredBackup,
			DestinationStateRoot: destinationBackup,
			OutputStateRoot:      mergedStateRoot,
			AllowedRoots:         relocatedWorkspaceMetadata(item, registeredRoot, destinationRoot).AllowDirs,
		}); err != nil {
			span.FailMessage("Workspace relocation resolution failed", err)
			return Workspace{}, err
		}
		identity, err := workspacestate.New(mergedWorkspaceRoot).LoadIdentity()
		if err != nil || identity.ID != item.ID {
			if err == nil {
				err = fmt.Errorf("merged workspace identity changed to %s", identity.ID)
			}
			err = fmt.Errorf("validate merged workspace state: %w", err)
			span.FailMessage("Workspace relocation resolution failed", err)
			return Workspace{}, err
		}
		for _, transient := range []string{"runtime", "cache"} {
			if _, err := os.Lstat(filepath.Join(mergedStateRoot, transient)); !errors.Is(err, os.ErrNotExist) {
				if err == nil {
					err = fmt.Errorf("merged workspace state contains transient %s state", transient)
				}
				span.FailMessage("Workspace relocation resolution failed", err)
				return Workspace{}, err
			}
		}
		if _, err := fingerprintRelocationState(mergedStateRoot); err != nil {
			span.FailMessage("Workspace relocation resolution failed", err)
			return Workspace{}, err
		}
	}

	previous := item
	next := relocatedWorkspaceMetadata(item, registeredRoot, destinationRoot)
	registryChanged := false
	destinationChanged := false
	sourceRetired := false
	rollback := func(cause error) error {
		var rollbackErr error
		if registryChanged {
			m.mu.Lock()
			m.items[previous.ID] = previous
			if err := m.saveLocked(); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore workspace registry: %w", err))
			}
			m.mu.Unlock()
		}
		if sourceRetired {
			if err := restoreRelocationState(registeredBackup, registeredLocal); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore registered workspace state: %w", err))
			}
		}
		if destinationChanged {
			if err := restoreRelocationState(destinationBackup, destinationLocal); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore destination workspace state: %w", err))
			}
		}
		journal.Status = "rolled_back"
		_ = writeRelocationJournal(journalPath, journal)
		if rollbackErr != nil {
			removeTransaction = false
			return errors.Join(cause, rollbackErr, fmt.Errorf("reconciliation journal retained at %s", boundedWorkspaceDiagnostic(journalPath)))
		}
		return cause
	}

	if parsed == RelocationResolutionRegistered {
		destinationChanged = true
		if err := replaceRelocationState(registeredBackup, destinationLocal); err != nil {
			err = rollback(err)
			span.FailMessage("Workspace relocation resolution failed", err)
			return Workspace{}, err
		}
		identity, verifyErr := workspacestate.New(destinationRoot).LoadIdentity()
		if verifyErr != nil || identity.ID != item.ID {
			if verifyErr == nil {
				verifyErr = fmt.Errorf("staged registered state identity changed to %s", identity.ID)
			}
			err = rollback(fmt.Errorf("verify staged registered workspace state: %w", verifyErr))
			span.FailMessage("Workspace relocation resolution failed", err)
			return Workspace{}, err
		}
		if err := duplicateRelocationFailureHook("after_destination_write"); err != nil {
			err = rollback(err)
			span.FailMessage("Workspace relocation resolution failed", err)
			return Workspace{}, err
		}
	}
	if parsed == RelocationResolutionMerge {
		destinationChanged = true
		if err := activateMergedRelocationState(mergedStateRoot, destinationRoot); err != nil {
			err = rollback(err)
			span.FailMessage("Workspace relocation resolution failed", err)
			return Workspace{}, err
		}
		if err := duplicateRelocationFailureHook("after_destination_write"); err != nil {
			err = rollback(err)
			span.FailMessage("Workspace relocation resolution failed", err)
			return Workspace{}, err
		}
	}

	m.mu.Lock()
	m.items[item.ID] = next
	if err := relocateRegistrySave(m); err != nil {
		m.items[item.ID] = previous
		m.mu.Unlock()
		err = rollback(err)
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}
	m.mu.Unlock()
	registryChanged = true
	journal.Status = "registry_saved"
	_ = writeRelocationJournal(journalPath, journal)
	if err := duplicateRelocationFailureHook("after_registry_write"); err != nil {
		err = rollback(err)
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}

	sourceRetired = true
	if err := os.RemoveAll(registeredLocal); err != nil {
		err = rollback(fmt.Errorf("retire registered workspace state: %w", err))
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}
	if err := duplicateRelocationFailureHook("after_source_retire"); err != nil {
		err = rollback(err)
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}

	identity, err := workspacestate.New(destinationRoot).LoadIdentity()
	if err != nil || identity.ID != item.ID {
		if err == nil {
			err = fmt.Errorf("destination identity changed to %s", identity.ID)
		}
		err = rollback(fmt.Errorf("verify activated destination workspace state: %w", err))
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}
	if _, err := os.Lstat(workspacestate.New(registeredRoot).IdentityPath()); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = errors.New("registered source identity remains active after relocation")
		}
		err = rollback(err)
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}

	journal.Status = "committed"
	if err := writeRelocationJournal(journalPath, journal); err != nil {
		err = rollback(err)
		span.FailMessage("Workspace relocation resolution failed", err)
		return Workspace{}, err
	}
	span.EndMessage("Duplicate workspace identity resolved", tracepkg.String("workspace_id", item.ID), tracepkg.String("registered_root", registeredRoot), tracepkg.String("destination_root", destinationRoot), tracepkg.String("resolution", string(parsed)))
	return next, nil
}

func probeMergeTransientLocks(workspaceRoot, workspaceID string) error {
	local := workspacestate.New(workspaceRoot)
	path, err := local.Join("runtime", "codegraph.lock")
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	lock, ok, err := oslock.TryAcquireExisting(path, oslock.Exclusive)
	if err != nil {
		return fmt.Errorf("%w: %s: probe derived-state mutation lock: %v", ErrStateLost, workspaceID, err)
	}
	if !ok {
		return fmt.Errorf("%w: %s: derived workspace state is owned by another runtime", ErrAlreadyActive, workspaceID)
	}
	return lock.Release()
}

func activateMergedRelocationState(source, destinationRoot string) error {
	destination := workspacestate.New(destinationRoot).Root()
	pruneStaleMergeArtifacts(destinationRoot, destination, time.Now())
	stage, err := os.MkdirTemp(destinationRoot, ".cm-merge-stage-")
	if err != nil {
		return err
	}
	stageActive := true
	defer func() {
		if stageActive {
			_ = os.RemoveAll(stage)
		}
	}()
	if err := copyRelocationState(source, stage); err != nil {
		return err
	}
	expected, err := fingerprintRelocationState(source)
	if err != nil {
		return err
	}
	if err := verifyRelocationFingerprint(stage, expected); err != nil {
		return err
	}
	old, err := os.MkdirTemp(destinationRoot, ".cm-merge-old-")
	if err != nil {
		return err
	}
	if err := os.Remove(old); err != nil {
		return err
	}
	if err := os.Rename(destination, old); err != nil {
		return fmt.Errorf("stage existing destination workspace state: %w", err)
	}
	if err := os.Rename(stage, destination); err != nil {
		_ = os.Rename(old, destination)
		return fmt.Errorf("activate merged workspace state: %w", err)
	}
	stageActive = false
	if err := os.RemoveAll(old); err != nil {
		return fmt.Errorf("retire previous destination workspace state: %w", err)
	}
	return nil
}

func pruneStaleMergeArtifacts(destinationRoot, destination string, now time.Time) {
	info, err := os.Lstat(destination)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return
	}
	entries, err := os.ReadDir(destinationRoot)
	if err != nil {
		return
	}
	cutoff := now.Add(-staleMergeArtifactAge)
	for _, entry := range entries {
		if !entry.IsDir() || (!strings.HasPrefix(entry.Name(), ".cm-merge-stage-") && !strings.HasPrefix(entry.Name(), ".cm-merge-old-")) {
			continue
		}
		entryInfo, infoErr := entry.Info()
		if infoErr != nil || !entryInfo.ModTime().Before(cutoff) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(destinationRoot, entry.Name()))
	}
}

func relocatedWorkspaceMetadata(item Workspace, oldRoot, newRoot string) Workspace {
	next := item
	next.Path = newRoot
	next.Error = ""
	next.AllowDirs = append([]string(nil), item.AllowDirs...)
	for index, allowDir := range next.AllowDirs {
		if relocated, ok := relocateAbsolutePath(allowDir, oldRoot, newRoot); ok {
			next.AllowDirs[index] = relocated
		}
	}
	next.AllowDirs = normalizeRoots(next.AllowDirs)
	next.LegacyIDs = append([]string(nil), item.LegacyIDs...)
	return next
}

func writeRelocationJournal(path string, journal relocationJournal) error {
	data, err := state.MarshalJSON(journal)
	if err != nil {
		return err
	}
	return state.WriteFileAtomic(path, data, 0600)
}

func verifyRelocationFingerprint(root string, expected relocationFingerprint) error {
	actual, err := fingerprintRelocationState(root)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("workspace local state changed during duplicate resolution: %s", boundedWorkspaceDiagnostic(root))
	}
	return nil
}

func fingerprintRelocationState(root string) (relocationFingerprint, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return relocationFingerprint{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return relocationFingerprint{}, fmt.Errorf("workspace local state must be a real directory: %s", boundedWorkspaceDiagnostic(root))
	}
	hasher := sha256.New()
	result := relocationFingerprint{}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.Clean(relative)
		if relative == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace local state contains symlink: %s", boundedWorkspaceDiagnostic(relative))
		}
		entryInfo, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if entryInfo.IsDir() {
			_, _ = io.WriteString(hasher, "d\x00"+filepath.ToSlash(relative)+"\x00")
			return nil
		}
		if !entryInfo.Mode().IsRegular() {
			return fmt.Errorf("workspace local state contains unsupported file type: %s", boundedWorkspaceDiagnostic(relative))
		}
		result.Files++
		result.Bytes += entryInfo.Size()
		if result.Files > relocationMaxFiles || result.Bytes > relocationMaxBytes {
			return errors.New("workspace local state exceeds duplicate relocation staging limits")
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		opened, statErr := file.Stat()
		if statErr != nil || !os.SameFile(entryInfo, opened) {
			_ = file.Close()
			if statErr != nil {
				return statErr
			}
			return fmt.Errorf("workspace local state changed while fingerprinting: %s", boundedWorkspaceDiagnostic(relative))
		}
		_, _ = io.WriteString(hasher, "f\x00"+filepath.ToSlash(relative)+"\x00"+fmt.Sprintf("%d\x00%d\x00", entryInfo.Mode().Perm(), entryInfo.Size()))
		_, copyErr := io.Copy(hasher, io.LimitReader(file, entryInfo.Size()+1))
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		return nil
	})
	if err != nil {
		return relocationFingerprint{}, err
	}
	result.SHA256 = hex.EncodeToString(hasher.Sum(nil))
	return result, nil
}

func copyRelocationState(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("workspace local state must be a real directory: %s", boundedWorkspaceDiagnostic(source))
	}
	if err := os.MkdirAll(destination, 0700); err != nil {
		return err
	}
	var files int
	var bytes int64
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		relative = filepath.Clean(relative)
		if relative == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace local state contains symlink: %s", boundedWorkspaceDiagnostic(relative))
		}
		sourceInfo, err := os.Lstat(path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if sourceInfo.IsDir() {
			return os.MkdirAll(target, sourceInfo.Mode().Perm())
		}
		if !sourceInfo.Mode().IsRegular() {
			return fmt.Errorf("workspace local state contains unsupported file type: %s", boundedWorkspaceDiagnostic(relative))
		}
		files++
		bytes += sourceInfo.Size()
		if files > relocationMaxFiles || bytes > relocationMaxBytes {
			return errors.New("workspace local state exceeds duplicate relocation staging limits")
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		opened, statErr := input.Stat()
		if statErr != nil || !os.SameFile(sourceInfo, opened) {
			_ = input.Close()
			if statErr != nil {
				return statErr
			}
			return fmt.Errorf("workspace local state changed while staging: %s", boundedWorkspaceDiagnostic(relative))
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			_ = input.Close()
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, sourceInfo.Mode().Perm())
		if err != nil {
			_ = input.Close()
			return err
		}
		written, copyErr := io.Copy(output, io.LimitReader(input, sourceInfo.Size()+1))
		syncErr := output.Sync()
		closeOutErr := output.Close()
		closeInErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		if written != sourceInfo.Size() {
			return fmt.Errorf("workspace local state changed while staging: %s", boundedWorkspaceDiagnostic(relative))
		}
		return errors.Join(syncErr, closeOutErr, closeInErr)
	})
}

func replaceRelocationState(sourceBackup, destination string) error {
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	return copyRelocationState(sourceBackup, destination)
}

func restoreRelocationState(backup, destination string) error {
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	return copyRelocationState(backup, destination)
}
