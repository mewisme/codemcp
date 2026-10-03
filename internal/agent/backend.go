package agent

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const MaxBackendIDBytes = 64

var (
	ErrBackendNotFound    = errors.New("managed agent backend not found")
	ErrBackendUnavailable = errors.New("managed agent backend unavailable")
	ErrCapacityReached    = errors.New("managed agent capacity reached")
	backendIDPattern      = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?$`)
)

type BackendID string

func NormalizeBackendID(raw string) (BackendID, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" || len(value) > MaxBackendIDBytes || !backendIDPattern.MatchString(value) {
		return "", errors.New("backend id must use 1-64 lowercase letters, digits, '.', '_' or '-' and start/end with a letter or digit")
	}
	return BackendID(value), nil
}

type Capacity struct {
	MaxParallel int `json:"max_parallel"`
}

func (capacity Capacity) Validate() error {
	if capacity.MaxParallel < 1 || capacity.MaxParallel > MaxParallelLimit {
		return fmt.Errorf("max_parallel must be between 1 and %d", MaxParallelLimit)
	}
	return nil
}

func EffectiveCapacity(global, backend Capacity) (Capacity, error) {
	if err := global.Validate(); err != nil {
		return Capacity{}, fmt.Errorf("global capacity: %w", err)
	}
	if err := backend.Validate(); err != nil {
		return Capacity{}, fmt.Errorf("backend capacity: %w", err)
	}
	return Capacity{MaxParallel: min(global.MaxParallel, backend.MaxParallel)}, nil
}

type Readiness struct {
	Available bool     `json:"available"`
	Reason    string   `json:"reason,omitempty"`
	Capacity  Capacity `json:"capacity,omitempty"`
}

func (readiness Readiness) Validate() error {
	if readiness.Available {
		if err := readiness.Capacity.Validate(); err != nil {
			return err
		}
	}
	if len(readiness.Reason) > MaxErrorBytes {
		return fmt.Errorf("backend readiness reason exceeds %d-byte limit", MaxErrorBytes)
	}
	return nil
}

type Handle any

type BackendPhase string

const (
	BackendPhaseWorking BackendPhase = "working"
	BackendPhaseIdle    BackendPhase = "idle"
	BackendPhaseFailed  BackendPhase = "failed"
)

func (phase BackendPhase) Valid() bool {
	return phase == BackendPhaseWorking || phase == BackendPhaseIdle || phase == BackendPhaseFailed
}

type BackendSnapshot struct {
	Phase  BackendPhase `json:"phase"`
	Result string       `json:"result,omitempty"`
	Error  string       `json:"error,omitempty"`
}

func NormalizeBackendSnapshot(snapshot BackendSnapshot) (BackendSnapshot, error) {
	if !snapshot.Phase.Valid() {
		return BackendSnapshot{}, fmt.Errorf("invalid backend phase %q", snapshot.Phase)
	}
	snapshot.Result = BoundResult(snapshot.Result)
	snapshot.Error = BoundErrorText(snapshot.Error)
	if snapshot.Phase == BackendPhaseFailed && snapshot.Error == "" {
		return BackendSnapshot{}, errors.New("failed backend snapshot requires error")
	}
	return snapshot, nil
}

type BackendSpawnRequest struct {
	AgentID         ID
	WorkspaceID     string
	Prompt          string
	Model           string
	ReasoningEffort string
	ParentID        ID
	Depth           int
}

func ValidateBackendSpawnRequest(request BackendSpawnRequest) error {
	if err := ValidateID(request.AgentID); err != nil {
		return err
	}
	if _, err := NormalizeSpawnInput(SpawnInput{
		WorkspaceID: request.WorkspaceID, Prompt: request.Prompt,
		Model: request.Model, ReasoningEffort: request.ReasoningEffort,
	}); err != nil {
		return err
	}
	return ValidateDelegation(request.ParentID, request.Depth)
}

type Message struct {
	Content string `json:"content"`
}

func (message Message) Validate() error {
	return validateRequiredText("message", message.Content, MaxPromptBytes)
}

type Backend interface {
	ID() BackendID
	Ready(context.Context) (Readiness, error)
	Spawn(context.Context, BackendSpawnRequest) (Handle, error)
	Send(context.Context, Handle, Message) error
	Snapshot(context.Context, Handle) (BackendSnapshot, error)
	Cancel(context.Context, Handle) error
	Close(context.Context, Handle) error
}

func ResolveBackend(ctx context.Context, backends map[BackendID]Backend, requested, defaultID BackendID) (Backend, Readiness, error) {
	id := requested
	if id == "" {
		id = defaultID
	}
	normalized, err := NormalizeBackendID(string(id))
	if err != nil {
		return nil, Readiness{}, err
	}
	backend := backends[normalized]
	if backend == nil {
		return nil, Readiness{}, fmt.Errorf("%w: %s", ErrBackendNotFound, normalized)
	}
	backendID, err := NormalizeBackendID(string(backend.ID()))
	if err != nil || backendID != normalized {
		return nil, Readiness{}, fmt.Errorf("registered backend %q has mismatched identity %q", normalized, backend.ID())
	}
	readiness, err := backend.Ready(ctx)
	if err != nil {
		return nil, Readiness{}, fmt.Errorf("%w: %s: %s", ErrBackendUnavailable, normalized, BoundError(err))
	}
	if err := readiness.Validate(); err != nil {
		return nil, Readiness{}, fmt.Errorf("backend %s readiness: %w", normalized, err)
	}
	if !readiness.Available {
		reason := BoundErrorText(readiness.Reason)
		if reason == "" {
			reason = "not ready"
		}
		return nil, readiness, fmt.Errorf("%w: %s: %s", ErrBackendUnavailable, normalized, reason)
	}
	return backend, readiness, nil
}
