package component

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/charmbracelet/x/ansi"

	"go.mewis.me/codemcp/internal/interface/tui/testutil"
)

func TestEditorMovesAcrossVisibleSectionsWithoutCompletingForm(t *testing.T) {
	general, detail := "general", "detail"
	editor := NewEditor("save",
		EditorSection{ID: "general", Title: "General", Form: NewEditorForm(Group(Input("General", &general)))},
		EditorSection{ID: "detail", Title: "Detail", Form: NewEditorForm(Group(Input("Detail", &detail)))},
	)
	editor = runEditorCmd(t, editor, editor.Init())
	updated, cmd := editor.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	editor = runEditorCmd(t, updated, cmd)
	if editor.ActiveSectionID() != "detail" {
		t.Fatalf("active section=%q", editor.ActiveSectionID())
	}
	form, _ := editor.SectionForm(0)
	if form.State() != huh.StateNormal {
		t.Fatalf("first form state=%v", form.State())
	}
	updated, cmd = editor.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	editor = runEditorCmd(t, updated, cmd)
	if editor.ActiveSectionID() != "general" {
		t.Fatalf("back section=%q", editor.ActiveSectionID())
	}
}

func TestEditorSubmitCancelFeedbackDirtyAndResponsiveLayout(t *testing.T) {
	value := "demo"
	editor := NewEditor("create", EditorSection{ID: "main", Title: "Main", Description: strings.Repeat("description ", 12), Form: NewEditorForm(Group(Input("Name", &value)))})
	editor = runEditorCmd(t, editor, editor.Init())
	editor.Resize(24, 10)
	testutil.AssertLinesFit(t, editor.View(), 24)
	value = "changed"
	if !editor.Dirty() {
		t.Fatal("editor did not detect dirty draft")
	}
	updated, cmd := editor.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if _, submitted := runEditorUntilSubmit(t, updated, cmd); !submitted {
		t.Fatal("final Enter did not submit editor")
	}
	_, cmd = editor.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("esc returned no cancel command")
	}
	if _, ok := cmd().(EditorCancelMsg); !ok {
		t.Fatalf("cancel message=%T", cmd())
	}
	editor.SetFeedback("saved", errors.New("invalid "+strings.Repeat("value ", 10)))
	testutil.AssertLinesFit(t, editor.View(), 24)
	editor.SetSubmitting(true)
	updated, cmd = editor.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if _, submitted := runEditorUntilSubmit(t, updated, cmd); submitted {
		t.Fatal("submitting editor accepted duplicate Enter submit")
	}
}

func TestEditorDefaultsToEnterCompletion(t *testing.T) {
	value := "complete"
	editor := NewEditor("apply", EditorSection{ID: "main", Title: "Main", Form: NewEditorForm(Group(Input("Name", &value)))})
	editor = runEditorCmd(t, editor, editor.Init())
	updated, cmd := editor.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !editorCmdProducesSubmit(t, updated, cmd) {
		t.Fatal("default editor did not submit final Input on Enter")
	}
	if plain := ansi.Strip(editor.View()); !strings.Contains(plain, "enter apply") {
		t.Fatalf("default editor does not advertise Enter submit: %q", plain)
	}
}

func TestEditorEnterAdvancesSectionsThenSubmits(t *testing.T) {
	first, second := "first", "second"
	editor := NewEditor("apply",
		EditorSection{ID: "first", Title: "First", Form: NewEditorForm(Group(Input("First", &first)))},
		EditorSection{ID: "second", Title: "Second", Form: NewEditorForm(Group(Input("Second", &second)))},
	)
	editor = runEditorCmd(t, editor, editor.Init())
	updated, cmd := editor.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	editor, submitted := runEditorUntilSubmit(t, updated, cmd)
	if submitted || editor.ActiveSectionID() != "second" {
		t.Fatalf("first section submitted=%t active=%q", submitted, editor.ActiveSectionID())
	}
	updated, cmd = editor.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, submitted = runEditorUntilSubmit(t, updated, cmd)
	if !submitted {
		t.Fatal("final section did not submit on Enter")
	}
}

func TestEditorEnterCompletesSelectAndSwitch(t *testing.T) {
	selected := "a"
	selectEditor := NewEditor("apply", EditorSection{ID: "select", Title: "Select", Form: NewEditorForm(Group(Select("Mode", &selected, huh.NewOption("A", "a"), huh.NewOption("B", "b"))))})
	selectEditor = runEditorCmd(t, selectEditor, selectEditor.Init())
	updated, cmd := selectEditor.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if _, submitted := runEditorUntilSubmit(t, updated, cmd); !submitted {
		t.Fatal("final Select did not submit on Enter")
	}

	enabled := true
	switchEditor := NewEditor("build", EditorSection{ID: "switch", Title: "Switch", Form: NewEditorForm(Group(Switch("Enabled", &enabled)))})
	switchEditor = runEditorCmd(t, switchEditor, switchEditor.Init())
	updated, cmd = switchEditor.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if _, submitted := runEditorUntilSubmit(t, updated, cmd); !submitted {
		t.Fatal("final Switch did not submit on Enter")
	}
}

func TestEditorMultilineUsesEnterForNewlineAndCtrlEnterToSubmit(t *testing.T) {
	value := "line one"
	editor := NewEditor("build", EditorSection{ID: "text", Title: "Text", Form: NewEditorForm(Group(Text("Content", &value)))})
	editor = runEditorCmd(t, editor, editor.Init())
	updated, cmd := editor.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	editor, submitted := runEditorUntilSubmit(t, updated, cmd)
	if submitted || !strings.Contains(value, "\n") {
		t.Fatalf("multiline Enter submitted=%t value=%q", submitted, value)
	}
	updated, cmd = editor.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl})
	if _, submitted = runEditorUntilSubmit(t, updated, cmd); !submitted {
		t.Fatal("multiline Ctrl+Enter did not submit")
	}
	if plain := ansi.Strip(editor.View()); !strings.Contains(plain, "ctrl+enter build") || !strings.Contains(plain, "enter new line") {
		t.Fatalf("multiline help=%q", plain)
	}
}

func TestEditorCompletionBlocksDuplicateWhileSubmitting(t *testing.T) {
	value := "demo"
	editor := NewEditor("apply", EditorSection{ID: "main", Title: "Main", Form: NewEditorForm(Group(Input("Name", &value)))})
	editor = runEditorCmd(t, editor, editor.Init())
	editor.SetSubmitting(true)
	updated, cmd := editor.Update(huh.NextField())
	if cmd != nil || updated.Submitting() != true {
		t.Fatalf("submitting completion cmd=%v submitting=%t", cmd, updated.Submitting())
	}
}

func TestEditorAcceptCommitsCurrentDraftBaseline(t *testing.T) {
	value := "initial"
	editor := NewEditor("save", EditorSection{ID: "main", Title: "Main", Form: NewEditorForm(Group(Input("Name", &value)))})
	value = "saved"
	if !editor.Dirty() {
		t.Fatal("changed editor was not dirty before accept")
	}
	editor.Accept()
	if editor.Dirty() {
		t.Fatal("accepted editor remained dirty")
	}
	value = "changed-again"
	if !editor.Dirty() {
		t.Fatal("editor did not become dirty after changing accepted baseline")
	}
}

func TestEditorValidateKeepsInvalidSectionVisible(t *testing.T) {
	first, second := "ok", ""
	editor := NewEditor("save",
		EditorSection{ID: "first", Title: "First", Form: NewEditorForm(Group(Input("First", &first)))},
		EditorSection{ID: "second", Title: "Second", Form: NewEditorForm(Group(Input("Second", &second).Validate(func(value string) error {
			if strings.TrimSpace(value) == "" {
				return errors.New("second is required")
			}
			return nil
		})))},
	)
	if err := editor.Validate(); err == nil || editor.ActiveSectionID() != "second" {
		t.Fatalf("validate err=%v section=%q", err, editor.ActiveSectionID())
	}
}

func TestEditorMouseSectionTargetsUseSharedGeometry(t *testing.T) {
	one, two := "one", "two"
	editor := NewEditor("save",
		EditorSection{ID: "one", Title: "One", Form: NewEditorForm(Group(Input("One", &one)))},
		EditorSection{ID: "two", Title: "Two", Form: NewEditorForm(Group(Input("Two", &two)))},
	)
	editor.Resize(40, 14)
	_ = editor.View()
	for _, target := range editor.MouseTargets(3, 2, 5) {
		if target.ID != "editor.section" {
			continue
		}
		message := target.Handle(MouseEvent{Button: tea.MouseLeft})
		section, ok := message.(EditorSectionMsg)
		if ok && section.Index == 1 {
			updated, _ := editor.Update(section)
			if updated.ActiveSection() != 1 {
				t.Fatalf("mouse section active=%d", updated.ActiveSection())
			}
			return
		}
	}
	t.Fatal("second editor section mouse target not found")
}

func runEditorCmd(t *testing.T, editor Editor, cmd tea.Cmd) Editor {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; steps < 64 && len(queue) > 0; steps++ {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		message := next()
		if batch, ok := message.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		updated, followup := editor.Update(message)
		editor = updated
		if followup != nil {
			queue = append(queue, followup)
		}
	}
	return editor
}

func editorCmdProducesSubmit(t *testing.T, editor Editor, cmd tea.Cmd) bool {
	t.Helper()
	_, submitted := runEditorUntilSubmit(t, editor, cmd)
	return submitted
}

func runEditorUntilSubmit(t *testing.T, editor Editor, cmd tea.Cmd) (Editor, bool) {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; steps < 64 && len(queue) > 0; steps++ {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		message := next()
		if _, ok := message.(EditorSubmitMsg); ok {
			return editor, true
		}
		if batch, ok := message.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		updated, followup := editor.Update(message)
		editor = updated
		if followup != nil {
			queue = append(queue, followup)
		}
	}
	return editor, false
}
