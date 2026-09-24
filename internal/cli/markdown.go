package cli

import (
	"io"

	"go.mewis.me/codemcp/internal/cli/presentation"
)

const defaultMarkdownWidth = presentation.DefaultWidth

func renderMarkdown(writer io.Writer, source string) error {
	capabilities := presentation.DetectWriter(writer)
	return presentation.New(writer, presentation.ModeHuman, capabilities).Markdown(source)
}

func markdownWidth(writer io.Writer) int {
	return presentation.DetectWriter(writer).Width
}

func markdownTerminal(writer io.Writer) bool {
	return presentation.DetectWriter(writer).StdoutTTY
}

func markdownColor(writer io.Writer) bool { return presentation.DetectWriter(writer).Color }
