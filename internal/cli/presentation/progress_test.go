package presentation

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestProgressSessionInteractiveRailKeepsOnlyActivePhaseTransient(t *testing.T) {
	var output bytes.Buffer
	session := NewProgressSession(&output, ModeHuman, Capabilities{
		Width: 80, Unicode: true, RawUnicode: true, Interactive: true, CursorControl: true, Animation: true,
	})
	session.animationGap = time.Millisecond
	session.Begin("Upgrade CodeMCP")
	if !session.Update(ProgressPhase{ID: "detect", Label: "Detect installation", State: ProgressRunning}) {
		t.Fatal("running phase was ignored")
	}
	time.Sleep(10 * time.Millisecond)
	if !session.Success("detect", "Detect installation", "Installation detected") {
		t.Fatal("success phase was ignored")
	}
	session.Update(ProgressPhase{ID: "download", Label: "Download release", State: ProgressRunning})
	session.Success("download", "Download release", "Release downloaded")
	session.Close()

	got := output.String()
	for _, want := range []string{"┌  Upgrade CodeMCP", "◆  Detect installation", "◇  Detect installation", "◆  Installation detected", "◆  Release downloaded", "└"} {
		if !strings.Contains(got, want) {
			t.Fatalf("interactive progress missing %q: %q", want, got)
		}
	}
	if strings.Count(got, "◆  Installation detected") != 1 {
		t.Fatalf("completed phase was not stable exactly once: %q", got)
	}
	if strings.Contains(got, "⠋") {
		t.Fatalf("legacy spinner glyph leaked into progress animation: %q", got)
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
	if got := output.String(); got != "disk full\n" {
		t.Fatalf("failure output=%q", got)
	}
}

func TestProgressSessionTerminalStatesUseOutcomeLabelOnly(t *testing.T) {
	tests := []struct {
		name  string
		state ProgressState
		want  string
	}{
		{name: "success", state: ProgressSuccess, want: "◆  Configuration saved"},
		{name: "skipped", state: ProgressSkipped, want: "◇  Runtime reload skipped"},
		{name: "warning", state: ProgressWarning, want: "!  Runtime reload delayed"},
		{name: "failed", state: ProgressFailed, want: "×  Configuration save failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			session := NewProgressSession(&output, ModeHuman, Capabilities{Unicode: true})
			session.Update(ProgressPhase{
				ID: "phase", Label: "Reloading running runtime", State: test.state, Message: test.want[strings.Index(test.want, "  ")+2:],
			})
			got := output.String()
			if !strings.Contains(got, test.want) {
				t.Fatalf("terminal outcome missing %q: %q", test.want, got)
			}
			if strings.Contains(got, "Reloading running runtime —") {
				t.Fatalf("terminal outcome repeated running label: %q", got)
			}
		})
	}
}

func TestProgressSessionASCIIAnimationUsesPhaseGlyphPair(t *testing.T) {
	var output bytes.Buffer
	session := NewProgressSession(&output, ModeHuman, Capabilities{
		Width: 80, Unicode: false, Interactive: true, CursorControl: true, Animation: true,
	})
	session.animationGap = time.Millisecond
	session.Update(ProgressPhase{ID: "work", Label: "Doing work", State: ProgressRunning})
	time.Sleep(10 * time.Millisecond)
	session.Success("work", "Doing work", "Work completed")
	got := output.String()
	for _, want := range []string{"*  Doing work", ".  Doing work", "*  Work completed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("ASCII animation missing %q: %q", want, got)
		}
	}
	for _, forbidden := range []string{"◆", "◇", "⠋"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("ASCII animation leaked Unicode glyph %q: %q", forbidden, got)
		}
	}
}

func TestProgressSessionHumanWithoutCursorControlIsNonAnimated(t *testing.T) {
	var output bytes.Buffer
	session := NewProgressSession(&output, ModeHuman, Capabilities{
		Width: 80, Unicode: true, Interactive: true, CursorControl: false, Animation: true,
	})
	session.Update(ProgressPhase{ID: "work", Label: "Doing work", State: ProgressRunning})
	session.Success("work", "Doing work", "Work completed")
	got := output.String()
	if strings.ContainsAny(got, "\r\x1b") {
		t.Fatalf("cursor-ineligible progress contains control bytes: %q", got)
	}
	if strings.Contains(got, "Doing work") || strings.Count(got, "Work completed") != 1 {
		t.Fatalf("cursor-ineligible progress is not deterministic: %q", got)
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

func TestProgressSessionWithInputSuspendsAndResumesActivePhase(t *testing.T) {
	var output bytes.Buffer
	session := NewProgressSession(&output, ModeHuman, Capabilities{
		Width: 80, Unicode: true, RawUnicode: true, Interactive: true, CursorControl: true, Animation: true,
	})
	session.animationGap = time.Hour
	session.Begin("Workspace relocation")
	session.Update(ProgressPhase{ID: "relocate", Label: "Relocating workspace", State: ProgressRunning})
	if err := session.WithInput(func(presenter *Presenter) {
		presenter.Prompt("Select resolution [1-4], then press Enter")
	}, func() error {
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	session.Success("relocate", "Relocating workspace", "Workspace relocated")
	session.CloseWith("Done")

	got := output.String()
	if strings.Count(got, "\r\x1b[2K") < 2 {
		t.Fatalf("input lifecycle did not clear active progress before prompt and completion: %q", got)
	}
	prompt := strings.Index(got, "Select resolution [1-4], then press Enter")
	complete := strings.Index(got, "Workspace relocated")
	if prompt < 0 || complete <= prompt {
		t.Fatalf("prompt/completion order is invalid: %q", got)
	}
	if strings.Count(got, "┌  Workspace relocation") != 1 || strings.Count(got, "└  Done") != 1 {
		t.Fatalf("input lifecycle broke frame ownership: %q", got)
	}
}

func TestProgressSessionWithInputPlainModeIsCursorFree(t *testing.T) {
	var output bytes.Buffer
	session := NewProgressSession(&output, ModePlain, Capabilities{Width: 80})
	session.SetTitle("Workspace relocation")
	session.Update(ProgressPhase{ID: "relocate", Label: "Relocating workspace", State: ProgressRunning})
	if err := session.WithInput(func(presenter *Presenter) {
		presenter.Prompt("Select resolution [1-4], then press Enter")
	}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	session.Success("relocate", "Relocating workspace", "Workspace relocated")
	session.CloseWith("Done")
	got := output.String()
	if strings.ContainsAny(got, "\r\x1b") {
		t.Fatalf("plain input lifecycle contains terminal control bytes: %q", got)
	}
	for _, want := range []string{"Workspace relocation", "Select resolution [1-4], then press Enter", "Workspace relocated", "Done"} {
		if !strings.Contains(got, want) {
			t.Fatalf("plain input lifecycle missing %q: %q", want, got)
		}
	}
}
