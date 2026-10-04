package fanout

import "strings"

type Mode string

const (
	Off          Mode = "off"
	Auto         Mode = "auto"
	Conservative Mode = "conservative"
	Aggressive   Mode = "aggressive"
)

func NormalizeRuntimeMode(value string) (Mode, bool) {
	switch Mode(strings.ToLower(strings.TrimSpace(value))) {
	case Auto:
		return Auto, true
	case Conservative:
		return Conservative, true
	case Aggressive:
		return Aggressive, true
	default:
		return "", false
	}
}

func NormalizeMode(value string) (Mode, bool) {
	if mode, ok := NormalizeRuntimeMode(value); ok {
		return mode, true
	}
	if Mode(strings.ToLower(strings.TrimSpace(value))) == Off {
		return Off, true
	}
	return "", false
}
