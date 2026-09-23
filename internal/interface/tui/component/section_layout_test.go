package component

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestSectionLayoutUsesAllRowsBeforeFooter(t *testing.T) {
	layout := NewSectionLayout("Logs", "live", "", 80, 20, 1)
	lines := strings.Split(ansi.Strip(layout.Header), "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[1]) != "" {
		t.Fatalf("root section title is not followed by an empty row: %q", ansi.Strip(layout.Header))
	}
	prefixHeight := lipgloss.Height(layout.Header)
	if layout.BodyY != prefixHeight {
		t.Fatalf("body y=%d want=%d", layout.BodyY, prefixHeight)
	}
	if layout.BodyHeight != 20-prefixHeight-1 {
		t.Fatalf("body height=%d want=%d", layout.BodyHeight, 20-prefixHeight-1)
	}
}

func TestSectionLayoutCountsFeedbackRowsExactly(t *testing.T) {
	layout := NewSectionLayout("Logs", "live", "warning\nretrying", 80, 20, 1)
	prefixHeight := lipgloss.Height(layout.Header)
	if layout.BodyY != prefixHeight || layout.BodyHeight != 20-prefixHeight-1 {
		t.Fatalf("layout y=%d height=%d prefix=%d", layout.BodyY, layout.BodyHeight, prefixHeight)
	}
}

func TestSectionLayoutOmitsTitleRowWhenTitleEmpty(t *testing.T) {
	layout := NewSectionLayout("", "3 items", "", 40, 12, 0)
	plain := ansi.Strip(layout.Header)
	if strings.Contains(plain, "\n\n") || !strings.Contains(plain, "3 items") {
		t.Fatalf("titleless header=%q", plain)
	}
	if got := lipgloss.Height(layout.Header); got != 2 || layout.BodyY != 2 || layout.BodyHeight != 10 {
		t.Fatalf("header=%d body=%d/%d", got, layout.BodyY, layout.BodyHeight)
	}

	layout = NewSectionLayout("", "", "", 40, 12, 0)
	if got := lipgloss.Height(layout.Header); got != 1 || layout.BodyY != 1 || layout.BodyHeight != 11 {
		t.Fatalf("empty header=%d body=%d/%d", got, layout.BodyY, layout.BodyHeight)
	}
}
