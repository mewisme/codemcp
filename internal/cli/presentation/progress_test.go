package presentation

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/productadapter"
)

func TestProgressStatesMapToCanonicalLifecycleVocabulary(t *testing.T) {
	tests := map[ProgressState]productadapter.LifecycleState{
		ProgressPending: productadapter.LifecycleIdle,
		ProgressRunning: productadapter.LifecycleWorking,
		ProgressSuccess: productadapter.LifecycleSuccess,
		ProgressSkipped: productadapter.LifecyclePartial,
		ProgressWarning: productadapter.LifecyclePartial,
		ProgressFailed:  productadapter.LifecycleTerminalFailure,
	}
	for state, want := range tests {
		if got := state.CanonicalLifecycle(); got != want {
			t.Fatalf("state=%d lifecycle=%q want=%q", state, got, want)
		}
	}
}

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

func TestProgressSessionInteractiveFrameStaysOpenUntilFinalClosure(t *testing.T) {
	var output bytes.Buffer
	session := NewProgressSession(&output, ModeHuman, Capabilities{
		Width: 80, Unicode: true, RawUnicode: true, Interactive: true, CursorControl: true,
	})
	session.Begin("Configure runtime")
	if begin := output.String(); strings.Contains(begin, "└") {
		t.Fatalf("active frame rendered provisional tail: %q", begin)
	}

	session.Success("save", "Save", "Configuration saved")
	session.Append(func(p *Presenter) { p.Note("Note", "Restart required") })
	beforeClose := output.String()
	if strings.Contains(beforeClose, "└") {
		t.Fatalf("active frame closed before command completion: %q", beforeClose)
	}

	session.CloseWith("Done")
	got := output.String()
	if !strings.HasSuffix(got, "└  Done\n") || strings.Count(got, "└") != 1 {
		t.Fatalf("final frame must close exactly once: %q", got)
	}
}

func TestProgressSessionInputSuspendsAndRestoresActiveProgressWithoutProvisionalTail(t *testing.T) {
	var output bytes.Buffer
	session := NewProgressSession(&output, ModeHuman, Capabilities{
		Width: 80, Unicode: true, RawUnicode: true, Interactive: true, CursorControl: true, Animation: true,
	})
	session.animationGap = time.Hour
	session.Begin("Interactive task")
	session.Update(ProgressPhase{ID: "work", Label: "Working", State: ProgressRunning})
	beforeRead := ""
	if err := session.WithInput(func(p *Presenter) {
		p.Prompt("Choose [1-2]")
	}, func() error {
		beforeRead = output.String()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(beforeRead, "└") {
		t.Fatalf("input prompt rendered provisional frame tail: %q", beforeRead)
	}
	if !strings.Contains(beforeRead, "Choose [1-2]") {
		t.Fatalf("input prompt missing while progress was suspended: %q", beforeRead)
	}
	after := output.String()
	if !strings.Contains(after, "Working") || strings.Contains(after, "└") {
		t.Fatalf("running phase was not restored cleanly after input: %q", after)
	}
	session.CloseWith("Done")
}

func TestProgressSessionCursorIneligibleHumanShowsRunningAndUsesOnlyFinalTail(t *testing.T) {
	var output bytes.Buffer
	session := NewProgressSession(&output, ModeHuman, Capabilities{Width: 80, Unicode: true, Interactive: true, CursorControl: false, Animation: true})
	session.Begin("Read state")
	session.Update(ProgressPhase{ID: "read", Label: "Reading state", State: ProgressRunning})
	session.Success("read", "Read", "State loaded")
	session.CloseWith("Done")
	got := output.String()
	if strings.ContainsAny(got, "\r\x1b") {
		t.Fatalf("cursor-ineligible human output contains terminal rewrites: %q", got)
	}
	for _, want := range []string{"◇  Reading state", "◆  State loaded", "└  Done"} {
		if !strings.Contains(got, want) {
			t.Fatalf("cursor-ineligible human output missing %q: %q", want, got)
		}
	}
	if strings.Count(got, "└") != 1 {
		t.Fatalf("cursor-ineligible human output must contain only final tail: %q", got)
	}
}

func TestProgressSessionDeduplicatesTerminalEventsAndRejectsRegression(t *testing.T) {
	var output bytes.Buffer
	session := NewProgressSession(&output, ModePlain, Capabilities{Width: 80})
	session.Update(ProgressPhase{ID: "persist", Label: "Save configuration", State: ProgressRunning})
	if !session.Fail("persist", "Save configuration", "disk full") {
		t.Fatal("first failure was ignored")
	}
	if session.Fail("persist", "Save configuration", "disk full") {
		t.Fatal("duplicate terminal event was accepted")
	}
	if session.Update(ProgressPhase{ID: "persist", Label: "Save configuration", State: ProgressRunning}) {
		t.Fatal("terminal -> running regression was accepted")
	}
	if session.Success("persist", "Save configuration", "unexpected recovery") {
		t.Fatal("terminal -> terminal rewrite was accepted")
	}
	session.Close()
	if got := output.String(); got != "disk full\n" {
		t.Fatalf("failure output=%q", got)
	}
}

func TestProgressSessionPendingCanTransitionDirectlyToTerminal(t *testing.T) {
	var output bytes.Buffer
	session := NewProgressSession(&output, ModeHuman, Capabilities{Width: 80, Unicode: true})
	if !session.Update(ProgressPhase{ID: "optional", Label: "Optional work", State: ProgressPending}) {
		t.Fatal("pending phase was ignored")
	}
	if !session.Skip("optional", "Optional work", "Optional work skipped") {
		t.Fatal("pending -> skipped transition was rejected")
	}
	if !strings.Contains(output.String(), "◇  Optional work skipped") {
		t.Fatalf("skipped outcome missing: %q", output.String())
	}
}

func TestProgressSessionResetPhaseAllowsExplicitNewGeneration(t *testing.T) {
	var output bytes.Buffer
	session := NewProgressSession(&output, ModePlain, Capabilities{Width: 80})
	session.Update(ProgressPhase{ID: "service.backend", Label: "Starting backend", State: ProgressRunning})
	if !session.Success("service.backend", "Starting backend", "Backend started") {
		t.Fatal("first generation did not terminate")
	}
	if session.Update(ProgressPhase{ID: "service.backend", Label: "Restarting backend", State: ProgressRunning}) {
		t.Fatal("terminal phase regressed without explicit reset")
	}
	session.ResetPhase("service.backend")
	if !session.Update(ProgressPhase{ID: "service.backend", Label: "Restarting backend", State: ProgressRunning}) {
		t.Fatal("explicit reset did not open a new lifecycle generation")
	}
	if !session.Success("service.backend", "Restarting backend", "Backend restarted") {
		t.Fatal("second generation did not terminate")
	}
	if got := output.String(); got != "Backend started\nBackend restarted\n" {
		t.Fatalf("explicit reset output=%q", got)
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

func TestProgressSessionTerminalPhasesDoNotCloseFrameUntilClose(t *testing.T) {
	var output bytes.Buffer
	session := NewProgressSession(&output, ModeHuman, Capabilities{Width: 80, Unicode: true, Interactive: true, CursorControl: true})
	session.Begin("Operation")
	session.Success("one", "One", "First complete")
	session.Warn("two", "Two", "Second degraded")
	session.Skip("three", "Three", "Third skipped")
	beforeClose := output.String()
	if strings.Contains(beforeClose, "└") {
		t.Fatalf("terminal phases closed active frame: %q", beforeClose)
	}
	session.CloseWith("Done")
	got := output.String()
	if !strings.HasSuffix(got, "└  Done\n") || strings.Count(got, "└") != 1 {
		t.Fatalf("terminal phases did not close cleanly exactly once: %q", got)
	}
}

func TestProgressSessionEmptyOperationRendersOnlyFinalTail(t *testing.T) {
	for _, completion := range []string{"Done", "Failed"} {
		t.Run(completion, func(t *testing.T) {
			var output bytes.Buffer
			session := NewProgressSession(&output, ModeHuman, Capabilities{Width: 80, Unicode: true, Interactive: true, CursorControl: true})
			session.Begin("Empty operation")
			if strings.Contains(output.String(), "└") {
				t.Fatalf("empty active operation rendered provisional tail: %q", output.String())
			}
			session.CloseWith(completion)
			got := output.String()
			if !strings.HasSuffix(got, "└  "+completion+"\n") || strings.Count(got, "└") != 1 {
				t.Fatalf("empty operation close=%q output=%q", completion, got)
			}
		})
	}
}

func TestProgressSessionJSONNeverEmitsPresentation(t *testing.T) {
	var output bytes.Buffer
	session := NewProgressSession(&output, ModeJSON, Capabilities{Width: 80, Unicode: true, Interactive: true, CursorControl: true, Animation: true})
	session.Begin("JSON operation")
	session.Update(ProgressPhase{ID: "one", Label: "One", State: ProgressRunning})
	session.Success("one", "One", "Complete")
	session.CloseWith("Done")
	if got := output.String(); got != "" {
		t.Fatalf("JSON progress emitted presentation bytes: %q", got)
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

func TestProgressSessionWrappedAnimationErasesWholeTransientBlock(t *testing.T) {
	var output bytes.Buffer
	session := NewProgressSession(&output, ModeHuman, Capabilities{
		Width: 22, Unicode: true, Interactive: true, CursorControl: true, Animation: true,
	})
	session.animationGap = time.Hour
	session.Begin("Long task")
	session.Update(ProgressPhase{
		ID:    "work",
		Label: "Doing work with a progress message that spans several physical lines",
		State: ProgressRunning,
	})
	if session.transientLines < 2 {
		t.Fatalf("long running phase did not span multiple physical lines: lines=%d output=%q", session.transientLines, output.String())
	}
	renderedLines := session.transientLines
	session.Success("work", "Doing work", "Work completed")
	got := output.String()
	if strings.Count(got, "\x1b[1A\r\x1b[2K") < renderedLines-1 {
		t.Fatalf("wrapped progress did not erase every transient physical line: lines=%d output=%q", renderedLines, got)
	}
	if !strings.Contains(got, "◆  Work completed") {
		t.Fatalf("terminal result missing after wrapped transient erase: %q", got)
	}
}

func TestProgressSessionDiagnosticWriteRestoresActiveProgress(t *testing.T) {
	var output bytes.Buffer
	session := NewProgressSession(&output, ModeHuman, Capabilities{
		Width: 80, Unicode: true, Interactive: true, CursorControl: true, Animation: true,
	})
	session.animationGap = time.Hour
	session.Begin("Diagnostic task")
	session.Update(ProgressPhase{ID: "work", Label: "Working", State: ProgressRunning})
	if _, err := session.DiagnosticWrite(&output, []byte("DBG detail\n")); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if !strings.Contains(got, "DBG detail") {
		t.Fatalf("diagnostic write missing: %q", got)
	}
	if strings.Count(got, "◆  Working") < 2 {
		t.Fatalf("active progress was not redrawn after diagnostic write: %q", got)
	}
	session.Success("work", "Working", "Worked")
	session.CloseWith("Done")
}

func TestProgressSessionCapturesFirstHumanWriteError(t *testing.T) {
	first := errors.New("progress write failed")
	session := NewProgressSession(&failingWriter{err: first}, ModeHuman, Capabilities{Width: 80, Unicode: true})
	session.SetTitle("Failure")
	session.EnsureBegun()
	session.Success("phase", "Phase", "Finished")
	session.CloseWith("Done")
	if !errors.Is(session.Err(), first) {
		t.Fatalf("session err=%v want=%v", session.Err(), first)
	}
}

func TestProgressSessionSuspendAndCloseClearTransientState(t *testing.T) {
	var output bytes.Buffer
	session := NewProgressSession(&output, ModeHuman, Capabilities{Unicode: true, CursorControl: true, Animation: true})
	session.Update(ProgressPhase{ID: "work", Label: "Doing work", State: ProgressRunning})
	session.Suspend()
	session.Close()
	got := output.String()
	if !strings.Contains(got, "\r\x1b[2K") {
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
