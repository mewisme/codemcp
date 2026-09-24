package cli

import (
	"bytes"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/cli/presentation"
)

func TestCLIVisualHierarchyAndNoColor(t *testing.T) {
	var output bytes.Buffer
	styled := presentation.WrapWriter(&output, presentation.Capabilities{Color: true, Unicode: true})
	statusField(styled, "session", "run_test")
	statusStateField(styled, "status", "connected")
	text := output.String()
	if !strings.Contains(text, "\x1b[2m") || !strings.Contains(text, "\x1b[92;1mconnected") || !strings.Contains(text, "run_test") {
		t.Fatalf("styled status = %q", text)
	}

	output.Reset()
	plain := presentation.WrapWriter(&output, presentation.Capabilities{Color: false, Unicode: true})
	statusField(plain, "session", "run_test")
	statusStateField(plain, "status", "connected")
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("plain status contains ANSI: %q", output.String())
	}
}

func TestCLIASCIIStatusUsesFallbackGlyphs(t *testing.T) {
	var output bytes.Buffer
	writer := presentation.WrapWriter(&output, presentation.Capabilities{Color: false, Unicode: false})
	renderTunnelStateLine(writer, "connected")
	if text := output.String(); !strings.Contains(text, "[OK] OpenAI Secure MCP Tunnel is connected") || strings.ContainsAny(text, "✓×⠋│◆◇├└─") {
		t.Fatalf("ASCII status output = %q", text)
	}
}
