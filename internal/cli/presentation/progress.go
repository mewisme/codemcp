package presentation

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/productadapter"
)

const defaultProgressAnimationInterval = 120 * time.Millisecond

type ProgressState uint8

const (
	ProgressPending ProgressState = iota
	ProgressRunning
	ProgressSuccess
	ProgressSkipped
	ProgressWarning
	ProgressFailed
)

func (state ProgressState) CanonicalLifecycle() productadapter.LifecycleState {
	switch state {
	case ProgressPending:
		return productadapter.LifecycleIdle
	case ProgressRunning:
		return productadapter.LifecycleWorking
	case ProgressSuccess:
		return productadapter.LifecycleSuccess
	case ProgressSkipped, ProgressWarning:
		return productadapter.LifecyclePartial
	case ProgressFailed:
		return productadapter.LifecycleTerminalFailure
	default:
		return productadapter.LifecycleUnavailable
	}
}

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
	title        string
	presenter    *Presenter
	phases       map[string]ProgressPhase
	activeID     string
	transient    bool
	provisional  bool
	suspended    bool
	animationSeq uint64
	animationGap time.Duration
	begun        bool
	gap          bool
	framed       bool
	completion   string
	closed       bool
}

func NewProgressSession(out io.Writer, mode ResultMode, capabilities Capabilities) *ProgressSession {
	if out == nil {
		out = io.Discard
	}
	session := &ProgressSession{
		out: out, mode: mode, capabilities: capabilities,
		theme: NewTheme(capabilities), glyphs: Glyphs(capabilities),
		phases:       map[string]ProgressPhase{},
		animationGap: defaultProgressAnimationInterval,
	}
	session.presenter = newSessionPresenter(session)
	return session
}

func (session *ProgressSession) Begin(title string) {
	if session == nil {
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed || session.mode == ModeJSON {
		return
	}
	title = strings.TrimSpace(title)
	if title != "" && !session.begun {
		session.title = title
	}
	session.beginLocked()
}

func (session *ProgressSession) SetTitle(title string) {
	if session == nil {
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed || session.begun {
		return
	}
	session.title = strings.TrimSpace(title)
}

func (session *ProgressSession) Presenter() *Presenter {
	if session == nil {
		return New(io.Discard, ModeJSON, Capabilities{})
	}
	return session.presenter
}

func (session *ProgressSession) EnsureBegun() {
	if session == nil {
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	session.beginLocked()
}

func (session *ProgressSession) beginLocked() {
	if session.closed || session.begun || session.mode == ModeJSON {
		return
	}
	title := strings.TrimSpace(session.title)
	if title == "" {
		return
	}
	session.begun = true
	if session.mode == ModePlain {
		fmt.Fprintln(session.out, title)
		fmt.Fprintln(session.out)
		session.gap = true
		return
	}
	fmt.Fprintln(session.out, session.theme.Render(RoleRail, session.glyphs.FrameStart)+"  "+session.theme.Render(RoleHeading, title))
	fmt.Fprintln(session.out, session.theme.Render(RoleRail, session.glyphs.Rail))
	session.gap = true
	session.framed = true
	session.renderProvisionalLocked()
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
	session.beginLocked()
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

// Checkpoint renders a terminal progress event without replacing the active
// running phase. This is used by readiness polling: completed components are
// committed to the log while the readiness spinner immediately resumes.
func (session *ProgressSession) Checkpoint(phase ProgressPhase) bool {
	if session == nil || phase.State == ProgressPending || phase.State == ProgressRunning {
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
	session.beginLocked()
	if previous, ok := session.phases[phase.ID]; ok && previous == phase {
		return false
	}
	activeID := session.activeID
	active, hasActive := session.phases[activeID]
	session.clearTransientLocked()
	session.phases[phase.ID] = phase
	session.renderTerminalLocked(phase)
	if hasActive && active.State == ProgressRunning && activeID != phase.ID {
		session.activeID = activeID
		session.renderRunningLocked(active)
	}
	return true
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
	session.clearProvisionalLocked()
	session.suspended = true
}

func (session *ProgressSession) Resume() {
	if session == nil {
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	session.suspended = false
	if session.closed || session.activeID == "" {
		session.renderProvisionalLocked()
		return
	}
	phase, ok := session.phases[session.activeID]
	if !ok || phase.State != ProgressRunning {
		session.renderProvisionalLocked()
		return
	}
	session.renderRunningLocked(phase)
}

func (session *ProgressSession) WithInput(render func(*Presenter), read func() error) error {
	if session == nil {
		if read != nil {
			return read()
		}
		return nil
	}
	session.Suspend()
	defer session.Resume()
	if render != nil {
		render(session.Presenter())
	}
	if read != nil {
		return read()
	}
	return nil
}

func (session *ProgressSession) Append(render func(*Presenter)) {
	if session == nil || render == nil {
		return
	}
	session.mu.Lock()
	if session.closed {
		session.mu.Unlock()
		return
	}
	session.beginLocked()
	session.clearTransientLocked()
	session.gapLocked()
	session.mu.Unlock()
	render(session.presenter)
}

func (session *ProgressSession) Begun() bool {
	if session == nil {
		return false
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.begun && !session.closed
}

func (session *ProgressSession) Closed() bool {
	if session == nil {
		return true
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.closed
}

func (session *ProgressSession) SetCompletion(message string) {
	if session == nil {
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return
	}
	session.completion = strings.TrimSpace(message)
}

func (session *ProgressSession) Close() {
	if session == nil {
		return
	}
	session.mu.Lock()
	message := session.completion
	session.mu.Unlock()
	session.CloseWith(message)
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
	session.clearProvisionalLocked()
	if session.framed && session.mode == ModeHuman {
		if !session.gap && session.begun {
			fmt.Fprintln(session.out, session.theme.Render(RoleRail, session.glyphs.Rail))
			session.gap = true
		}
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

func (session *ProgressSession) spacer() {
	if session == nil {
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	session.beginLocked()
	session.gapLocked()
}

func (session *ProgressSession) beginBlock() {
	if session == nil {
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	session.beginLocked()
	session.gapLocked()
}

func (session *ProgressSession) line(value string) {
	if session == nil {
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return
	}
	session.beginLocked()
	session.clearTransientLocked()
	session.clearProvisionalLocked()
	fmt.Fprintln(session.out, value)
	session.gap = false
	session.renderProvisionalLocked()
}

func (session *ProgressSession) renderRunningLocked(phase ProgressPhase) {
	if session.mode != ModeHuman || !session.capabilities.Animation || !session.capabilities.CursorControl {
		return
	}
	session.clearTransientLocked()
	session.clearProvisionalLocked()
	session.animationSeq++
	sequence := session.animationSeq
	session.renderTransientLocked(session.glyphs.PhaseDone, phase.Label)
	session.transient = true
	interval := session.animationGap
	if interval <= 0 {
		interval = defaultProgressAnimationInterval
	}
	go session.animatePhase(sequence, phase.ID, phase.Label, interval)
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
	session.clearProvisionalLocked()
	glyph, role := session.terminalStyle(phase.State)
	label := terminalProgressLabel(phase)
	line := session.theme.Render(role, glyph) + "  " + label
	fmt.Fprintln(session.out, line)
	session.gap = false
	session.renderProvisionalLocked()
}

func (session *ProgressSession) gapLocked() {
	if session.closed || session.gap || !session.begun {
		return
	}
	if session.mode == ModeHuman && session.framed {
		session.clearProvisionalLocked()
		fmt.Fprintln(session.out, session.theme.Render(RoleRail, session.glyphs.Rail))
		session.renderProvisionalLocked()
	} else if session.mode == ModePlain {
		fmt.Fprintln(session.out)
	}
	session.gap = true
}

func (session *ProgressSession) clearTransientLocked() {
	if !session.transient {
		return
	}
	session.animationSeq++
	if session.provisionalEligibleLocked() && session.provisional {
		session.clearProvisionalLocked()
		fmt.Fprint(session.out, "\x1b[1A\r\x1b[2K")
	} else if session.capabilities.CursorControl {
		fmt.Fprint(session.out, "\r\x1b[2K")
	}
	session.transient = false
}

func (session *ProgressSession) animatePhase(sequence uint64, id, label string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	pending := true
	for range ticker.C {
		session.mu.Lock()
		if session.closed || !session.transient || session.animationSeq != sequence || session.activeID != id {
			session.mu.Unlock()
			return
		}
		glyph := session.glyphs.PhaseDone
		if pending {
			glyph = session.glyphs.PhasePending
		}
		pending = !pending
		session.renderTransientLocked(glyph, label)
		session.mu.Unlock()
	}
}

func (session *ProgressSession) renderTransientLocked(glyph, label string) {
	line := session.theme.Render(RoleActive, glyph) + "  " + label
	if session.provisionalEligibleLocked() {
		if session.transient && session.provisional {
			fmt.Fprint(session.out, "\x1b[2A\r\x1b[2K", line, "\x1b[2B\r")
			return
		}
		session.clearProvisionalLocked()
		fmt.Fprintln(session.out, line)
		session.renderProvisionalLocked()
		return
	}
	fmt.Fprint(session.out, "\r\x1b[2K", line)
}

func (session *ProgressSession) provisionalEligibleLocked() bool {
	return session.mode == ModeHuman && session.framed && session.capabilities.Interactive && session.capabilities.CursorControl && !session.suspended
}

func (session *ProgressSession) renderProvisionalLocked() {
	if !session.provisionalEligibleLocked() || session.closed || session.provisional {
		return
	}
	fmt.Fprintln(session.out, session.theme.Render(RoleRail, session.glyphs.FrameEnd))
	session.provisional = true
}

func (session *ProgressSession) clearProvisionalLocked() {
	if !session.provisional {
		return
	}
	fmt.Fprint(session.out, "\x1b[1A\r\x1b[2K")
	session.provisional = false
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
	return terminalProgressLabel(phase)
}

func terminalProgressLabel(phase ProgressPhase) string {
	if message := strings.TrimSpace(phase.Message); message != "" {
		return message
	}
	return strings.TrimSpace(phase.Label)
}
