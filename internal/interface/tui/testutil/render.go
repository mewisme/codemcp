package testutil

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func AssertLinesFit(t testing.TB, rendered string, width int) {
	t.Helper()
	for index, line := range strings.Split(ansi.Strip(rendered), "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d width=%d want <=%d: %q", index, got, width, line)
		}
	}
}
