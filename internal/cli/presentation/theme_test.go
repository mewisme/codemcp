package presentation

import (
	"slices"
	"strings"
	"testing"

	"github.com/fatih/color"
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

func TestThemeSeparatesRailStructureActiveAndSemanticRoles(t *testing.T) {
	tests := []struct {
		name string
		role Role
		want []color.Attribute
	}{
		{name: "structure", role: RoleStructure, want: []color.Attribute{color.FgGreen}},
		{name: "rail", role: RoleRail, want: []color.Attribute{color.Faint}},
		{name: "active", role: RoleActive, want: []color.Attribute{color.FgHiCyan}},
		{name: "success", role: RoleSuccess, want: []color.Attribute{color.FgHiGreen, color.Bold}},
		{name: "danger", role: RoleDanger, want: []color.Attribute{color.FgHiRed, color.Bold}},
		{name: "warning", role: RoleWarning, want: []color.Attribute{color.FgHiYellow, color.Bold}},
		{name: "muted", role: RoleMuted, want: []color.Attribute{color.Faint}},
		{name: "heading", role: RoleHeading, want: []color.Attribute{color.Bold}},
		{name: "label", role: RoleLabel, want: []color.Attribute{color.Faint}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := roleAttributes(test.role); !slices.Equal(got, test.want) {
				t.Fatalf("role attributes = %#v, want %#v", got, test.want)
			}
		})
	}
	if got := roleAttributes(RoleValue); len(got) != 0 {
		t.Fatalf("value role attributes = %#v", got)
	}
}
