package telegram

import (
	"errors"
	"strings"
)

type Screen struct {
	Text     string
	HTML     SafeHTML
	Keyboard [][]Button
}

type Button struct {
	Text         string
	CallbackData string
	URL          string
	Disabled     bool
}

func screenText(screen Screen) string {
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
			if len(button.CallbackData) > MaxCallbackDataBytes {
				return errors.New("telegram callback data exceeds 64 bytes")
			}
			if button.CallbackData != "" && button.URL != "" {
				return errors.New("telegram button cannot contain both callback data and URL")
			}
		}
	}
	return nil
}
