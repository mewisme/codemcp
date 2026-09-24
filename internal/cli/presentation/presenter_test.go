package presentation

import (
	"bytes"
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
	p.List("alpha", "beta")
	p.Rows([]string{"Name", "State"}, Row{"one", "ready"}, Row{"two", "offline"})
	p.Note("Hint", "Use cm status --json for machine output.")

	got := output.String()
	for _, want := range []string{"CodeMCP status", "Runtime", "status", "running", "✓ Ready", "ℹ alpha", "Name", "State", "Hint"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("plain-color human output contains ANSI: %q", got)
	}
}

func TestPresenterRepresentativeHumanASCII(t *testing.T) {
	var output bytes.Buffer
	p := New(&output, ModeHuman, Capabilities{Width: 60, Unicode: false, Color: false})
	p.Status(StatusSuccess, "Ready")
	p.Status(StatusError, "Failed")
	p.List("item")

	got := output.String()
	for _, want := range []string{"[OK] Ready", "[ERR] Failed", "[i] item"} {
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
	p.Section("ignored")
	p.Status(StatusSuccess, "ignored")
	p.Fields(Field{Label: "ignored", Value: "ignored"})
	p.List("ignored")
	p.Rows([]string{"ignored"}, Row{"ignored"})
	p.Note("ignored", "ignored")
	if err := p.Markdown("ignored"); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatalf("JSON presenter wrote human output: %q", output.String())
	}
}
