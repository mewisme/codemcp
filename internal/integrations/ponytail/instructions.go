package ponytail

import (
	_ "embed"
	"regexp"
	"strings"
)

//go:embed instructions.md
var baseInstructions string

//go:embed review.md
var reviewInstructions string

var (
	tableModePattern   = regexp.MustCompile(`^\|\s*\*\*(.+?)\*\*\s*\|`)
	exampleModePattern = regexp.MustCompile(`^-\s*([^:]+):\s*"`)
)

func Instructions(mode Mode) string {
	if mode == Off {
		return ""
	}
	body := reviewInstructions
	if mode != Review {
		body = filterInstructionsForMode(baseInstructions, mode)
	}
	return "PONYTAIL MODE ACTIVE — level: " + string(mode) + "\n\n" + strings.TrimSpace(body)
}

func filterInstructionsForMode(body string, mode Mode) string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if match := tableModePattern.FindStringSubmatch(line); len(match) == 2 {
			if candidate, ok := NormalizeRuntimeMode(match[1]); ok && candidate != mode {
				continue
			}
		}
		if match := exampleModePattern.FindStringSubmatch(line); len(match) == 2 {
			if candidate, ok := NormalizeRuntimeMode(match[1]); ok && candidate != mode {
				continue
			}
		}
		filtered = append(filtered, line)
	}
	return strings.Join(filtered, "\n")
}
