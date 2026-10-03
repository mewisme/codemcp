package plan

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

var ErrExecutionBindingConflict = errors.New("plan execution binding conflict")

type ExecutionBinding struct {
	WorkspaceID       string `json:"workspace_id"`
	PlanName          string `json:"plan_name"`
	BaselineContentID string `json:"baseline_content_id"`
	CompletedPhases   int    `json:"completed_phases"`
	Phase             Phase  `json:"phase"`
	Closed            bool   `json:"closed"`
}

type ExecutionTransition struct {
	binding       ExecutionBinding
	nextContentID string
	nextCompleted int
	closes        bool
}

type ExecutionManager struct {
	mu       sync.Mutex
	bindings map[string]ExecutionBinding
}

func NewExecutionManager() *ExecutionManager {
	return &ExecutionManager{bindings: map[string]ExecutionBinding{}}
}

func (m *ExecutionManager) Bind(sessionKey string, binding ExecutionBinding) (ExecutionBinding, error) {
	if m == nil {
		return ExecutionBinding{}, errors.New("plan execution manager is unavailable")
	}
	sessionKey = strings.TrimSpace(sessionKey)
	binding = normalizeExecutionBinding(binding)
	if sessionKey == "" {
		return ExecutionBinding{}, errors.New("plan execution session is required")
	}
	if err := validateExecutionBinding(binding); err != nil {
		return ExecutionBinding{}, err
	}
	key := executionBindingKey(sessionKey, binding.WorkspaceID)
	m.mu.Lock()
	defer m.mu.Unlock()
	if current, ok := m.bindings[key]; ok {
		if current == binding {
			return current, nil
		}
		if !current.Closed &&
			current.WorkspaceID == binding.WorkspaceID &&
			current.PlanName == binding.PlanName &&
			current.CompletedPhases == binding.CompletedPhases &&
			current.Phase == binding.Phase {
			current.BaselineContentID = binding.BaselineContentID
			m.bindings[key] = current
			return current, nil
		}
		return ExecutionBinding{}, fmt.Errorf("%w: session is already bound to plan %q phase %s", ErrExecutionBindingConflict, current.PlanName, current.Phase.ID)
	}
	m.bindings[key] = binding
	return binding, nil
}

func (m *ExecutionManager) Lookup(sessionKey, workspaceID string) (ExecutionBinding, bool) {
	if m == nil {
		return ExecutionBinding{}, false
	}
	key := executionBindingKey(strings.TrimSpace(sessionKey), strings.TrimSpace(workspaceID))
	if key == "" {
		return ExecutionBinding{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	binding, ok := m.bindings[key]
	return binding, ok
}

func (m *ExecutionManager) Release(sessionKey, workspaceID string) bool {
	if m == nil {
		return false
	}
	key := executionBindingKey(strings.TrimSpace(sessionKey), strings.TrimSpace(workspaceID))
	if key == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.bindings[key]; !ok {
		return false
	}
	delete(m.bindings, key)
	return true
}

func (m *ExecutionManager) PrepareUpdate(sessionKey, workspaceID, planName, expectedContentID string, next Document) (ExecutionTransition, error) {
	if m == nil {
		return ExecutionTransition{}, errors.New("plan execution manager is unavailable")
	}
	sessionKey = strings.TrimSpace(sessionKey)
	workspaceID = strings.TrimSpace(workspaceID)
	planName = strings.TrimSpace(planName)
	expectedContentID = strings.TrimSpace(expectedContentID)
	key := executionBindingKey(sessionKey, workspaceID)
	if key == "" {
		return ExecutionTransition{}, errors.New("plan execution session and workspace are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	binding, ok := m.bindings[key]
	if !ok {
		return ExecutionTransition{}, errors.New("plan execution binding is unavailable")
	}
	if binding.Closed {
		return ExecutionTransition{}, fmt.Errorf("%w: plan %q phase %s is already closed for this session", ErrExecutionBindingConflict, binding.PlanName, binding.Phase.ID)
	}
	if binding.PlanName != planName {
		return ExecutionTransition{}, fmt.Errorf("%w: session is bound to plan %q phase %s", ErrExecutionBindingConflict, binding.PlanName, binding.Phase.ID)
	}
	if binding.BaselineContentID != expectedContentID {
		return ExecutionTransition{}, fmt.Errorf("%w: expected content %q does not match bound baseline %q", ErrExecutionBindingConflict, expectedContentID, binding.BaselineContentID)
	}
	phases := next.Phases()
	if binding.CompletedPhases < 0 || binding.CompletedPhases >= len(phases) {
		return ExecutionTransition{}, errors.New("bound plan phase is missing from the updated document")
	}
	bound := phases[binding.CompletedPhases]
	if bound.ID != binding.Phase.ID || bound.Title != binding.Phase.Title {
		return ExecutionTransition{}, fmt.Errorf("bound phase %s cannot be removed, renamed, or reordered", binding.Phase.ID)
	}
	nextCompleted := next.CompletedPhaseCount()
	if nextCompleted < binding.CompletedPhases {
		return ExecutionTransition{}, errors.New("completed plan progress cannot regress behind the bound phase")
	}
	if nextCompleted > binding.CompletedPhases+1 {
		return ExecutionTransition{}, fmt.Errorf("plan execution may complete only bound phase %s in this session", binding.Phase.ID)
	}
	closes := nextCompleted == binding.CompletedPhases+1
	if closes && !bound.Completed {
		return ExecutionTransition{}, fmt.Errorf("bound phase %s must be completed when plan progress advances", binding.Phase.ID)
	}
	if !closes && bound.Completed {
		return ExecutionTransition{}, fmt.Errorf("bound phase %s completion state is inconsistent with plan progress", binding.Phase.ID)
	}
	return ExecutionTransition{binding: binding, nextContentID: next.ContentID(), nextCompleted: nextCompleted, closes: closes}, nil
}

func (m *ExecutionManager) CommitUpdate(sessionKey string, transition ExecutionTransition) error {
	if m == nil {
		return errors.New("plan execution manager is unavailable")
	}
	sessionKey = strings.TrimSpace(sessionKey)
	key := executionBindingKey(sessionKey, transition.binding.WorkspaceID)
	if key == "" {
		return errors.New("plan execution session is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.bindings[key]
	if !ok || current != transition.binding {
		return fmt.Errorf("%w: plan execution binding changed before update commit", ErrExecutionBindingConflict)
	}
	current.BaselineContentID = transition.nextContentID
	current.CompletedPhases = transition.nextCompleted
	current.Closed = transition.closes
	m.bindings[key] = current
	return nil
}

func normalizeExecutionBinding(binding ExecutionBinding) ExecutionBinding {
	binding.WorkspaceID = strings.TrimSpace(binding.WorkspaceID)
	binding.PlanName = strings.TrimSpace(binding.PlanName)
	binding.BaselineContentID = strings.TrimSpace(binding.BaselineContentID)
	binding.Phase.ID = strings.TrimSpace(binding.Phase.ID)
	binding.Phase.Title = strings.TrimSpace(binding.Phase.Title)
	return binding
}

func validateExecutionBinding(binding ExecutionBinding) error {
	if binding.WorkspaceID == "" {
		return errors.New("plan execution workspace is required")
	}
	if err := ValidateName(binding.PlanName); err != nil {
		return fmt.Errorf("plan execution name: %w", err)
	}
	if binding.BaselineContentID == "" {
		return errors.New("plan execution baseline content id is required")
	}
	if binding.CompletedPhases < 0 {
		return errors.New("plan execution completed phase count cannot be negative")
	}
	if binding.Closed {
		return errors.New("new plan execution binding cannot already be closed")
	}
	if binding.Phase.Completed {
		return errors.New("plan execution phase must be incomplete")
	}
	if err := validatePhaseIdentity(binding.Phase.ID, binding.Phase.Title, "phase", 0); err != nil {
		return err
	}
	return nil
}

func executionBindingKey(sessionKey, workspaceID string) string {
	if sessionKey == "" || workspaceID == "" {
		return ""
	}
	return sessionKey + "\x00" + workspaceID
}
