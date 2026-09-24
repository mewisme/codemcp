package cli

import (
	"io"
	"strings"

	"charm.land/glamour/v2"

	"go.mewis.me/codemcp/internal/cli/presentation"
)

const defaultMarkdownWidth = presentation.DefaultWidth

func renderMarkdown(writer io.Writer, source string) error {
	capabilities := presentation.DetectWriter(writer)
	options := []glamour.TermRendererOption{glamour.WithWordWrap(capabilities.Width)}
	if !capabilities.Color {
		options = append(options, glamour.WithStandardStyle("ascii"))
	} else {
		options = append(options, glamour.WithStandardStyle("dark"))
	}
	renderer, err := glamour.NewTermRenderer(options...)
	if err != nil {
		return err
	}
	output, err := renderer.Render(strings.TrimSpace(source) + "\n")
	if err != nil {
		return err
	}
	_, err = io.WriteString(writer, output)
	return err
}

func markdownWidth(writer io.Writer) int {
	return presentation.DetectWriter(writer).Width
}

func markdownTerminal(writer io.Writer) bool {
	return presentation.DetectWriter(writer).StdoutTTY
}

func markdownColor(writer io.Writer) bool { return presentation.DetectWriter(writer).Color }
