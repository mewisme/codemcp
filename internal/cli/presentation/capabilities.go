package presentation

import (
	"io"
	"os"
	"runtime"
	"strings"

	"golang.org/x/term"
)

const DefaultWidth = 100

type Capabilities struct {
	StdoutTTY     bool
	StderrTTY     bool
	Width         int
	Color         bool
	Unicode       bool
	RawUnicode    bool
	Interactive   bool
	CursorControl bool
	Animation     bool
}

type DetectOptions struct {
	Stdout        io.Writer
	Stderr        io.Writer
	ForceColor    bool
	NoColor       bool
	HumanResult   bool
	MachineOutput bool
	Platform      string
	LookupEnv     func(string) string
	IsTerminal    func(io.Writer) bool
	TerminalSize  func(io.Writer) (int, int, error)
}

type capabilityWriter struct {
	io.Writer
	capabilities Capabilities
}

func (writer capabilityWriter) TerminalCapabilities() Capabilities { return writer.capabilities }

func WrapWriter(writer io.Writer, capabilities Capabilities) io.Writer {
	if writer == nil {
		writer = io.Discard
	}
	return capabilityWriter{Writer: writer, capabilities: capabilities}
}

func FromWriter(writer io.Writer) (Capabilities, bool) {
	type provider interface {
		TerminalCapabilities() Capabilities
	}
	value, ok := writer.(provider)
	if !ok {
		return Capabilities{}, false
	}
	return value.TerminalCapabilities(), true
}

func Detect(options DetectOptions) Capabilities {
	lookupEnv := options.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.Getenv
	}
	isTerminal := options.IsTerminal
	if isTerminal == nil {
		isTerminal = writerIsTerminal
	}
	terminalSize := options.TerminalSize
	if terminalSize == nil {
		terminalSize = writerTerminalSize
	}
	platform := strings.TrimSpace(options.Platform)
	if platform == "" {
		platform = runtime.GOOS
	}

	stdoutTTY := isTerminal(options.Stdout)
	stderrTTY := isTerminal(options.Stderr)
	termName := strings.TrimSpace(lookupEnv("TERM"))
	interactive := stdoutTTY && !strings.EqualFold(termName, "dumb")
	width := DefaultWidth
	if stdoutTTY {
		if value, _, err := terminalSize(options.Stdout); err == nil && value > 0 {
			width = value
		}
	}

	colorEnabled := detectColor(options.NoColor, options.ForceColor, stdoutTTY, termName, lookupEnv)
	unicodeEnabled := detectUnicode(platform, lookupEnv)
	rawUnicodeEnabled := detectRawUnicode(platform, lookupEnv)
	cursorControl := interactive && cursorControlSafe(platform, lookupEnv)
	animation := options.HumanResult && !options.MachineOutput && cursorControl

	return Capabilities{
		StdoutTTY:     stdoutTTY,
		StderrTTY:     stderrTTY,
		Width:         width,
		Color:         colorEnabled,
		Unicode:       unicodeEnabled,
		RawUnicode:    rawUnicodeEnabled,
		Interactive:   interactive,
		CursorControl: cursorControl,
		Animation:     animation,
	}
}

func DetectWriter(writer io.Writer) Capabilities {
	if capabilities, ok := FromWriter(writer); ok {
		return capabilities
	}
	return Detect(DetectOptions{Stdout: writer})
}

func detectColor(noColor, forceColor, stdoutTTY bool, termName string, lookupEnv func(string) string) bool {
	if noColor {
		return false
	}
	if forceColor {
		return true
	}
	if lookupEnv("NO_COLOR") != "" {
		return false
	}
	if value := strings.TrimSpace(lookupEnv("FORCE_COLOR")); value != "" {
		return value != "0" && !strings.EqualFold(value, "false")
	}
	return stdoutTTY && !strings.EqualFold(termName, "dumb")
}

func detectUnicode(platform string, lookupEnv func(string) string) bool {
	if enabledOverride(lookupEnv("CM_ASCII")) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(lookupEnv("TERM")), "linux") {
		return false
	}
	if platform == "windows" {
		return windowsUnicodeSafe(lookupEnv)
	}
	if enabledOverride(lookupEnv("CM_UNICODE")) {
		return true
	}
	return true
}

func detectRawUnicode(platform string, lookupEnv func(string) string) bool {
	if enabledOverride(lookupEnv("CM_ASCII")) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(lookupEnv("TERM")), "linux") {
		return false
	}
	if platform == "windows" {
		return false
	}
	return detectUnicode(platform, lookupEnv)
}

func cursorControlSafe(platform string, lookupEnv func(string) string) bool {
	if platform != "windows" {
		return true
	}
	return windowsUnicodeSafe(lookupEnv)
}

func windowsUnicodeSafe(lookupEnv func(string) string) bool {
	if lookupEnv("CI") != "" || lookupEnv("WT_SESSION") != "" || lookupEnv("TERMINUS_SUBLIME") != "" {
		return true
	}
	if lookupEnv("ConEmuTask") == "{cmd::Cmder}" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(lookupEnv("TERM_PROGRAM"))) {
	case "terminus-sublime", "vscode":
		return true
	}
	switch strings.ToLower(strings.TrimSpace(lookupEnv("TERM"))) {
	case "xterm-256color", "alacritty":
		return true
	}
	return strings.EqualFold(strings.TrimSpace(lookupEnv("TERMINAL_EMULATOR")), "JetBrains-JediTerm")
}

func enabledOverride(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func writerIsTerminal(writer io.Writer) bool {
	type fdWriter interface{ Fd() uintptr }
	value, ok := writer.(fdWriter)
	return ok && term.IsTerminal(int(value.Fd()))
}

func writerTerminalSize(writer io.Writer) (int, int, error) {
	type fdWriter interface{ Fd() uintptr }
	value, ok := writer.(fdWriter)
	if !ok {
		return 0, 0, os.ErrInvalid
	}
	return term.GetSize(int(value.Fd()))
}
