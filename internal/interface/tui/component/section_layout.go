package component

import (
	"strings"

	"charm.land/lipgloss/v2"
)

type SectionLayout struct {
	Header     string
	BodyY      int
	BodyHeight int
}

func NewSectionLayout(title, meta, feedback string, width, height, footerHeight int) SectionLayout {
	width, height, footerHeight = max(1, width), max(1, height), max(0, footerHeight)
	parts := make([]string, 0, 3)
	if title = strings.TrimSpace(title); title != "" {
		parts = append(parts, TwoColumn(Title(title), Secondary(meta), width))
		parts = append(parts, "")
	} else if meta = strings.TrimSpace(meta); meta != "" {
		parts = append(parts, TwoColumn("", Secondary(meta), width))
	}
	if feedback = strings.TrimSpace(feedback); feedback != "" {
		parts = append(parts, feedback)
	}
	parts = append(parts, Divider(width))
	prefix := strings.Join(parts, "\n")
	prefixHeight := lipgloss.Height(prefix)
	return SectionLayout{Header: prefix, BodyY: prefixHeight, BodyHeight: max(1, height-prefixHeight-footerHeight)}
}

func (layout SectionLayout) View(body string) string { return layout.Header + "\n" + body }
