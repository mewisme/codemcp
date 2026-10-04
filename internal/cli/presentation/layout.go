package presentation

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const minimumLayoutContentWidth = 1

type layoutContext struct {
	width              int
	prefix             string
	continuationPrefix string
}

func newLayoutContext(width int, prefix, continuationPrefix string) layoutContext {
	if width <= 0 {
		width = DefaultWidth
	}
	if continuationPrefix == "" {
		continuationPrefix = prefix
	}
	return layoutContext{width: width, prefix: prefix, continuationPrefix: continuationPrefix}
}

func (layout layoutContext) contentWidth() int {
	first := layout.width - displayWidth(layout.prefix)
	continuation := layout.width - displayWidth(layout.continuationPrefix)
	width := min(first, continuation)
	if width < minimumLayoutContentWidth {
		return minimumLayoutContentWidth
	}
	return width
}

func (layout layoutContext) render(content string, decorate func(string) string) []string {
	parts := wrapDisplayLines(content, layout.contentWidth())
	if len(parts) == 0 {
		parts = []string{""}
	}
	lines := make([]string, 0, len(parts))
	for index, part := range parts {
		prefix := layout.continuationPrefix
		if index == 0 {
			prefix = layout.prefix
		}
		if decorate != nil {
			part = decorate(part)
		}
		lines = append(lines, prefix+part)
	}
	return lines
}

func displayWidth(value string) int {
	return ansi.StringWidth(value)
}

func effectiveLayoutWidth(width int) int {
	if width <= 0 {
		return DefaultWidth
	}
	return width
}

func wrapDisplayLines(value string, width int) []string {
	if width < minimumLayoutContentWidth {
		width = minimumLayoutContentWidth
	}
	logical := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(logical))
	for _, item := range logical {
		item = strings.TrimSpace(item)
		if item == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, wrapDisplayWords(item, width)...)
	}
	return lines
}

func hardWrapDisplay(value string, width int) []string {
	if width < minimumLayoutContentWidth {
		width = minimumLayoutContentWidth
	}
	if displayWidth(value) <= width {
		return []string{value}
	}
	lines := make([]string, 0, 2)
	remaining := value
	for displayWidth(remaining) > width {
		total := displayWidth(remaining)
		part := ansi.Cut(remaining, 0, width)
		if part == "" {
			break
		}
		lines = append(lines, part)
		remaining = ansi.Cut(remaining, width, total)
	}
	if remaining != "" {
		lines = append(lines, remaining)
	}
	return lines
}

func wrapDisplayWords(value string, width int) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if width < minimumLayoutContentWidth {
		width = minimumLayoutContentWidth
	}
	if displayWidth(value) <= width {
		return []string{value}
	}
	words := strings.Fields(value)
	lines := make([]string, 0, len(words))
	current := ""
	for _, word := range words {
		for displayWidth(word) > width {
			if current != "" {
				lines = append(lines, current)
				current = ""
			}
			wordWidth := displayWidth(word)
			part := ansi.Cut(word, 0, width)
			if part == "" {
				break
			}
			lines = append(lines, part)
			word = ansi.Cut(word, width, wordWidth)
		}
		if word == "" {
			continue
		}
		candidate := word
		if current != "" {
			candidate = current + " " + word
		}
		if displayWidth(candidate) <= width {
			current = candidate
			continue
		}
		if current != "" {
			lines = append(lines, current)
		}
		current = word
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}
