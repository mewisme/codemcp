package component

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestWrapContentPreservesAndBoundsReadableText(t *testing.T) {
	for name, value := range map[string]string{
		"token":   strings.Repeat("a", 41),
		"linux":   "/very/long/workspace/path/with/a/deeply/nested/file.json",
		"windows": `C:\Users\Mew\Projects\very-long-workspace\nested\file.json`,
		"url":     "https://example.test/a/very/long/path?token=abcdefghijklmnopqrstuvwxyz0123456789",
		"unicode": strings.Repeat("界", 14),
		"ansi":    ToneText(strings.Repeat("failure-", 8), ToneDanger),
	} {
		t.Run(name, func(t *testing.T) {
			wrapped := WrapContent(value, 12)
			for _, line := range strings.Split(wrapped, "\n") {
				if got := lipgloss.Width(line); got > 12 {
					t.Fatalf("line width=%d want <=12: %q", got, ansi.Strip(line))
				}
			}
			if strings.ReplaceAll(ansi.Strip(wrapped), "\n", "") != ansi.Strip(value) {
				t.Fatalf("content changed: wrapped=%q value=%q", ansi.Strip(wrapped), ansi.Strip(value))
			}
		})
	}
}

func TestWrapContentPreservesExplicitNewlines(t *testing.T) {
	value := "first\n\n" + strings.Repeat("x", 18) + "\nlast"
	wrapped := WrapContent(value, 8)
	if !strings.Contains(wrapped, "first\n\n") || !strings.HasSuffix(wrapped, "\nlast") {
		t.Fatalf("explicit newlines changed: %q", wrapped)
	}
	if got := WrapContent(value, 0); got != value {
		t.Fatalf("zero-width wrap changed content: %q", got)
	}
}

func TestWrapStructuredContentPreservesIndentationAndBoundsWidth(t *testing.T) {
	value := "{\n  \"path\": \"/" + strings.Repeat("very-long-segment/", 8) + "\"\n}"
	wrapped := WrapStructuredContent(value, 24)
	lines := strings.Split(wrapped, "\n")
	if len(lines) < 4 {
		t.Fatalf("structured content did not wrap: %q", wrapped)
	}
	for _, line := range lines {
		if got := lipgloss.Width(line); got > 24 {
			t.Fatalf("line width=%d want <=24: %q", got, ansi.Strip(line))
		}
	}
	for _, line := range lines[1 : len(lines)-1] {
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "  ") {
			t.Fatalf("continuation lost indentation: %q", line)
		}
	}
}

func TestWrapKeyValueUsesHangingIndent(t *testing.T) {
	wrapped := WrapKeyValue("Path", "/very/long/workspace/path/that/keeps/going", 18)
	lines := strings.Split(wrapped, "\n")
	if len(lines) < 2 {
		t.Fatalf("value did not wrap: %q", ansi.Strip(wrapped))
	}
	for _, line := range lines {
		if got := lipgloss.Width(line); got > 18 {
			t.Fatalf("line width=%d want <=18: %q", got, ansi.Strip(line))
		}
	}
	if !strings.HasPrefix(ansi.Strip(lines[1]), "      ") {
		t.Fatalf("missing hanging indent: %q", ansi.Strip(wrapped))
	}
}

func TestBannerWidthWrapsWithMarkerIndent(t *testing.T) {
	message := "failure/" + strings.Repeat("x", 48)
	view := BannerWidth(message, ToneDanger, 18)
	for _, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > 18 {
			t.Fatalf("banner line width=%d want <=18: %q", got, ansi.Strip(line))
		}
	}
	if flat := strings.ReplaceAll(strings.ReplaceAll(ansi.Strip(view), "\n", ""), "  ", ""); !strings.Contains(flat, message) {
		t.Fatalf("banner content changed: %q", flat)
	}
}

func TestWrapModalBodyUsesInnerWidth(t *testing.T) {
	token := strings.Repeat("m", 64)
	view := WrapModalBody(token, 30)
	for _, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > ModalContentWidth(30) {
			t.Fatalf("modal body line width=%d want <=%d", got, ModalContentWidth(30))
		}
	}
}
