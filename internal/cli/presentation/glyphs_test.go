package presentation

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestGlyphSetsRespectUnicodeCapability(t *testing.T) {
	if got := Glyphs(Capabilities{Unicode: true}); got.Success != "✓" || got.Rail != "│" {
		t.Fatalf("unicode glyphs = %#v", got)
	}
	if got := Glyphs(Capabilities{Unicode: false}); got.Success != "[OK]" || got.Rail != "|" {
		t.Fatalf("ASCII glyphs = %#v", got)
	}
	if got := RawGlyphs(Capabilities{Unicode: true, RawUnicode: false}); got.Success != "[OK]" {
		t.Fatalf("raw glyphs ignored raw-write capability: %#v", got)
	}
}

func TestASCIIGlyphSetContainsOnlyASCII(t *testing.T) {
	values := []string{
		ASCIIGlyphs.FrameStart, ASCIIGlyphs.FrameEnd,
		ASCIIGlyphs.Success, ASCIIGlyphs.Error, ASCIIGlyphs.Warning, ASCIIGlyphs.Info,
		ASCIIGlyphs.Active, ASCIIGlyphs.Rail, ASCIIGlyphs.PhaseDone, ASCIIGlyphs.PhasePending,
		ASCIIGlyphs.Branch, ASCIIGlyphs.LastBranch, ASCIIGlyphs.Horizontal, ASCIIGlyphs.Separator,
	}
	for _, value := range values {
		if !utf8.ValidString(value) {
			t.Fatalf("invalid glyph %q", value)
		}
		for _, r := range value {
			if r > 0x7f {
				t.Fatalf("ASCII glyph set contains non-ASCII rune %q in %q", r, value)
			}
		}
	}
	if strings.Contains(strings.Join(values, ""), "\x1b") {
		t.Fatal("glyph set contains ANSI control bytes")
	}
}
