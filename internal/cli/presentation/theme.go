package presentation

import (
	"fmt"

	"github.com/fatih/color"
)

type Role uint8

const (
	RoleValue Role = iota
	RoleSuccess
	RoleDanger
	RoleWarning
	RoleAccent
	RoleMuted
	RoleHeading
	RoleLabel
)

type Theme struct {
	color bool
}

func NewTheme(capabilities Capabilities) Theme {
	return Theme{color: capabilities.Color}
}

func (theme Theme) ColorEnabled() bool { return theme.color }

func (theme Theme) Render(role Role, value any) string {
	text := fmt.Sprint(value)
	attributes := roleAttributes(role)
	if len(attributes) == 0 {
		return text
	}
	style := color.New(attributes...)
	if theme.color {
		style.EnableColor()
	} else {
		style.DisableColor()
	}
	return style.Sprint(text)
}

func roleAttributes(role Role) []color.Attribute {
	switch role {
	case RoleSuccess:
		return []color.Attribute{color.FgHiGreen, color.Bold}
	case RoleDanger:
		return []color.Attribute{color.FgHiRed, color.Bold}
	case RoleWarning:
		return []color.Attribute{color.FgHiYellow, color.Bold}
	case RoleAccent:
		return []color.Attribute{color.FgHiCyan, color.Bold}
	case RoleMuted, RoleLabel:
		return []color.Attribute{color.Faint}
	case RoleHeading:
		return []color.Attribute{color.Bold}
	default:
		return nil
	}
}
