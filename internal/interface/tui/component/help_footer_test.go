package component

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestHelpFooterCollapsesAfterFiveBindings(t *testing.T) {
	footer := NewHelpFooter(
		Binding([]string{"1"}, "1", "one"), Binding([]string{"2"}, "2", "two"), Binding([]string{"3"}, "3", "three"),
		Binding([]string{"4"}, "4", "four"), Binding([]string{"5"}, "5", "five"), Binding([]string{"6"}, "6", "six"),
	)
	plain := ansi.Strip(footer.View(120))
	if !strings.Contains(plain, "? more") || strings.Contains(plain, "5 five") || strings.Contains(plain, "6 six") {
		t.Fatalf("collapsed footer=%q", plain)
	}
	if !footer.Update(tea.KeyPressMsg{Code: '?'}) || !footer.Expanded() {
		t.Fatal("help footer did not expand")
	}
	plain = ansi.Strip(footer.View(120))
	for _, want := range []string{"1 one", "5 five", "6 six", "? less"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expanded footer missing %q: %q", want, plain)
		}
	}
}

func TestHelpFooterDoesNotToggleAtFiveBindings(t *testing.T) {
	footer := NewHelpFooter(
		Binding([]string{"1"}, "1", "one"), Binding([]string{"2"}, "2", "two"), Binding([]string{"3"}, "3", "three"),
		Binding([]string{"4"}, "4", "four"), Binding([]string{"5"}, "5", "five"),
	)
	if footer.Update(tea.KeyPressMsg{Code: '?'}) || footer.Expanded() || strings.Contains(ansi.Strip(footer.View(120)), "? more") {
		t.Fatalf("five-binding footer unexpectedly toggled: %q", ansi.Strip(footer.View(120)))
	}
}
