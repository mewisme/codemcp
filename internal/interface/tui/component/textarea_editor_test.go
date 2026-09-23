package component

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestTextAreaEditorEditsSavesAndTracksDirtyState(t *testing.T) {
	editor := NewTextAreaEditor("Global context", "hello")
	editor.Resize(60, 16)
	if editor.Dirty() || editor.Value() != "hello" || !editor.Focused() {
		t.Fatalf("initial editor dirty=%v value=%q focused=%v", editor.Dirty(), editor.Value(), editor.Focused())
	}
	updated, _ := editor.Update(tea.KeyPressMsg{Text: "!", Code: '!'})
	editor = updated
	if !editor.Dirty() || editor.Value() != "hello!" {
		t.Fatalf("edited editor dirty=%v value=%q", editor.Dirty(), editor.Value())
	}
	_, cmd := editor.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("save command is nil")
	}
	message, ok := cmd().(TextAreaSavedMsg)
	if !ok || message.Value != "hello!" {
		t.Fatalf("save message=%#v", message)
	}
}

func TestTextAreaEditorCancelsAndRendersBubblesHelp(t *testing.T) {
	editor := NewTextAreaEditor("Rule content", "line one\nline two")
	editor.Resize(52, 12)
	plain := ansi.Strip(editor.View())
	for _, want := range []string{"Rule content", "line one", "line two", "ctrl+enter save", "esc cancel"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("editor view missing %q: %q", want, plain)
		}
	}
	_, cmd := editor.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("cancel command is nil")
	}
	if _, ok := cmd().(TextAreaCancelledMsg); !ok {
		t.Fatalf("cancel message=%#v", cmd())
	}
}

func TestTextAreaEditorSetValueResetsDirtyBaseline(t *testing.T) {
	editor := NewTextAreaEditor("Context", "one")
	updated, _ := editor.Update(tea.KeyPressMsg{Text: "x", Code: 'x'})
	editor = updated
	if !editor.Dirty() {
		t.Fatal("editor should be dirty after edit")
	}
	editor.SetValue("fresh")
	if editor.Dirty() || editor.Value() != "fresh" {
		t.Fatalf("reset editor dirty=%v value=%q", editor.Dirty(), editor.Value())
	}
}

func TestTextAreaEditorCustomPrimaryActionAndWrapping(t *testing.T) {
	editor := NewTextAreaEditorAction("", strings.Repeat("abcdefghij", 8), "create")
	editor.Resize(24, 10)
	plain := ansi.Strip(editor.View())
	if !strings.Contains(plain, "ctrl+enter create") || strings.Contains(plain, "ctrl+enter save") {
		t.Fatalf("custom action help=%q", plain)
	}
	for _, line := range strings.Split(plain, "\n") {
		if len([]rune(line)) > 24 {
			t.Fatalf("textarea line exceeds width: %q", line)
		}
	}
}
