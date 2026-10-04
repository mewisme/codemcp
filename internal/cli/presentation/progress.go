package presentation

import (
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

func (state ProgressState) terminal() bool {
	switch state {
	case ProgressSuccess, ProgressSkipped, ProgressWarning, ProgressFailed:
		return true
	default:
		return false
	}
}

type ProgressPhase struct {
	ID      string
	Label   string
	State   ProgressState
	Message string
}

type ProgressSession struct {
	mu             sync.Mutex
	sink           *terminalSink
	mode           ResultMode
	capabilities   Capabilities
	theme          Theme
	glyphs         GlyphSet
	title          string
	presenter      *Presenter
	phases         map[string]ProgressPhase
	runningVisible map[string]bool
	activeID       string
	transientLines int
	suspended      bool
	animationSeq   uint64
	animationGap   time.Duration
	begun          bool
	gap            bool
	framed         bool
	completion     string
	closed         bool
}

func NewProgressSession(out io.Writer, mode ResultMode, capabilities Capabilities) *ProgressSession {
	sink := newTerminalSink(out)
	session := &ProgressSession{
		sink:           sink,
		mode:           mode,
		capabilities:   capabilities,
		theme:          NewTheme(capabilities),
		glyphs:         Glyphs(capabilities),
		phases:         map[string]ProgressPhase{},
		runningVisible: map[string]bool{},
		animationGap:   defaultProgressAnimationInterval,
	}
	session.presenter = newSessionPresenter(session)
	return session
}

func (session *ProgressSession) Err() error {
	if session == nil || session.sink == nil {
		return nil
	}
	return session.sink.err()
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
		session.sink.line(title)
		session.sink.line("")
		session.gap = true
		return
	}
	layout := newLayoutContext(
		effectiveLayoutWidth(session.capabilities.Width),
		session.theme.Render(RoleRail, session.glyphs.FrameStart)+"  ",
		session.theme.Render(RoleRail, session.glyphs.Rail)+"  ",
	)
	for _, line := range layout.render(title, func(value string) string {
		return session.theme.Render(RoleHeading, value)
	}) {
		session.sink.line(line)
	}
	session.sink.line(session.theme.Render(RoleRail, session.glyphs.Rail))
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
	session.beginLocked()
	if previous, ok := session.phases[phase.ID]; ok {
		if previous == phase {
			return false
		}
		if !validProgressTransition(previous.State, phase.State) {
			return false
		}
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

// ResetPhase explicitly starts a new lifecycle generation for an identifier.
// Normal Update calls keep terminal phases immutable; callers that intentionally
// repeat the same operation within one session must opt in through this reset.
func (session *ProgressSession) ResetPhase(id string) {
	if session == nil {
		return
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.activeID == id {
		session.clearTransientLocked()
		session.activeID = ""
	}
	delete(session.phases, id)
	delete(session.runningVisible, id)
}

func validProgressTransition(previous, next ProgressState) bool {
	if previous.terminal() {
		return false
	}
	switch previous {
	case ProgressPending:
		return next == ProgressPending || next == ProgressRunning || next.terminal()
	case ProgressRunning:
		return next == ProgressRunning || next.terminal()
	default:
		return false
	}
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
	session.suspended = true
}

func (session *ProgressSession) Resume() {
	if session == nil {
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return
	}
	session.suspended = false
	session.restoreActiveLocked()
}

func (session *ProgressSession) DiagnosticWrite(out io.Writer, data []byte) (int, error) {
	if out == nil {
		out = io.Discard
	}
	if session == nil {
		return out.Write(data)
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return out.Write(data)
	}
	session.beginLocked()
	wasSuspended := session.suspended
	session.clearTransientLocked()
	session.suspended = true
	n, err := out.Write(data)
	session.suspended = wasSuspended
	if !wasSuspended {
		session.restoreActiveLocked()
	}
	return n, err
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
	wasSuspended := session.suspended
	session.clearTransientLocked()
	session.suspended = true
	session.gapLocked()
	session.mu.Unlock()

	render(session.presenter)

	session.mu.Lock()
	session.suspended = wasSuspended
	if !wasSuspended {
		session.restoreActiveLocked()
	}
	session.mu.Unlock()
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
	if session.framed && session.mode == ModeHuman {
		if !session.gap && session.begun {
			session.sink.line(session.theme.Render(RoleRail, session.glyphs.Rail))
			session.gap = true
		}
		message = strings.TrimSpace(message)
		if message == "" {
			session.sink.line(session.theme.Render(RoleRail, session.glyphs.FrameEnd))
		} else {
			layout := newLayoutContext(
				effectiveLayoutWidth(session.capabilities.Width),
				session.theme.Render(RoleRail, session.glyphs.FrameEnd)+"  ",
				"   ",
			)
			for _, line := range layout.render(message, nil) {
				session.sink.line(line)
			}
		}
	} else if session.begun && session.mode == ModePlain {
		session.gapLocked()
		if message = strings.TrimSpace(message); message != "" {
			session.sink.line(message)
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
	session.sink.line(value)
	session.gap = false
	if !session.suspended {
		session.restoreActiveLocked()
	}
}

func (session *ProgressSession) protectedInput(label string, maskCount int, final bool) {
	if session == nil || session.mode == ModeJSON {
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return
	}
	session.beginLocked()
	session.clearTransientLocked()
	line := label + ": " + strings.Repeat("*", maskCount)
	if session.mode == ModeHuman {
		line = session.theme.Render(RoleRail, session.glyphs.Rail) + "  " + session.theme.Render(RoleActive, session.glyphs.PhasePending) + " " + line
	}
	if session.capabilities.CursorControl {
		_, _ = session.sink.writeString("\r\x1b[2K" + line)
	} else if maskCount == 0 {
		_, _ = session.sink.writeString(line)
	}
	if final {
		_, _ = session.sink.writeString("\n")
		session.gap = false
	}
}

func (session *ProgressSession) renderRunningLocked(phase ProgressPhase) {
	if session.mode == ModeJSON || session.mode == ModePlain || session.suspended {
		return
	}
	if session.capabilities.Animation && session.capabilities.CursorControl {
		session.clearTransientLocked()
		session.animationSeq++
		sequence := session.animationSeq
		session.renderTransientLocked(session.glyphs.PhaseDone, phase.Label)
		session.runningVisible[phase.ID] = true
		interval := session.animationGap
		if interval <= 0 {
			interval = defaultProgressAnimationInterval
		}
		go session.animatePhase(sequence, phase.ID, phase.Label, interval)
		return
	}
	if session.runningVisible[phase.ID] {
		return
	}
	for _, line := range session.progressLines(session.glyphs.PhasePending, RoleActive, phase.Label) {
		session.sink.line(line)
	}
	session.runningVisible[phase.ID] = true
	session.gap = false
}

func (session *ProgressSession) renderTerminalLocked(phase ProgressPhase) {
	if session.mode == ModeJSON {
		return
	}
	if session.mode == ModePlain {
		session.sink.line(plainProgressLine(phase))
		session.gap = false
		return
	}
	glyph, role := session.terminalStyle(phase.State)
	label := terminalProgressLabel(phase)
	for _, line := range session.progressLines(glyph, role, label) {
		session.sink.line(line)
	}
	session.gap = false
}

func (session *ProgressSession) gapLocked() {
	if session.closed || session.gap || !session.begun {
		return
	}
	if session.mode == ModeHuman && session.framed {
		session.clearTransientLocked()
		session.sink.line(session.theme.Render(RoleRail, session.glyphs.Rail))
	} else if session.mode == ModePlain {
		session.sink.line("")
	}
	session.gap = true
}

func (session *ProgressSession) restoreActiveLocked() {
	if session.closed || session.suspended || session.activeID == "" {
		return
	}
	phase, ok := session.phases[session.activeID]
	if !ok || phase.State != ProgressRunning {
		return
	}
	session.renderRunningLocked(phase)
}

func (session *ProgressSession) clearTransientLocked() {
	if session.transientLines == 0 {
		return
	}
	session.animationSeq++
	session.eraseTransientLocked()
}

func (session *ProgressSession) eraseTransientLocked() {
	if session.transientLines == 0 {
		return
	}
	if session.capabilities.CursorControl {
		_, _ = session.sink.writeString("\r\x1b[2K")
		for line := 1; line < session.transientLines; line++ {
			_, _ = session.sink.writeString("\x1b[1A\r\x1b[2K")
		}
	}
	session.transientLines = 0
}

func (session *ProgressSession) animatePhase(sequence uint64, id, label string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	pending := true
	for range ticker.C {
		session.mu.Lock()
		if session.closed || session.transientLines == 0 || session.animationSeq != sequence || session.activeID != id || session.suspended {
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
	lines := session.progressLines(glyph, RoleActive, label)
	if len(lines) == 0 {
		return
	}
	session.eraseTransientLocked()
	for index, line := range lines {
		if index == 0 {
			_, _ = session.sink.writeString("\r\x1b[2K" + line)
			continue
		}
		_, _ = session.sink.writeString("\n\r\x1b[2K" + line)
	}
	session.transientLines = len(lines)
}

func (session *ProgressSession) progressLines(glyph string, role Role, label string) []string {
	first := session.theme.Render(role, glyph) + "  "
	continuation := strings.Repeat(" ", displayWidth(glyph)+2)
	if session.framed {
		continuation = session.theme.Render(RoleRail, session.glyphs.Rail) + "  "
	}
	layout := newLayoutContext(effectiveLayoutWidth(session.capabilities.Width), first, continuation)
	return layout.render(label, nil)
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
