package component

import "strings"

type ToastDialog struct {
	Title   string
	Message string
	Tone    Tone
}

func NewToastDialog(title, message string, tone Tone) ToastDialog {
	return ToastDialog{Title: strings.TrimSpace(title), Message: strings.TrimSpace(message), Tone: tone}
}

func (dialog ToastDialog) View() string { return dialog.ViewWidth(0) }

func (dialog ToastDialog) ViewWidth(width int) string {
	title := dialog.Title
	if title == "" {
		title = "Notification"
	}
	heading := Title(title)
	if marker := toastToneMarker(dialog.Tone); marker != "" {
		heading = ToneText(marker, dialog.Tone) + " " + heading
	}
	if width > 0 {
		heading = WrapContent(heading, width)
	}
	message := dialog.Message
	if width > 0 {
		message = WrapContent(message, width)
	}
	if message == "" {
		return heading + "\n\n" + dialog.CloseButtonView()
	}
	return heading + "\n\n" + message + "\n\n" + dialog.CloseButtonView()
}

func (dialog ToastDialog) CloseButtonView() string { return Button("Close") }

func toastToneMarker(tone Tone) string {
	switch tone {
	case ToneSuccess:
		return "✓"
	case ToneWarning:
		return "!"
	case ToneDanger:
		return "×"
	case ToneAccent:
		return "·"
	default:
		return ""
	}
}
