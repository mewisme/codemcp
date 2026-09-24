package logger

import (
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	spinnerlib "github.com/briandowns/spinner"
	"github.com/fatih/color"
	"golang.org/x/term"
)

type Options struct {
	Level    Level
	Mode     Mode
	Format   Format
	TimeMode TimeMode
	Writer   io.Writer
	Terminal *TerminalOptions
}

type TerminalOptions struct {
	Color      bool
	Unicode    bool
	RawUnicode bool
	Animate    bool
}

type Logger struct {
	level    Level
	mode     Mode
	format   Format
	timeMode TimeMode
	out      io.Writer
	now      func() time.Time
	animate  bool
	terminal *TerminalOptions
	eventMu  sync.Mutex
	renderMu sync.Mutex
	spinMu   sync.Mutex
	spinner  *spinnerState
	sinksMu  sync.RWMutex
	sinks    []Sink
}

type spinnerState struct {
	event   Event
	spinner *spinnerlib.Spinner
}

const defaultSpinnerRate = 80 * time.Millisecond

var spinnerCharacterSets = [...]int{11, 13, 14, 21, 22, 23, 24}

func New(level Level) *Logger { return NewWithOptions(Options{Level: level, Writer: color.Output}) }
func NewCLI() *Logger         { return NewWithOptions(Options{Level: Info, Writer: color.Output}) }
func NewWithWriter(level Level, writer io.Writer) *Logger {
	return NewWithOptions(Options{Level: level, Writer: writer})
}
func NewCLIWithWriter(writer io.Writer) *Logger {
	return NewWithOptions(Options{Level: Info, Writer: writer})
}
func NewWithOptions(options Options) *Logger {
	if options.Writer == nil {
		options.Writer = color.Output
	}
	if options.Format == "" {
		options.Format = FormatText
	}
	animate := options.Format == FormatText && options.Mode != ModeDebug && terminalWriter(options.Writer)
	var terminalOptions *TerminalOptions
	if options.Terminal != nil {
		value := *options.Terminal
		terminalOptions = &value
		animate = options.Format == FormatText && options.Mode != ModeDebug && value.Animate
	}
	return &Logger{level: options.Level, mode: options.Mode, format: options.Format, timeMode: options.TimeMode, out: options.Writer, now: time.Now, animate: animate, terminal: terminalOptions}
}

func randomSpinnerCharset(unicode bool) []string {
	if !unicode {
		return []string{".", "*", "+", "x", "o", "O"}
	}
	var random [1]byte
	if _, err := cryptorand.Read(random[:]); err == nil {
		id := spinnerCharacterSets[int(random[0])%len(spinnerCharacterSets)]
		return spinnerlib.CharSets[id]
	}
	return spinnerlib.CharSets[14]
}

func (l *Logger) Emit(event Event) {
	if l == nil {
		return
	}
	event = l.normalize(event)
	l.writeSinks(event)
	if event.Level < l.level || event.Visibility > l.visibility() {
		return
	}
	if l.format == FormatJSON {
		l.renderMu.Lock()
		defer l.renderMu.Unlock()
		l.renderJSON(event)
		return
	}
	l.eventMu.Lock()
	defer l.eventMu.Unlock()
	l.renderText(event)
}

func (l *Logger) Close() {
	l.eventMu.Lock()
	defer l.eventMu.Unlock()
	l.stopSpinner(true)
}

func (l *Logger) StopAnimation() {
	if l == nil {
		return
	}
	l.eventMu.Lock()
	defer l.eventMu.Unlock()
	l.stopSpinner(true)
}

func CanAnimate(writer io.Writer) bool { return terminalWriter(writer) }

func terminalWriter(writer io.Writer) bool {
	type fdWriter interface{ Fd() uintptr }
	value, ok := writer.(fdWriter)
	return ok && term.IsTerminal(int(value.Fd()))
}

func (l *Logger) normalize(event Event) Event {
	if strings.TrimSpace(event.Name) == "" {
		event.Name = legacyEventName(event.Component, event.Message)
	}
	if strings.TrimSpace(event.Message) == "" {
		event.Message = event.Name
	}
	if event.Component == "" {
		event.Component = "CLI"
	}
	if event.Kind == KindInfo {
		switch event.Level {
		case Warn:
			event.Kind = KindWarning
		case Error:
			event.Kind = KindError
		}
	}
	if event.Time.IsZero() {
		event.Time = l.now()
	}
	return event
}

func (l *Logger) Action(component, name, message string, fields ...Field) {
	l.Emit(Event{Level: Info, Name: name, Message: message, Fields: fields, Component: component, Kind: KindAction})
}
func (l *Logger) Ready(component, name, message string, fields ...Field) {
	l.Emit(Event{Level: Info, Name: name, Message: message, Fields: fields, Component: component, Kind: KindSuccess})
}
func (l *Logger) Notice(component, name, message string, fields ...Field) {
	l.Emit(Event{Level: Info, Name: name, Message: message, Fields: fields, Component: component, Kind: KindInfo})
}
func (l *Logger) Warning(component, name, message string, err error, fields ...Field) {
	l.Emit(Event{Level: Warn, Name: name, Message: message, Fields: fields, Err: err, Component: component, Kind: KindWarning})
}
func (l *Logger) Failure(component, name, message string, err error, fields ...Field) {
	l.Emit(Event{Level: Error, Name: name, Message: message, Fields: fields, Err: err, Component: component, Kind: KindError})
}
func (l *Logger) Verbose(component, name, message string, fields ...Field) {
	l.Emit(Event{Level: Info, Name: name, Message: message, Fields: fields, Component: component, Kind: KindInfo, Visibility: VisibilityVerbose})
}
func (l *Logger) Diagnostic(level Level, component, name, message string, fields ...Field) {
	l.Emit(Event{Level: level, Name: name, Message: message, Fields: fields, Component: component, Kind: kindForLevel(level), Visibility: VisibilityDebug})
}

func (l *Logger) Debug(component, message string, fields ...any) {
	l.Emit(legacyEvent(Debug, KindInfo, VisibilityDebug, component, message, fields...))
}
func (l *Logger) Info(component, message string, fields ...any) {
	l.Emit(legacyEvent(Info, KindInfo, VisibilityDefault, component, message, fields...))
}
func (l *Logger) Warn(component, message string, fields ...any) {
	l.Emit(legacyEvent(Warn, KindWarning, VisibilityDefault, component, message, fields...))
}
func (l *Logger) Error(component, message string, fields ...any) {
	l.Emit(legacyEvent(Error, KindError, VisibilityDefault, component, message, fields...))
}
func (l *Logger) Success(component, message string, fields ...any) {
	l.Emit(legacyEvent(Info, KindSuccess, VisibilityDefault, component, message, fields...))
}
func (l *Logger) Detail(label string, value any) {
	l.Emit(Event{Level: Info, Name: "cli.detail", Message: strings.TrimSpace(label), Fields: []Field{With("value", value)}, Component: "CLI", Kind: KindInfo})
}

func (l *Logger) eventTime(event Event) time.Time {
	if !event.Time.IsZero() {
		return event.Time
	}
	return l.now()
}

func (l *Logger) showTime() bool {
	switch l.timeMode {
	case TimeShow:
		return true
	case TimeHide:
		return false
	default:
		return false
	}
}

func (l *Logger) visibility() Visibility {
	switch l.mode {
	case ModeDebug:
		return VisibilityDebug
	case ModeVerbose:
		return VisibilityVerbose
	default:
		return VisibilityDefault
	}
}

func legacyEvent(level Level, kind Kind, visibility Visibility, component, message string, values ...any) Event {
	fields, err := legacyFields(values...)
	return Event{Level: level, Name: legacyEventName(component, message), Message: message, Fields: fields, Err: err, Component: component, Kind: kind, Visibility: visibility}
}

func legacyFields(values ...any) ([]Field, error) {
	fields := make([]Field, 0, len(values)/2)
	var eventErr error
	for i := 0; i+1 < len(values); i += 2 {
		key := strings.TrimSpace(fmt.Sprint(values[i]))
		value := values[i+1]
		if strings.EqualFold(key, "error") {
			switch typed := value.(type) {
			case error:
				eventErr = typed
			case nil:
			default:
				eventErr = errors.New(fmt.Sprint(typed))
			}
			continue
		}
		fields = append(fields, WithVerbose(key, value))
	}
	return fields, eventErr
}

func legacyEventName(component, message string) string {
	component = slug(component)
	message = slug(message)
	if component == "" {
		component = "log"
	}
	if message == "" {
		return component
	}
	return component + "." + message
}

func slug(value string) string {
	var builder strings.Builder
	lastDash := false
	for _, char := range strings.ToLower(strings.TrimSpace(value)) {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			builder.WriteRune(char)
			lastDash = false
			continue
		}
		if builder.Len() > 0 && !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func kindForLevel(level Level) Kind {
	switch level {
	case Warn:
		return KindWarning
	case Error:
		return KindError
	default:
		return KindInfo
	}
}

func (l *Logger) styled(attrs ...color.Attribute) *color.Color {
	value := color.New(attrs...)
	if l != nil && l.terminal != nil {
		if l.terminal.Color {
			value.EnableColor()
		} else {
			value.DisableColor()
		}
		return value
	}
	if os.Getenv("NO_COLOR") != "" {
		value.DisableColor()
	}
	return value
}

func (l *Logger) unicodeEnabled() bool {
	return l == nil || l.terminal == nil || l.terminal.Unicode
}

func (l *Logger) rawUnicodeEnabled() bool {
	return l == nil || l.terminal == nil || l.terminal.RawUnicode
}
