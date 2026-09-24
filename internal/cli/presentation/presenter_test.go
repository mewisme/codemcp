package presentation

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestPresenterRepresentativeHumanUnicode(t *testing.T) {
	var output bytes.Buffer
	p := New(&output, ModeHuman, Capabilities{Width: 80, Unicode: true, Color: false})
	p.Intro("CodeMCP status")
	p.Section("Runtime")
	p.Fields(
		Field{Label: "status", Value: "running"},
		Field{Label: "pid", Value: 4242},
	)
	p.Status(StatusSuccess, "Ready")
	p.Spacer()
	p.Subsection("Loopback")
	p.NestedFields(Field{Label: "mcp http", Value: "http://127.0.0.1:37421/mcp"})
	p.List("alpha", "beta")
	p.Rows([]string{"Name", "State"}, Row{"one", "ready"}, Row{"two", "offline"})
	p.Note("Hint", "Use cm status --json for machine output.")
	p.Outro("Done")

	got := output.String()
	for _, want := range []string{"┌  CodeMCP status", "◆  Runtime", "│  ◆ status — running", "✓  Ready", "│  ◆ Loopback", "│  │  mcp http — http://127.0.0.1:37421/mcp", "│  ◆ alpha", "│  ◆ one — ready", "·  Hint", "└  Done"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
	if strings.ContainsRune(got, 'ℹ') {
		t.Fatalf("rich presenter emitted information-source glyph: %q", got)
	}
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("plain-color human output contains ANSI: %q", got)
	}
}

func TestPresenterRailHierarchyGolden(t *testing.T) {
	var output bytes.Buffer
	p := New(&output, ModeHuman, Capabilities{Width: 100, Unicode: true, Color: false})
	p.Frame("CodeMCP status")
	p.Section("Runtime")
	p.Fields(
		Field{Label: "pid", Value: 4242},
		Field{Label: "session", Value: "run_abcd"},
	)
	p.Spacer()
	p.StateSection(StatusInactive, "Tunnel")
	p.ChildState(StatusInactive, "OpenAI Secure MCP Tunnel", "disabled")
	p.FrameEnd("Status complete")

	want := "┌  CodeMCP status\n" +
		"│\n" +
		"◆  Runtime\n" +
		"│  ◆ pid — 4242\n" +
		"│  ◆ session — run_abcd\n" +
		"│\n" +
		"◇  Tunnel\n" +
		"│  ◇ OpenAI Secure MCP Tunnel — disabled\n" +
		"│\n" +
		"└  Status complete\n"
	if got := output.String(); got != want {
		t.Fatalf("rail hierarchy mismatch:\nwant=%q\ngot =%q", want, got)
	}
}

func TestPresenterCollectionRailUsesChildAndContinuationRows(t *testing.T) {
	var output bytes.Buffer
	p := New(&output, ModeHuman, Capabilities{Width: 100, Unicode: true, Color: false})
	p.Frame("Workspaces")
	p.Section("Registered 2 workspaces")
	p.Rows(
		[]string{"ID", "Status", "Root"},
		Row{"ws_alpha", "ready", "/work/a"},
		Row{"ws_beta", "ready", "/work/b"},
	)
	p.FrameEnd("Done")

	want := "┌  Workspaces\n" +
		"│\n" +
		"◆  Registered 2 workspaces\n" +
		"│  ◆ ws_alpha\n" +
		"│  │  Status — ready\n" +
		"│  │  Root — /work/a\n" +
		"│  ◆ ws_beta\n" +
		"│  │  Status — ready\n" +
		"│  │  Root — /work/b\n" +
		"│\n" +
		"└  Done\n"
	if got := output.String(); got != want {
		t.Fatalf("collection rail mismatch:\nwant=%q\ngot =%q", want, got)
	}
}

func TestPresenterSeparatorFollowsGlyphCapabilities(t *testing.T) {
	unicode := New(io.Discard, ModeHuman, Capabilities{Unicode: true})
	ascii := New(io.Discard, ModeHuman, Capabilities{Unicode: false})
	if unicode.Separator() != UnicodeGlyphs.Separator || ascii.Separator() != ASCIIGlyphs.Separator {
		t.Fatalf("separators unicode=%q ascii=%q", unicode.Separator(), ascii.Separator())
	}
}

func TestPresenterRepresentativeHumanASCII(t *testing.T) {
	var output bytes.Buffer
	p := New(&output, ModeHuman, Capabilities{Width: 60, Unicode: false, Color: false})
	p.Status(StatusSuccess, "Ready")
	p.Status(StatusError, "Failed")
	p.List("item")

	got := output.String()
	for _, want := range []string{"[OK]  Ready", "[ERR]  Failed", "|  * item"} {
		if !strings.Contains(got, want) {
			t.Fatalf("ASCII output missing %q: %q", want, got)
		}
	}
	for _, r := range got {
		if r > 0x7f {
			t.Fatalf("ASCII presenter emitted non-ASCII rune %q in %q", r, got)
		}
	}
}

func TestPresenterPlainIsDeterministicAndWidthAware(t *testing.T) {
	var first, second bytes.Buffer
	caps := Capabilities{Width: 24, Unicode: false, Color: false}
	render := func(out *bytes.Buffer) {
		p := New(out, ModePlain, caps)
		p.Section("Runtime")
		p.Fields(Field{Label: "very-long-label", Value: "critical-value-that-must-not-be-truncated"})
		p.Rows([]string{"Name", "Description"}, Row{"alpha", "a very long description"})
	}
	render(&first)
	render(&second)
	if first.String() != second.String() {
		t.Fatalf("plain output unstable:\nfirst=%q\nsecond=%q", first.String(), second.String())
	}
	if !strings.Contains(first.String(), "critical-value-that-must-not-be-truncated") {
		t.Fatalf("critical value was truncated: %q", first.String())
	}
	if strings.Contains(first.String(), "\x1b[") {
		t.Fatalf("plain output contains ANSI: %q", first.String())
	}
}

func TestPresenterMarkdownUsesInjectedCapabilities(t *testing.T) {
	var output bytes.Buffer
	p := New(&output, ModePlain, Capabilities{Width: 32, Color: false, Unicode: false})
	if err := p.Markdown("# Heading\n\nA short paragraph."); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if !strings.Contains(got, "Heading") || strings.Contains(got, "\x1b[") {
		t.Fatalf("markdown output=%q", got)
	}
}

func TestPresenterJSONModeIsSilent(t *testing.T) {
	var output bytes.Buffer
	p := New(&output, ModeJSON, Capabilities{Width: 80, Unicode: true, Color: true})
	p.Intro("ignored")
	p.Frame("ignored")
	p.Section("ignored")
	p.StateSection(StatusInactive, "ignored")
	p.Status(StatusSuccess, "ignored")
	p.ChildStatus(StatusWarning, "ignored")
	p.ChildState(StatusInactive, "ignored", "ignored")
	p.Fields(Field{Label: "ignored", Value: "ignored"})
	p.List("ignored")
	p.Rows([]string{"ignored"}, Row{"ignored"})
	p.Note("ignored", "ignored")
	p.FrameEnd("ignored")
	if err := p.Markdown("ignored"); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatalf("JSON presenter wrote human output: %q", output.String())
	}
}
