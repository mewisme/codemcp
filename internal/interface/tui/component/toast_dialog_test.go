package component

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestToastDialogHasSingleCloseAction(t *testing.T) {
	dialog := NewToastDialog("Updated", "Workspace saved", ToneSuccess)
	plain := ansi.Strip(dialog.View())
	for _, want := range []string{"✓ Updated", "Workspace saved", "Close"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("toast dialog missing %q: %q", want, plain)
		}
	}
	if strings.Count(plain, "Close") != 1 {
		t.Fatalf("toast dialog close actions=%d: %q", strings.Count(plain, "Close"), plain)
	}
}

func TestToastDialogTitleOnlyDoesNotDuplicateTitle(t *testing.T) {
	plain := ansi.Strip(NewToastDialog("Saved", "", ToneSuccess).View())
	if strings.Count(plain, "Saved") != 1 || strings.Count(plain, "Close") != 1 {
		t.Fatalf("title-only toast duplicated content: %q", plain)
	}
}

func TestToastDialogWrapsLongReadableContent(t *testing.T) {
	token := strings.Repeat("x", 64)
	view := NewToastDialog("Long "+token, "https://example.test/"+token, ToneDanger).ViewWidth(20)
	for _, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > 20 {
			t.Fatalf("toast line width=%d want <=20: %q", got, ansi.Strip(line))
		}
	}
	flat := strings.ReplaceAll(ansi.Strip(view), "\n", "")
	if strings.Count(flat, token) != 2 {
		t.Fatalf("toast content was truncated: %q", flat)
	}
}
