package presentation

import (
	"bytes"
	"strings"
	"testing"
)

func TestProgressSessionInteractiveRailKeepsOnlyActivePhaseTransient(t *testing.T) {
	var output bytes.Buffer
	session := NewProgressSession(&output, ModeHuman, Capabilities{
		Width: 80, Unicode: true, RawUnicode: true, Interactive: true, CursorControl: true, Animation: true,
	})
	session.Begin("Upgrade CodeMCP")
	if !session.Update(ProgressPhase{ID: "detect", Label: "Detect installation", State: ProgressRunning}) {
		t.Fatal("running phase was ignored")
	}
	if !session.Success("detect", "Detect installation", "Installation detected") {
		t.Fatal("success phase was ignored")
	}
	session.Update(ProgressPhase{ID: "download", Label: "Download release", State: ProgressRunning})
	session.Success("download", "Download release", "Release downloaded")
	session.Close()

	got := output.String()
	for _, want := range []string{"┌  Upgrade CodeMCP", "⠋  Detect installation", "◆  Installation detected", "◆  Release downloaded", "└"} {
		if !strings.Contains(got, want) {
			t.Fatalf("interactive progress missing %q: %q", want, got)
		}
	}
	if strings.Count(got, "◆  Installation detected") != 1 {
		t.Fatalf("completed phase was not stable exactly once: %q", got)
	}
}

func TestProgressSessionPlainIsDeterministicAndNeverUsesCursorControl(t *testing.T) {
	var output bytes.Buffer
	session := NewProgressSession(&output, ModePlain, Capabilities{Width: 80, Unicode: false})
	session.Update(ProgressPhase{ID: "check", Label: "Check release", State: ProgressRunning})
	session.Success("check", "Check release", "Release checked")
	session.Update(ProgressPhase{ID: "reload", Label: "Reload runtime", State: ProgressRunning})
	session.Skip("reload", "Reload runtime", "Runtime reload skipped")
	session.Close()

	got := output.String()
	if got != "Release checked\nRuntime reload skipped\n" {
		t.Fatalf("plain progress=%q", got)
	}
	if strings.ContainsAny(got, "\r\x1b") {
		t.Fatalf("plain progress contains terminal control bytes: %q", got)
	}
}

func TestProgressSessionDeduplicatesTerminalEventsAndFailureIsStable(t *testing.T) {
	var output bytes.Buffer
	session := NewProgressSession(&output, ModePlain, Capabilities{Width: 80})
	session.Update(ProgressPhase{ID: "persist", Label: "Save configuration", State: ProgressRunning})
	if !session.Fail("persist", "Save configuration", "disk full") {
		t.Fatal("first failure was ignored")
	}
	if session.Fail("persist", "Save configuration", "disk full") {
		t.Fatal("duplicate terminal event was accepted")
	}
	session.Close()
	if got := output.String(); got != "Save configuration... failed: disk full\n" {
		t.Fatalf("failure output=%q", got)
	}
}

func TestProgressSessionSuspendAndCloseClearTransientState(t *testing.T) {
	var output bytes.Buffer
	session := NewProgressSession(&output, ModeHuman, Capabilities{Unicode: true, CursorControl: true, Animation: true})
	session.Update(ProgressPhase{ID: "work", Label: "Doing work", State: ProgressRunning})
	session.Suspend()
	session.Close()
	got := output.String()
	if !strings.HasSuffix(got, "\r\x1b[2K") {
		t.Fatalf("transient line was not cleared: %q", got)
	}
}
