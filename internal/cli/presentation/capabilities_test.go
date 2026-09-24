package presentation

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestDetectTerminalCapabilities(t *testing.T) {
	var output bytes.Buffer
	capabilities := Detect(DetectOptions{
		Stdout:      &output,
		Stderr:      &output,
		HumanResult: true,
		LookupEnv:   env(map[string]string{"TERM": "xterm-256color"}),
		IsTerminal:  func(io.Writer) bool { return true },
		TerminalSize: func(io.Writer) (int, int, error) {
			return 132, 40, nil
		},
	})
	if !capabilities.StdoutTTY || !capabilities.StderrTTY || !capabilities.Interactive || !capabilities.Color || !capabilities.Unicode || !capabilities.CursorControl || !capabilities.Animation || capabilities.Width != 132 {
		t.Fatalf("capabilities = %#v", capabilities)
	}
}

func TestDetectNonTTYAndDumbTerminalArePlainSafe(t *testing.T) {
	var output bytes.Buffer
	nonTTY := Detect(DetectOptions{
		Stdout:      &output,
		HumanResult: true,
		LookupEnv:   env(nil),
		IsTerminal:  func(io.Writer) bool { return false },
	})
	if nonTTY.Color || nonTTY.Interactive || nonTTY.CursorControl || nonTTY.Animation || nonTTY.Width != DefaultWidth {
		t.Fatalf("non-TTY capabilities = %#v", nonTTY)
	}
	dumb := Detect(DetectOptions{
		Stdout:      &output,
		HumanResult: true,
		LookupEnv:   env(map[string]string{"TERM": "dumb"}),
		IsTerminal:  func(io.Writer) bool { return true },
		TerminalSize: func(io.Writer) (int, int, error) {
			return 88, 24, nil
		},
	})
	if dumb.Color || dumb.Interactive || dumb.CursorControl || dumb.Animation || dumb.Width != 88 {
		t.Fatalf("TERM=dumb capabilities = %#v", dumb)
	}
}

func TestDetectColorPrecedence(t *testing.T) {
	var output bytes.Buffer
	base := DetectOptions{Stdout: &output, IsTerminal: func(io.Writer) bool { return false }}
	tests := []struct {
		name  string
		no    bool
		force bool
		env   map[string]string
		want  bool
	}{
		{name: "explicit-no-color-wins", no: true, force: true, env: map[string]string{"FORCE_COLOR": "1"}, want: false},
		{name: "explicit-color-overrides-no-color-env", force: true, env: map[string]string{"NO_COLOR": "1"}, want: true},
		{name: "no-color-env", env: map[string]string{"NO_COLOR": "1", "FORCE_COLOR": "1"}, want: false},
		{name: "force-color-env", env: map[string]string{"FORCE_COLOR": "1"}, want: true},
		{name: "force-color-zero", env: map[string]string{"FORCE_COLOR": "0"}, want: false},
		{name: "force-color-false", env: map[string]string{"FORCE_COLOR": "false"}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := base
			options.NoColor = test.no
			options.ForceColor = test.force
			options.LookupEnv = env(test.env)
			if got := Detect(options).Color; got != test.want {
				t.Fatalf("Color=%t want %t", got, test.want)
			}
		})
	}
}

func TestDetectUnicodeOverridesAndSafetyFallbacks(t *testing.T) {
	tests := []struct {
		name       string
		platform   string
		env        map[string]string
		unicode    bool
		rawUnicode bool
		cursorSafe bool
	}{
		{name: "ascii-override", platform: "linux", env: map[string]string{"CM_ASCII": "1"}, unicode: false, rawUnicode: false, cursorSafe: true},
		{name: "unicode-override", platform: "linux", env: map[string]string{"CM_UNICODE": "1"}, unicode: true, rawUnicode: true, cursorSafe: true},
		{name: "kernel-console-safety-wins", platform: "linux", env: map[string]string{"TERM": "linux", "CM_UNICODE": "1"}, unicode: false, rawUnicode: false, cursorSafe: true},
		{name: "windows-legacy-console", platform: "windows", env: map[string]string{}, unicode: false, rawUnicode: false, cursorSafe: false},
		{name: "windows-safety-wins-over-unicode-override", platform: "windows", env: map[string]string{"CM_UNICODE": "1"}, unicode: false, rawUnicode: false, cursorSafe: false},
		{name: "windows-terminal-buffered-unicode", platform: "windows", env: map[string]string{"WT_SESSION": "1"}, unicode: true, rawUnicode: false, cursorSafe: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			caps := Detect(DetectOptions{
				Stdout:      &output,
				HumanResult: true,
				Platform:    test.platform,
				LookupEnv:   env(test.env),
				IsTerminal:  func(io.Writer) bool { return true },
				TerminalSize: func(io.Writer) (int, int, error) {
					return 80, 24, nil
				},
			})
			if caps.Unicode != test.unicode || caps.RawUnicode != test.rawUnicode || caps.CursorControl != test.cursorSafe {
				t.Fatalf("capabilities = %#v", caps)
			}
		})
	}
}

func TestMachineOutputNeverAnimates(t *testing.T) {
	var output bytes.Buffer
	caps := Detect(DetectOptions{
		Stdout:        &output,
		HumanResult:   true,
		MachineOutput: true,
		LookupEnv:     env(map[string]string{"TERM": "xterm-256color"}),
		IsTerminal:    func(io.Writer) bool { return true },
		TerminalSize: func(io.Writer) (int, int, error) {
			return 80, 24, nil
		},
	})
	if caps.Animation {
		t.Fatalf("machine output animation enabled: %#v", caps)
	}
}

func TestCapabilityWriterProvidesInjectedSnapshot(t *testing.T) {
	var output bytes.Buffer
	want := Capabilities{Width: 77, Color: true, Unicode: false}
	writer := WrapWriter(&output, want)
	got, ok := FromWriter(writer)
	if !ok || got != want {
		t.Fatalf("FromWriter() = %#v, %t; want %#v", got, ok, want)
	}
	if _, err := writer.Write([]byte("value")); err != nil || output.String() != "value" {
		t.Fatalf("wrapped writer output=%q err=%v", output.String(), err)
	}
}

func TestTerminalSizeFailureFallsBack(t *testing.T) {
	var output bytes.Buffer
	caps := Detect(DetectOptions{
		Stdout:       &output,
		LookupEnv:    env(nil),
		IsTerminal:   func(io.Writer) bool { return true },
		TerminalSize: func(io.Writer) (int, int, error) { return 0, 0, errors.New("unavailable") },
	})
	if caps.Width != DefaultWidth {
		t.Fatalf("width=%d want %d", caps.Width, DefaultWidth)
	}
}

func TestASCIIOverrideParsingIsBounded(t *testing.T) {
	for _, value := range []string{"1", "true", "YES", "on"} {
		if !enabledOverride(value) {
			t.Fatalf("%q should enable override", value)
		}
	}
	for _, value := range []string{"", "0", "false", "random"} {
		if enabledOverride(value) {
			t.Fatalf("%q should not enable override", value)
		}
	}
	if strings.Contains(ASCIIGlyphs.Branch+ASCIIGlyphs.LastBranch, "─") {
		t.Fatal("ASCII glyphs contain Unicode rail characters")
	}
}
