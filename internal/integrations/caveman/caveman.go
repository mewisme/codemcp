package caveman

import (
	"errors"
	"regexp"
	"strings"
	"sync"
	"unicode"
)

type Mode string

const (
	Off         Mode = "off"
	Lite        Mode = "lite"
	Full        Mode = "full"
	Ultra       Mode = "ultra"
	WenyanLite  Mode = "wenyan-lite"
	WenyanFull  Mode = "wenyan-full"
	WenyanUltra Mode = "wenyan-ultra"
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

var (
	deactivateCavemanPattern = regexp.MustCompile(`\b(stop|disable|deactivate|quit|exit|kill)\s+(the\s+)?caveman\b`)
	cavemanOffPattern        = regexp.MustCompile(`\bcaveman(\s+mode)?\s+(off|stop|disabled?)\b`)
	turnOffCavemanPattern    = regexp.MustCompile(`\bturn\s+off\s+(the\s+)?caveman\b`)
	normalModeStartPattern   = regexp.MustCompile(`^(please\s+)?(go\s+|back\s+to\s+|switch\s+(back\s+)?to\s+|return\s+to\s+)?normal\s+mode\b`)
	questionPattern          = regexp.MustCompile(`^(what|whats|what's|how|why|when|where|who|does|do|did|is|are|can|could|would|should|tell me|explain)\b`)
	activateCavemanPattern   = regexp.MustCompile(`\b(activate|enable|start|turn on|use|switch to|want|give me)\b[^.]{0,40}\bcaveman\b`)
	talkLikeCavemanPattern   = regexp.MustCompile(`\btalk like\b[^.]{0,40}\bcaveman\b`)
	cavemanModeOnPattern     = regexp.MustCompile(`\bcaveman\s+mode\s+(on|please|now)\b`)
	bareCavemanPattern       = regexp.MustCompile(`^caveman(\s+mode)?\s*[.!]*$`)
)

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
	if requested, ok := RequestedMode(prompt, m.defaultMode); ok {
		state.Mode = requested
		state.InstructionsSent = false
	}
	result := Result{Available: true, Mode: state.Mode, Active: state.Mode != Off}
	if result.Active {
		if action == "refresh" || !state.InstructionsSent {
			result.ActiveInstructions = Instructions(state.Mode)
			state.InstructionsSent = true
		} else {
			result.RefreshHint = "Use action refresh if earlier Caveman instructions are no longer in context."
		}
	}
	m.states[workspaceID] = state
	return result, nil
}

func RequestedMode(prompt string, defaultMode Mode) (Mode, bool) {
	if mode, ok := NormalizeRuntimeMode(string(defaultMode)); ok {
		defaultMode = mode
	} else {
		defaultMode = Full
	}
	value := strings.ToLower(strings.TrimSpace(prompt))
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "", false
	}
	if mode, ok := commandMode(value, defaultMode); ok {
		return mode, true
	}
	if strings.HasPrefix(value, "/") {
		return "", false
	}
	natural := blankQuotedSpans(value)
	if deactivateCavemanPattern.MatchString(natural) || cavemanOffPattern.MatchString(natural) || turnOffCavemanPattern.MatchString(natural) || normalModeStartPattern.MatchString(natural) || strings.Contains(natural, "normal mode") && strings.Contains(natural, "caveman") {
		return Off, true
	}
	if questionPattern.MatchString(natural) {
		return "", false
	}
	if activateCavemanPattern.MatchString(natural) || talkLikeCavemanPattern.MatchString(natural) || cavemanModeOnPattern.MatchString(natural) || bareCavemanPattern.MatchString(natural) || brevityIntent(natural) {
		return defaultMode, true
	}
	return "", false
}

func commandMode(value string, defaultMode Mode) (Mode, bool) {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return "", false
	}
	switch fields[0] {
	case "/caveman", "@caveman", "$caveman", "/caveman:caveman", "@caveman:caveman", "$caveman:caveman":
		if len(fields) == 1 {
			return defaultMode, true
		}
		arg := normalizeModeArg(fields[1])
		switch arg {
		case "off", "stop", "disable":
			return Off, true
		case "wenyan":
			return WenyanFull, true
		}
		return NormalizeRuntimeMode(arg)
	default:
		return "", false
	}
}

func normalizeModeArg(value string) string {
	return strings.TrimFunc(strings.ToLower(value), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' })
}

func NormalizeRuntimeMode(value string) (Mode, bool) {
	switch Mode(strings.ToLower(strings.TrimSpace(value))) {
	case Lite:
		return Lite, true
	case Full:
		return Full, true
	case Ultra:
		return Ultra, true
	case WenyanLite:
		return WenyanLite, true
	case WenyanFull:
		return WenyanFull, true
	case WenyanUltra:
		return WenyanUltra, true
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
	case "wenyan":
		return WenyanFull, true
	default:
		return "", false
	}
}

func blankQuotedSpans(value string) string {
	runes := []rune(value)
	for index := 0; index < len(runes); index++ {
		quote := runes[index]
		if quote != '"' && quote != '`' {
			continue
		}
		end := index + 1
		for end < len(runes) && runes[end] != quote {
			end++
		}
		if end >= len(runes) {
			continue
		}
		for cursor := index; cursor <= end; cursor++ {
			runes[cursor] = ' '
		}
		index = end
	}
	return string(runes)
}

func brevityIntent(value string) bool {
	for _, phrase := range []string{"less tokens", "fewer tokens", "be brief", "be terse", "shorter answers"} {
		index := strings.Index(value, phrase)
		if index < 0 {
			continue
		}
		rest := strings.TrimSpace(value[index+len(phrase):])
		if rest == "" {
			return true
		}
		next := strings.Fields(rest)[0]
		next = strings.Trim(next, ",.;:!?()[]{}")
		switch next {
		case "in", "for", "on", "about", "when", "during", "with":
			continue
		default:
			return true
		}
	}
	return false
}
