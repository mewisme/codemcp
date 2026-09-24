package presentation

import (
	"strings"
	"testing"
)

func TestThemeUsesSemanticColorCapability(t *testing.T) {
	plain := NewTheme(Capabilities{Color: false}).Render(RoleSuccess, "ready")
	if plain != "ready" || strings.Contains(plain, "\x1b[") {
		t.Fatalf("plain theme output = %q", plain)
	}
	styled := NewTheme(Capabilities{Color: true}).Render(RoleSuccess, "ready")
	if !strings.Contains(styled, "\x1b[") || !strings.Contains(styled, "ready") {
		t.Fatalf("colored theme output = %q", styled)
	}
}

func TestThemeValueRoleNeverDecorates(t *testing.T) {
	if got := NewTheme(Capabilities{Color: true}).Render(RoleValue, "value"); got != "value" {
		t.Fatalf("value role = %q", got)
	}
}
