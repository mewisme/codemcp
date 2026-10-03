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
	Phase             Phase  `json:"phase"`
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
