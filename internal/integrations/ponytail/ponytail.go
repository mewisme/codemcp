package ponytail

import (
	"errors"
	"strings"
	"sync"
)

type Mode string

const (
	Off    Mode = "off"
	Lite   Mode = "lite"
	Full   Mode = "full"
	Ultra  Mode = "ultra"
	Review Mode = "review"
)

type State struct {
	Mode             Mode `json:"mode"`
	InstructionsSent bool `json:"-"`
}

type Result struct {
	Available          bool   `json:"available"`
	Mode               Mode   `json:"mode"`
	Active             bool   `json:"active"`
	ActiveInstructions string `json:"active_instructions,omitempty"`
	RefreshHint        string `json:"refresh_hint,omitempty"`
}

type Manager struct {
	mu            sync.Mutex
	defaultActive bool
	defaultMode   Mode
	states        map[string]State
}

func NewManager(defaultActive bool, defaultMode ...Mode) *Manager {
	mode := Full
	if len(defaultMode) > 0 {
		if value, ok := NormalizeRuntimeMode(string(defaultMode[0])); ok {
			mode = value
		}
	}
	return &Manager{defaultActive: defaultActive, defaultMode: mode, states: map[string]State{}}
}

func (m *Manager) SetDefaults(active bool, mode Mode) {
	if value, ok := NormalizeRuntimeMode(string(mode)); ok {
		mode = value
	} else {
		mode = Full
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.defaultActive == active && m.defaultMode == mode {
		return
	}
	m.defaultActive = active
	m.defaultMode = mode
	m.states = map[string]State{}
}

func (m *Manager) Turn(workspaceID, prompt, action string) (Result, error) {
	if strings.TrimSpace(workspaceID) == "" {
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
	state, exists := m.states[workspaceID]
	if !exists {
		state.Mode = Off
		if m.defaultActive {
			state.Mode = m.defaultMode
		}
	}
	if requested, ok := RequestedMode(prompt); ok {
		state.Mode = requested
		state.InstructionsSent = false
	}
	result := Result{Available: true, Mode: state.Mode, Active: state.Mode != Off}
	if result.Active {
		if action == "refresh" || !state.InstructionsSent {
			result.ActiveInstructions = Instructions(state.Mode)
			state.InstructionsSent = true
		} else {
			result.RefreshHint = "Use action refresh if earlier Ponytail instructions are no longer in context."
		}
	}
	m.states[workspaceID] = state
	return result, nil
}

func RequestedMode(prompt string) (Mode, bool) {
	value := strings.TrimSpace(strings.ToLower(prompt))
	standalone := strings.TrimRight(value, ".!? \t\r\n")
	if standalone == "stop ponytail" || standalone == "normal mode" {
		return Off, true
	}
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return "", false
	}
	switch fields[0] {
	case "/ponytail-review", "@ponytail-review", "$ponytail-review", "/ponytail:ponytail-review", "@ponytail:ponytail-review", "$ponytail:ponytail-review":
		return Review, true
	case "/ponytail", "@ponytail", "$ponytail", "/ponytail:ponytail", "@ponytail:ponytail", "$ponytail:ponytail":
		if len(fields) < 2 {
			return "", false
		}
		if fields[1] == "off" {
			return Off, true
		}
		return NormalizeRuntimeMode(fields[1])
	default:
		return "", false
	}
}

func NormalizeRuntimeMode(value string) (Mode, bool) {
	switch Mode(strings.ToLower(strings.TrimSpace(value))) {
	case Lite:
		return Lite, true
	case Full:
		return Full, true
	case Ultra:
		return Ultra, true
	default:
		return "", false
	}
}

func NormalizeMode(value string) (Mode, bool) {
	if mode, ok := NormalizeRuntimeMode(value); ok {
		return mode, true
	}
	switch Mode(strings.ToLower(strings.TrimSpace(value))) {
	case Off:
		return Off, true
	case Review:
		return Review, true
	default:
		return "", false
	}
}
