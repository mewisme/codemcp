package presentation

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

type ProgressState uint8

const (
	ProgressPending ProgressState = iota
	ProgressRunning
	ProgressSuccess
	ProgressSkipped
	ProgressWarning
	ProgressFailed
)

type ProgressPhase struct {
	ID      string
	Label   string
	State   ProgressState
	Message string
}

type ProgressSession struct {
	mu           sync.Mutex
	out          io.Writer
	mode         ResultMode
	capabilities Capabilities
	theme        Theme
	glyphs       GlyphSet
	phases       map[string]ProgressPhase
	activeID     string
	transient    bool
	begun        bool
	gap          bool
	framed       bool
	closed       bool
}

func NewProgressSession(out io.Writer, mode ResultMode, capabilities Capabilities) *ProgressSession {
	if out == nil {
		out = io.Discard
	}
	return &ProgressSession{
		out: out, mode: mode, capabilities: capabilities,
		theme: NewTheme(capabilities), glyphs: Glyphs(capabilities),
		phases: map[string]ProgressPhase{},
	}
}

func (session *ProgressSession) Begin(title string) {
	if session == nil {
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed || session.mode == ModeJSON || strings.TrimSpace(title) == "" {
		return
	}
	session.begun = true
	if session.mode == ModePlain {
		fmt.Fprintln(session.out, strings.TrimSpace(title))
		fmt.Fprintln(session.out)
		session.gap = true
		return
	}
	fmt.Fprintln(session.out, session.theme.Render(RoleRail, session.glyphs.FrameStart)+"  "+session.theme.Render(RoleHeading, strings.TrimSpace(title)))
	fmt.Fprintln(session.out, session.theme.Render(RoleRail, session.glyphs.Rail))
	session.gap = true
	session.framed = true
}

func (session *ProgressSession) Update(phase ProgressPhase) bool {
	if session == nil {
		return false
	}
	phase.ID = strings.TrimSpace(phase.ID)
	phase.Label = strings.TrimSpace(phase.Label)
	phase.Message = strings.TrimSpace(phase.Message)
	if phase.ID == "" {
		phase.ID = phase.Label
	}
	if phase.Label == "" {
		phase.Label = phase.ID
	}
	if phase.ID == "" {
		return false
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return false
	}
	if previous, ok := session.phases[phase.ID]; ok && previous == phase {
		return false
	}
	if phase.State == ProgressRunning && session.activeID != "" && session.activeID != phase.ID {
		session.clearTransientLocked()
		session.activeID = ""
	}
	session.phases[phase.ID] = phase

	switch phase.State {
	case ProgressPending:
		return true
	case ProgressRunning:
		session.activeID = phase.ID
		session.renderRunningLocked(phase)
	default:
		if session.activeID == phase.ID {
			session.clearTransientLocked()
			session.activeID = ""
		}
		session.renderTerminalLocked(phase)
	}
	return true
}

func (session *ProgressSession) Success(id, label, message string) bool {
	return session.Update(ProgressPhase{ID: id, Label: label, State: ProgressSuccess, Message: message})
}

func (session *ProgressSession) Skip(id, label, message string) bool {
	return session.Update(ProgressPhase{ID: id, Label: label, State: ProgressSkipped, Message: message})
}

func (session *ProgressSession) Warn(id, label, message string) bool {
	return session.Update(ProgressPhase{ID: id, Label: label, State: ProgressWarning, Message: message})
}

func (session *ProgressSession) Fail(id, label, message string) bool {
	return session.Update(ProgressPhase{ID: id, Label: label, State: ProgressFailed, Message: message})
}

func (session *ProgressSession) FailActive(message string) bool {
	if session == nil {
		return false
	}
	session.mu.Lock()
	id := session.activeID
	phase := session.phases[id]
	session.mu.Unlock()
	if id == "" {
		return false
	}
	return session.Fail(id, phase.Label, message)
}

func (session *ProgressSession) Suspend() {
	if session == nil {
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	session.clearTransientLocked()
}

func (session *ProgressSession) Append(render func(*Presenter)) {
	if session == nil || render == nil {
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return
	}
	session.clearTransientLocked()
	session.gapLocked()
	render(New(session.out, session.mode, session.capabilities))
	session.gap = false
}

func (session *ProgressSession) Begun() bool {
	if session == nil {
		return false
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.begun && !session.closed
}

func (session *ProgressSession) Close() {
	session.CloseWith("")
}

func (session *ProgressSession) CloseWith(message string) {
	if session == nil {
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return
	}
	session.clearTransientLocked()
	if session.framed && session.mode == ModeHuman {
		session.gapLocked()
		line := session.theme.Render(RoleRail, session.glyphs.FrameEnd)
		if message = strings.TrimSpace(message); message != "" {
			line += "  " + message
		}
		fmt.Fprintln(session.out, line)
	} else if session.begun && session.mode == ModePlain {
		session.gapLocked()
		if message = strings.TrimSpace(message); message != "" {
			fmt.Fprintln(session.out, message)
		}
	}
	session.closed = true
}

func (session *ProgressSession) renderRunningLocked(phase ProgressPhase) {
	if session.mode != ModeHuman || !session.capabilities.Animation {
		return
	}
	session.clearTransientLocked()
	fmt.Fprint(session.out, "\r\x1b[2K", session.theme.Render(RoleActive, session.glyphs.Active), "  ", phase.Label)
	session.transient = true
}

func (session *ProgressSession) renderTerminalLocked(phase ProgressPhase) {
	if session.mode == ModeJSON {
		return
	}
	if session.mode == ModePlain {
		fmt.Fprintln(session.out, plainProgressLine(phase))
		session.gap = false
		return
	}
	glyph, role := session.terminalStyle(phase.State)
	label := phase.Label
	if phase.State == ProgressSuccess && phase.Message != "" {
		label = phase.Message
	}
	line := session.theme.Render(role, glyph) + "  " + label
	if phase.State != ProgressSuccess && phase.Message != "" && !strings.EqualFold(phase.Message, phase.Label) {
		line += " — " + phase.Message
	}
	fmt.Fprintln(session.out, line)
	session.gap = false
}

func (session *ProgressSession) gapLocked() {
	if session.closed || session.gap || !session.begun {
		return
	}
	if session.mode == ModeHuman && session.framed {
		fmt.Fprintln(session.out, session.theme.Render(RoleRail, session.glyphs.Rail))
	} else if session.mode == ModePlain {
		fmt.Fprintln(session.out)
	}
	session.gap = true
}

func (session *ProgressSession) clearTransientLocked() {
	if !session.transient {
		return
	}
	if session.capabilities.CursorControl {
		fmt.Fprint(session.out, "\r\x1b[2K")
	}
	session.transient = false
}

func (session *ProgressSession) terminalStyle(state ProgressState) (string, Role) {
	switch state {
	case ProgressSuccess:
		return session.glyphs.PhaseDone, RoleSuccess
	case ProgressSkipped:
		return session.glyphs.PhasePending, RoleMuted
	case ProgressWarning:
		return session.glyphs.Warning, RoleWarning
	case ProgressFailed:
		return session.glyphs.Error, RoleDanger
	default:
		return session.glyphs.Info, RoleMuted
	}
}

func plainProgressLine(phase ProgressPhase) string {
	label := strings.TrimSpace(phase.Label)
	switch phase.State {
	case ProgressSuccess:
		if phase.Message != "" && !strings.EqualFold(phase.Message, label) {
			return phase.Message
		}
		return label + "... done"
	case ProgressSkipped:
		if phase.Message != "" {
			return phase.Message
		}
		return label + "... skipped"
	case ProgressWarning:
		if phase.Message != "" {
			return label + "... warning: " + phase.Message
		}
		return label + "... warning"
	case ProgressFailed:
		if phase.Message != "" {
			return label + "... failed: " + phase.Message
		}
		return label + "... failed"
	default:
		return label
	}
}
