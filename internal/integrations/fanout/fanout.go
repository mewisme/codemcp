package fanout

import (
	"errors"
	"strings"
	"sync"

	"go.mewis.me/codemcp/internal/instructioncontext"
)

type Mode string

const (
	Off          Mode = "off"
	Auto         Mode = "auto"
	Conservative Mode = "conservative"
	Aggressive   Mode = "aggressive"
)

type State struct {
	Mode             Mode
	InstructionsSent bool
}

type Result struct {
	Available          bool   `json:"available"`
	Mode               Mode   `json:"mode"`
	Active             bool   `json:"active"`
	ActiveInstructions string `json:"active_instructions,omitempty"`
	RefreshHint        string `json:"refresh_hint,omitempty"`
}

type stateKey struct {
	Controller string
	Workspace  string
}

type Manager struct {
	mu            sync.Mutex
	defaultActive bool
	defaultMode   Mode
	states        map[stateKey]State
}

func NewManager(defaultActive bool, defaultMode ...Mode) *Manager {
	mode := Auto
	if len(defaultMode) > 0 {
		if value, ok := NormalizeRuntimeMode(string(defaultMode[0])); ok {
			mode = value
		}
	}
	return &Manager{defaultActive: defaultActive, defaultMode: mode, states: map[stateKey]State{}}
}

func (m *Manager) SetDefaults(active bool, mode Mode) {
	if value, ok := NormalizeRuntimeMode(string(mode)); ok {
		mode = value
	} else {
		mode = Auto
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.defaultActive == active && m.defaultMode == mode {
		return
	}
	m.defaultActive = active
	m.defaultMode = mode
	m.states = map[stateKey]State{}
}

func (m *Manager) Turn(controllerID, workspaceID, prompt, action string) (Result, error) {
	controllerID = strings.TrimSpace(controllerID)
	workspaceID = strings.TrimSpace(workspaceID)
	if controllerID == "" {
		return Result{}, errors.New("controller id is required")
	}
	if workspaceID == "" {
		return Result{}, errors.New("workspace id is required")
	}
	if strings.TrimSpace(prompt) == "" {
		return Result{}, errors.New("prompt is required")
	}
	if action != "turn" && action != "refresh" && action != "status" {
		return Result{}, errors.New("action must be turn, refresh, or status")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	key := stateKey{Controller: controllerID, Workspace: workspaceID}
	state, exists := m.states[key]
	if !exists {
		state.Mode = Off
		if m.defaultActive {
			state.Mode = m.defaultMode
		}
	}
	if requested, ok, err := RequestedMode(prompt, m.defaultMode); err != nil {
		return Result{}, err
	} else if ok {
		state.Mode = requested
		state.InstructionsSent = false
	}
	result := Result{Available: true, Mode: state.Mode, Active: state.Mode != Off}
	if result.Active {
		if action == "refresh" || !state.InstructionsSent {
			result.ActiveInstructions = Instructions(state.Mode)
			state.InstructionsSent = true
		} else {
			result.RefreshHint = "Use action refresh if earlier Fanout instructions are no longer in context."
		}
	}
	m.states[key] = state
	return result, nil
}

func RequestedMode(prompt string, defaultMode Mode) (Mode, bool, error) {
	if value, ok := NormalizeRuntimeMode(string(defaultMode)); ok {
		defaultMode = value
	} else {
		defaultMode = Auto
	}
	directive, err := instructioncontext.ResolveSlashDirective(prompt, nil)
	if err != nil {
		return "", false, err
	}
	if directive.Kind == instructioncontext.SlashDirectivePlanMode || directive.FanoutMode == "" {
		return "", false, nil
	}
	if directive.FanoutMode == "default" {
		return defaultMode, true, nil
	}
	mode, ok := NormalizeMode(directive.FanoutMode)
	if !ok {
		return "", false, errors.New("invalid fanout mode")
	}
	return mode, true, nil
}

func NormalizeRuntimeMode(value string) (Mode, bool) {
	switch Mode(strings.ToLower(strings.TrimSpace(value))) {
	case Auto:
		return Auto, true
	case Conservative:
		return Conservative, true
	case Aggressive:
		return Aggressive, true
	default:
		return "", false
	}
}

func NormalizeMode(value string) (Mode, bool) {
	if mode, ok := NormalizeRuntimeMode(value); ok {
		return mode, true
	}
	if Mode(strings.ToLower(strings.TrimSpace(value))) == Off {
		return Off, true
	}
	return "", false
}
