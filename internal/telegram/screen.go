package telegram

import (
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	MaxCopyTextBytes       = 512
	MaxCopyTextRunes       = 256
	MaxButtonURLBytes      = 2048
	maxActionButtonsPerRow = 3
)

type ButtonStyle string

const (
	ButtonStylePrimary ButtonStyle = "primary"
	ButtonStyleSuccess ButtonStyle = "success"
	ButtonStyleDanger  ButtonStyle = "danger"
)

type ButtonRole string

const (
	ButtonRoleNeutral     ButtonRole = "neutral"
	ButtonRoleNavigation  ButtonRole = "navigation"
	ButtonRoleView        ButtonRole = "view"
	ButtonRoleCopy        ButtonRole = "copy"
	ButtonRoleConfigure   ButtonRole = "configure"
	ButtonRolePrimary     ButtonRole = "primary"
	ButtonRolePositive    ButtonRole = "positive"
	ButtonRoleDestructive ButtonRole = "destructive"
	ButtonRoleResource    ButtonRole = "resource"
)

type Screen struct {
	Text     string
	HTML     SafeHTML
	Keyboard [][]Button
	Rich     *RichPresentation
}

type Button struct {
	Text         string
	CallbackData string
	URL          string
	CopyText     string
	WebAppURL    string
	Style        ButtonStyle
	Role         ButtonRole
	Disabled     bool
}

type ActionGroups struct {
	Primary     []Button
	Secondary   []Button
	Destructive []Button
	Navigation  []Button
}

func screenText(screen Screen) string {
	if screen.Rich != nil {
		fallback := RichFallback(screen.Rich)
		if fallback.HTML != "" {
			return string(fallback.HTML)
		}
		return EscapeText(fallback.Text)
	}
	if screen.HTML != "" {
		return string(screen.HTML)
	}
	return EscapeText(screen.Text)
}

func ErrorScreen(err error) Screen {
	detail := "Operation failed"
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		detail = compactPresentationValue(err.Error())
	}
	presentation := Present(ProductHeader("CodeMCP", "Telegram / Error"), TitleBlock("Operation failed", ""), ErrorState(detail))
	return Screen{Text: presentation.Text, HTML: presentation.HTML}
}

func StaleScreen() Screen {
	presentation := Present(ProductHeader("CodeMCP", "Telegram"), TitleBlock("Stale control", "The underlying resource changed."), ErrorState("Open the screen again before mutating state."))
	return Screen{Text: presentation.Text, HTML: presentation.HTML}
}

func validateKeyboard(rows [][]Button) error {
	for _, row := range rows {
		for _, button := range row {
			if strings.TrimSpace(button.Text) == "" {
				return errors.New("telegram button text is required")
			}
			if len(button.CallbackData) > MaxCallbackDataBytes {
				return errors.New("telegram callback data exceeds 64 bytes")
			}
			if button.CopyText != "" && !validCopyText(button.CopyText) {
				return errors.New("telegram copy text exceeds the bounded copy budget")
			}
			if len(button.URL) > MaxButtonURLBytes || len(button.WebAppURL) > MaxButtonURLBytes {
				return errors.New("telegram button URL exceeds the bounded URL budget")
			}
			if button.Style != "" && button.Style != ButtonStylePrimary && button.Style != ButtonStyleSuccess && button.Style != ButtonStyleDanger {
				return errors.New("unsupported telegram button style")
			}
			actions := 0
			for _, value := range []string{button.CallbackData, button.URL, button.CopyText, button.WebAppURL} {
				if strings.TrimSpace(value) != "" {
					actions++
				}
			}
			if button.Disabled {
				if actions != 0 {
					return errors.New("disabled telegram button cannot contain an action")
				}
				continue
			}
			if actions != 1 {
				return errors.New("enabled telegram button requires exactly one action")
			}
			if button.URL != "" && !validButtonURL(button.URL, false) {
				return errors.New("telegram button URL is invalid")
			}
			if button.WebAppURL != "" && !validButtonURL(button.WebAppURL, true) {
				return errors.New("telegram WebApp URL must use HTTPS")
			}
		}
	}
	return nil
}

func semanticButtonStyle(button Button) ButtonStyle {
	if button.Disabled || button.CopyText != "" || button.URL != "" || button.WebAppURL != "" {
		return ""
	}
	switch button.Role {
	case ButtonRoleNeutral, ButtonRoleNavigation, ButtonRoleView, ButtonRoleCopy, ButtonRoleConfigure, ButtonRoleResource:
		return ""
	case ButtonRolePrimary:
		return ButtonStylePrimary
	case ButtonRolePositive:
		return ButtonStyleSuccess
	case ButtonRoleDestructive:
		return ButtonStyleDanger
	}
	label := strings.ToLower(strings.TrimSpace(button.Text))
	if isNeutralButtonLabel(label) {
		return ""
	}
	if label == "use this private chat" || label == "retry" {
		return ButtonStylePrimary
	}
	if label == "confirm" && button.Style == ButtonStylePrimary {
		return ButtonStyleSuccess
	}
	if hasButtonVerb(label, "approve", "allow similar", "enable", "start", "save") {
		return ButtonStyleSuccess
	}
	if hasButtonVerb(label, "deny", "delete", "revoke", "remove", "disable", "stop", "clear", "purge", "detach", "uninitialize", "down", "rotate") {
		return ButtonStyleDanger
	}
	if button.Style == ButtonStyleSuccess || button.Style == ButtonStyleDanger || button.Style == ButtonStylePrimary {
		return button.Style
	}
	return ""
}

func BoundedActionGroups(groups ActionGroups) [][]Button {
	rows := make([][]Button, 0)
	appendGroup := func(buttons []Button) {
		row := make([]Button, 0, maxActionButtonsPerRow)
		for _, button := range buttons {
			if strings.TrimSpace(button.Text) == "" {
				continue
			}
			button.Text = CompactActionLabel(button.Text)
			row = append(row, button)
			if len(row) == maxActionButtonsPerRow {
				rows = append(rows, row)
				row = make([]Button, 0, maxActionButtonsPerRow)
			}
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
	}
	appendGroup(groups.Primary)
	appendGroup(groups.Secondary)
	appendGroup(groups.Destructive)
	appendGroup(groups.Navigation)
	return rows
}

func CopyValueButton(label, value string) (Button, bool) {
	label, value = strings.TrimSpace(label), strings.TrimSpace(value)
	if label == "" || !validCopyText(value) {
		return Button{}, false
	}
	return Button{Text: CompactActionLabel(label), CopyText: value, Role: ButtonRoleCopy}, true
}

func validCopyText(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= MaxCopyTextBytes && utf8.RuneCountInString(value) <= MaxCopyTextRunes
}

func SetupPrivateChatButton(callbackData string) Button {
	return Button{Text: "Use this private chat", CallbackData: strings.TrimSpace(callbackData), Role: ButtonRolePrimary}
}

func DisabledAction(label, reason string, nativeDisabled bool) (Button, PresentationPart) {
	label, reason = strings.TrimSpace(label), strings.TrimSpace(reason)
	explanation := StatusRow(ToneWarning, "Unavailable", reason)
	if label == "" || reason == "" || !nativeDisabled {
		return Button{}, explanation
	}
	return Button{Text: CompactActionLabel(label), Disabled: true, Role: ButtonRoleNeutral}, explanation
}

func ResourceButton(label, callbackData string) Button {
	return Button{Text: CompactResourceLabel(label), CallbackData: callbackData, Role: ButtonRoleResource}
}

func ResourceRows(buttons ...Button) [][]Button {
	rows := make([][]Button, 0, len(buttons))
	for _, button := range buttons {
		if strings.TrimSpace(button.Text) == "" || strings.TrimSpace(button.CallbackData) == "" {
			continue
		}
		button.Text = CompactResourceLabel(button.Text)
		button.Role = ButtonRoleResource
		button.Style = ""
		button.URL = ""
		button.CopyText = ""
		button.WebAppURL = ""
		button.Disabled = false
		rows = append(rows, []Button{button})
	}
	return rows
}

func CompactActionLabel(value string) string {
	return strings.TrimSpace(value)
}

func CompactResourceLabel(value string) string {
	return strings.TrimSpace(value)
}

func validButtonURL(value string, webApp bool) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" {
		return false
	}
	if webApp {
		return parsed.Scheme == "https" && parsed.Host != ""
	}
	return parsed.Scheme == "https" || parsed.Scheme == "http" || parsed.Scheme == "tg"
}

func hasButtonVerb(label string, verbs ...string) bool {
	for _, verb := range verbs {
		if label == verb || strings.HasPrefix(label, verb+" ") {
			return true
		}
	}
	return false
}

func isNeutralButtonLabel(label string) bool {
	switch label {
	case "back", "home", "refresh", "open", "details", "status", "system", "requests", "completions", "workspaces", "secure mcp tunnel", "upstreams", "integrations", "instructions", "settings", "auth", "logs", "help", "commands", "newer", "older", "previous", "next", "configure", "cancel", "close", "clear view":
		return true
	default:
		return strings.HasPrefix(label, "page ")
	}
}
