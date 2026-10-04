package presentation

type GlyphSet struct {
	FrameStart   string
	FrameEnd     string
	Section      string
	Success      string
	Error        string
	Warning      string
	Info         string
	Active       string
	Rail         string
	PhaseDone    string
	PhasePending string
	Branch       string
	LastBranch   string
	Horizontal   string
	Separator    string
}

var UnicodeGlyphs = GlyphSet{
	FrameStart:   "┌",
	FrameEnd:     "└",
	Section:      "▸",
	Success:      "✓",
	Error:        "×",
	Warning:      "!",
	Info:         "·",
	Active:       "⠋",
	Rail:         "│",
	PhaseDone:    "◆",
	PhasePending: "◇",
	Branch:       "├─ ",
	LastBranch:   "└─ ",
	Horizontal:   "─",
	Separator:    "·",
}

var ASCIIGlyphs = GlyphSet{
	FrameStart:   "+",
	FrameEnd:     "+",
	Section:      ">",
	Success:      "[OK]",
	Error:        "[ERR]",
	Warning:      "[!]",
	Info:         "[i]",
	Active:       "*",
	Rail:         "|",
	PhaseDone:    "*",
	PhasePending: ".",
	Branch:       "|- ",
	LastBranch:   "`- ",
	Horizontal:   "-",
	Separator:    "|",
}

func Glyphs(capabilities Capabilities) GlyphSet {
	if capabilities.Unicode {
		return UnicodeGlyphs
	}
	return ASCIIGlyphs
}

func RawGlyphs(capabilities Capabilities) GlyphSet {
	if capabilities.RawUnicode {
		return UnicodeGlyphs
	}
	return ASCIIGlyphs
}
