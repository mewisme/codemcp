package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

type RegisteredRootState string

const (
	RegisteredRootMissing           RegisteredRootState = "root_missing"
	RegisteredLocalRootAbsent       RegisteredRootState = "local_root_absent"
	RegisteredLocalValidSameID      RegisteredRootState = "valid_same_id"
	RegisteredLocalValidDifferentID RegisteredRootState = "valid_different_id"
	RegisteredLocalCorruptOrUnowned RegisteredRootState = "corrupt_or_unowned"
	RegisteredLocalSymlinkOrUnsafe  RegisteredRootState = "symlink_or_unsafe"
)

type RegisteredRootClassification struct {
	State       RegisteredRootState
	WorkspaceID string
	Root        string
	LocalID     string
	Cause       error
}

var ErrDuplicateWorkspaceIdentity = errors.New("duplicate workspace identity")

type DuplicateWorkspaceIdentityError struct {
	WorkspaceID     string
	RegisteredRoot  string
	DestinationRoot string
}

func (e *DuplicateWorkspaceIdentityError) Error() string {
	if e == nil {
		return ErrDuplicateWorkspaceIdentity.Error()
	}
	return fmt.Sprintf(
		"%s: %s is present at both registered root %s and destination %s; rerun relocation with --resolve destination, --resolve registered, or --resolve merge",
		ErrDuplicateWorkspaceIdentity,
		e.WorkspaceID,
		boundedWorkspaceDiagnostic(e.RegisteredRoot),
		boundedWorkspaceDiagnostic(e.DestinationRoot),
	)
}

func (e *DuplicateWorkspaceIdentityError) Unwrap() error {
	return ErrDuplicateWorkspaceIdentity
}

var ErrWorkspaceReconnectConflict = errors.New("workspace reconnect conflict")

type WorkspaceReconnectConflictError struct {
	WorkspaceID     string
	RegisteredRoot  string
	DestinationRoot string
	State           RegisteredRootState
	LocalID         string
	Cause           error
}

func (e *WorkspaceReconnectConflictError) Error() string {
	if e == nil {
		return ErrWorkspaceReconnectConflict.Error()
	}
	detail := string(e.State)
	if e.LocalID != "" {
		detail += " local_id=" + e.LocalID
	}
	return fmt.Sprintf(
		"%s: workspace %s registered root %s is %s; refusing automatic rebind to %s",
		ErrWorkspaceReconnectConflict,
		e.WorkspaceID,
		boundedWorkspaceDiagnostic(e.RegisteredRoot),
		detail,
		boundedWorkspaceDiagnostic(e.DestinationRoot),
	)
}

func (e *WorkspaceReconnectConflictError) Unwrap() error {
	return ErrWorkspaceReconnectConflict
}

func (m *Manager) classifyRegisteredRoot(item Workspace) RegisteredRootClassification {
	result := RegisteredRootClassification{
		WorkspaceID: strings.TrimSpace(item.ID),
		Root:        filepath.Clean(item.Path),
	}
	if m == nil {
		result.State = RegisteredLocalSymlinkOrUnsafe
		result.Cause = errors.New("workspace manager is unavailable")
		return result
	}
	if m.protected(result.Root) || m.workspaceLocalRootAliasesProtected(result.Root) {
		result.State = RegisteredLocalSymlinkOrUnsafe
		result.Cause = errors.New("registered root aliases protected control-plane state")
		return result
	}

	absolute, err := filepath.Abs(result.Root)
	if err != nil {
		result.State = RegisteredLocalSymlinkOrUnsafe
		result.Cause = err
		return result
	}
	result.Root = filepath.Clean(absolute)
	rootInfo, err := os.Lstat(result.Root)
	if errors.Is(err, os.ErrNotExist) {
		result.State = RegisteredRootMissing
		return result
	}
	if err != nil {
		result.State = RegisteredLocalSymlinkOrUnsafe
		result.Cause = err
		return result
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		result.State = RegisteredLocalSymlinkOrUnsafe
		result.Cause = errors.New("registered root is not a stable directory")
		return result
	}
	canonical, err := filepath.EvalSymlinks(result.Root)
	if err != nil || !sameCanonicalRoot(canonical, result.Root) {
		result.State = RegisteredLocalSymlinkOrUnsafe
		if err != nil {
			result.Cause = err
		} else {
			result.Cause = errors.New("registered root resolves to a different path")
		}
		return result
	}

	localRoot := workspacestate.New(result.Root).Root()
	localInfo, err := os.Lstat(localRoot)
	if errors.Is(err, os.ErrNotExist) {
		result.State = RegisteredLocalRootAbsent
		return result
	}
	if err != nil {
		result.State = RegisteredLocalSymlinkOrUnsafe
		result.Cause = err
		return result
	}
	if !localInfo.IsDir() || localInfo.Mode()&os.ModeSymlink != 0 {
		result.State = RegisteredLocalSymlinkOrUnsafe
		result.Cause = errors.New("registered workspace local root is not a stable directory")
		return result
	}

	identityPath := workspacestate.New(result.Root).IdentityPath()
	identityInfo, err := os.Lstat(identityPath)
	if err == nil && (!identityInfo.Mode().IsRegular() || identityInfo.Mode()&os.ModeSymlink != 0) {
		result.State = RegisteredLocalSymlinkOrUnsafe
		result.Cause = errors.New("registered workspace identity marker is not a stable regular file")
		return result
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		result.State = RegisteredLocalSymlinkOrUnsafe
		result.Cause = err
		return result
	}

	identity, loadErr := workspacestate.New(result.Root).LoadIdentity()
	if loadErr != nil {
		result.State = RegisteredLocalCorruptOrUnowned
		result.Cause = loadErr
		return result
	}
	result.LocalID = identity.ID
	if identity.ID == result.WorkspaceID {
		result.State = RegisteredLocalValidSameID
	} else {
		result.State = RegisteredLocalValidDifferentID
	}
	return result
}

func reconnectClassificationError(classification RegisteredRootClassification, destination string) error {
	switch classification.State {
	case RegisteredRootMissing, RegisteredLocalRootAbsent:
		return nil
	case RegisteredLocalValidSameID:
		return &DuplicateWorkspaceIdentityError{
			WorkspaceID:     classification.WorkspaceID,
			RegisteredRoot:  classification.Root,
			DestinationRoot: destination,
		}
	default:
		return &WorkspaceReconnectConflictError{
			WorkspaceID:     classification.WorkspaceID,
			RegisteredRoot:  classification.Root,
			DestinationRoot: destination,
			State:           classification.State,
			LocalID:         classification.LocalID,
			Cause:           classification.Cause,
		}
	}
}

func boundedWorkspaceDiagnostic(value string) string {
	value = filepath.Clean(strings.TrimSpace(value))
	const max = 512
	if len(value) <= max {
		return value
	}
	return "..." + value[len(value)-(max-3):]
}
