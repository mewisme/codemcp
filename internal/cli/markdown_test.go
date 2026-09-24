package cli

import (
	"bytes"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/cli/presentation"
)

func TestMarkdownUsesInjectedTerminalCapabilities(t *testing.T) {
	var plain bytes.Buffer
	plainCaps := presentation.Capabilities{StdoutTTY: false, Width: 42, Color: false, Unicode: false}
	plainWriter := presentation.WrapWriter(&plain, plainCaps)
	if markdownWidth(plainWriter) != 42 || markdownTerminal(plainWriter) || markdownColor(plainWriter) {
		t.Fatalf("plain markdown capabilities width=%d terminal=%t color=%t", markdownWidth(plainWriter), markdownTerminal(plainWriter), markdownColor(plainWriter))
	}
	if err := renderMarkdown(plainWriter, "# Heading\n\nA short paragraph."); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.String(), "\x1b[") {
		t.Fatalf("plain markdown contains ANSI: %q", plain.String())
	}

	var styled bytes.Buffer
	styledCaps := presentation.Capabilities{StdoutTTY: true, Width: 73, Color: true, Unicode: true, Interactive: true}
	styledWriter := presentation.WrapWriter(&styled, styledCaps)
	if markdownWidth(styledWriter) != 73 || !markdownTerminal(styledWriter) || !markdownColor(styledWriter) {
		t.Fatalf("styled markdown capabilities width=%d terminal=%t color=%t", markdownWidth(styledWriter), markdownTerminal(styledWriter), markdownColor(styledWriter))
	}
	if err := renderMarkdown(styledWriter, "# Heading\n\nA short paragraph."); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(styled.String(), "\x1b[") {
		t.Fatalf("styled markdown did not use color capability: %q", styled.String())
	}
}

func TestMarkdownAndOrdinaryRenderingShareColorPolicy(t *testing.T) {
	var output bytes.Buffer
	caps := presentation.Capabilities{StdoutTTY: false, Width: 60, Color: false, Unicode: false}
	writer := presentation.WrapWriter(&output, caps)
	statusStateField(writer, "status", "connected")
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("ordinary rendering contains ANSI: %q", output.String())
	}
	output.Reset()
	if err := renderMarkdown(writer, "**connected**"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("markdown rendering disagrees with ordinary color policy: %q", output.String())
	}
}
