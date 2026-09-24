package cli

import (
	"fmt"
	"io"

	"go.mewis.me/codemcp/internal/cli/presentation"
)

func cliTheme(out io.Writer) presentation.Theme {
	return presentation.NewTheme(presentation.DetectWriter(out))
}

func cliGlyphs(out io.Writer) presentation.GlyphSet {
	return presentation.Glyphs(presentation.DetectWriter(out))
}

func cliSeparator(out io.Writer) string { return cliGlyphs(out).Separator }

func cliDim(out io.Writer, value any) string {
	return cliTheme(out).Render(presentation.RoleMuted, value)
}

func cliHeading(out io.Writer, value string) string {
	return cliTheme(out).Render(presentation.RoleHeading, value)
}

func cliTone(out io.Writer, role presentation.Role, value any) string {
	return cliTheme(out).Render(role, value)
}

func cliState(out io.Writer, value any) string {
	text := fmt.Sprint(value)
	switch text {
	case "connected", "ready", "running":
		return cliTone(out, presentation.RoleSuccess, text)
	case "connecting", "reconnecting":
		return cliTone(out, presentation.RoleAccent, text)
	case "unreachable", "failed", "error":
		return cliTone(out, presentation.RoleDanger, text)
	case "stopped", "offline", "disabled", "not configured":
		return cliDim(out, text)
	default:
		return text
	}
}
