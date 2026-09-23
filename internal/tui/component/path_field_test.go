package component

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"go.mewis.me/codemcp/internal/tui/testutil"
)

func TestPathFieldPickerInputModesShareDraft(t *testing.T) {
	root := t.TempDir()
	value := filepath.Join(root, "one")
	if err := os.MkdirAll(value, 0700); err != nil {
		t.Fatal(err)
	}
	field := NewPathField("Workspace path", &value, PathFieldOptions{Kind: PathKindDirectory, Root: root})
	if field.Mode() != PathFieldPicker || field.GetValue() != value {
		t.Fatalf("mode=%d value=%v", field.Mode(), field.GetValue())
	}
	field.SetMode(PathFieldInput)
	value = filepath.Join(root, "two")
	field.syncValue()
	field.SetMode(PathFieldPicker)
	if field.GetValue() != value {
		t.Fatalf("draft changed across modes: %v", field.GetValue())
	}
	field.WithWidth(24)
	testutil.AssertLinesFit(t, field.View(), 24)
}

func TestPathFieldValidatesKindRootAndMissingFallback(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "dir")
	file := filepath.Join(root, "file.txt")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	value := directory
	directoryField := NewPathField("Directory", &value, PathFieldOptions{Kind: PathKindDirectory, Root: root})
	if err := directoryField.validatePath(directory); err != nil {
		t.Fatalf("directory validation: %v", err)
	}
	if err := directoryField.validatePath(file); err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("file accepted as directory: %v", err)
	}
	if err := directoryField.validatePath(filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing directory accepted without fallback")
	}
	directoryField.options.AllowMissing = true
	if err := directoryField.validatePath(filepath.Join(root, "missing")); err != nil {
		t.Fatalf("allow-missing input rejected: %v", err)
	}
	if err := directoryField.validatePath(filepath.Join(root, "..", "outside")); err == nil || !strings.Contains(err.Error(), "within") {
		t.Fatalf("outside root accepted: %v", err)
	}
}

func TestPathFieldRelativePickerValueAndToggleKey(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "nested")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	value := directory
	field := NewPathField("Project path", &value, PathFieldOptions{Kind: PathKindDirectory, Root: root, RelativeTo: root})
	field.normalizePickerValue()
	if value != "nested" {
		t.Fatalf("relative value=%q", value)
	}
	updated, _ := field.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	field = updated.(*PathField)
	if field.Mode() != PathFieldInput || value != "nested" {
		t.Fatalf("toggle mode=%d value=%q", field.Mode(), value)
	}
}

func TestPathFieldIntegratesWithEditorForm(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "project")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	value, name := directory, "demo"
	path := NewPathField("Workspace path", &value, PathFieldOptions{Kind: PathKindDirectory, Root: root})
	form := NewEditorForm(Group(path, Input("Name", &name)))
	form = runFormCmd(t, form, form.Init())
	if form.FocusedFieldIndex() != 0 || form.State() != huh.StateNormal {
		t.Fatalf("initial index=%d state=%v", form.FocusedFieldIndex(), form.State())
	}
	path.SetMode(PathFieldInput)
	updated, cmd := form.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	form = runFormCmd(t, updated, cmd)
	if form.FocusedFieldIndex() != 1 || form.State() != huh.StateNormal {
		t.Fatalf("after tab index=%d state=%v", form.FocusedFieldIndex(), form.State())
	}
	updated, cmd = form.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	form = runFormCmd(t, updated, cmd)
	if form.FocusedFieldIndex() != 1 || form.State() != huh.StateNormal {
		t.Fatalf("last field completed index=%d state=%v", form.FocusedFieldIndex(), form.State())
	}
}

func TestPathFieldFileKindRejectsDirectory(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "bundle.json")
	if err := os.WriteFile(file, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	value := file
	field := NewPathField("Bundle", &value, PathFieldOptions{Kind: PathKindFile, Root: root})
	if err := field.validatePath(file); err != nil {
		t.Fatalf("file validation: %v", err)
	}
	if err := field.validatePath(root); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("directory accepted as file: %v", err)
	}
}
