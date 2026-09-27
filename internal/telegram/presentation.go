package telegram

import (
	"fmt"
	"html"
	"strings"
	"unicode/utf8"
)

const (
	presentationMaxBytes = 3900
	presentationMaxValue = 512
)

type PresentationTone string

const (
	ToneHealthy     PresentationTone = "healthy"
	ToneWarning     PresentationTone = "warning"
	ToneStopped     PresentationTone = "stopped"
	TonePending     PresentationTone = "pending"
	ToneSuccess     PresentationTone = "success"
	ToneFailure     PresentationTone = "failure"
	ToneSecurity    PresentationTone = "security"
	ToneDestructive PresentationTone = "destructive"
)

type SafeHTML string

type PresentationPart struct {
	Text string
	HTML SafeHTML
}

type Presentation struct {
	Text string
	HTML SafeHTML
}

type MetadataItem struct {
	Label string
	Value string
	Code  bool
}

type ListItem struct {
	Label  string
	Detail string
	Tone   PresentationTone
}

func Present(parts ...PresentationPart) Presentation {
	plain, rich := make([]string, 0, len(parts)), make([]string, 0, len(parts))
	bytes := 0
	for _, part := range parts {
		text, richText := strings.TrimSpace(part.Text), strings.TrimSpace(string(part.HTML))
		if text == "" && richText == "" {
			continue
		}
		if richText == "" {
			richText = EscapeText(text)
		}
		separator := 0
		if len(rich) > 0 {
			separator = 2
		}
		if bytes+separator+len(richText) > presentationMaxBytes {
			plain = append(plain, "Additional details omitted.")
			rich = append(rich, "<i>Additional details omitted.</i>")
			break
		}
		plain, rich = append(plain, text), append(rich, richText)
		bytes += separator + len(richText)
	}
	return Presentation{Text: strings.Join(plain, "\n\n"), HTML: SafeHTML(strings.Join(rich, "\n\n"))}
}

func EscapeText(value string) string { return html.EscapeString(value) }

func ProductHeader(product, context string) PresentationPart {
	product, context = compactPresentationValue(product), compactPresentationValue(context)
	text, rich := product, "<b>"+EscapeText(product)+"</b>"
	if context != "" {
		text += "\n" + context
		rich += "\n<i>" + EscapeText(context) + "</i>"
	}
	return PresentationPart{Text: text, HTML: SafeHTML(rich)}
}

func TitleBlock(title, subtitle string) PresentationPart {
	title, subtitle = compactPresentationValue(title), compactPresentationValue(subtitle)
	text, rich := title, "<b>"+EscapeText(title)+"</b>"
	if subtitle != "" {
		text += "\n" + subtitle
		rich += "\n" + EscapeText(subtitle)
	}
	return PresentationPart{Text: text, HTML: SafeHTML(rich)}
}

func StatusRow(tone PresentationTone, label, detail string) PresentationPart {
	label, detail = compactPresentationValue(label), compactPresentationValue(detail)
	prefix := presentationToneIcon(tone)
	text, rich := strings.TrimSpace(prefix+" "+label), EscapeText(prefix)+" <b>"+EscapeText(label)+"</b>"
	if detail != "" {
		text += " - " + detail
		rich += " - " + EscapeText(detail)
	}
	return PresentationPart{Text: text, HTML: SafeHTML(rich)}
}

func MetadataBlock(items ...MetadataItem) PresentationPart {
	if len(items) > 12 {
		items = items[:12]
	}
	plain, rich := []string{}, []string{}
	for _, item := range items {
		label, value := compactPresentationValue(item.Label), compactPresentationValue(item.Value)
		if label == "" || value == "" {
			continue
		}
		plain = append(plain, label+": "+value)
		rendered := EscapeText(value)
		if item.Code {
			rendered = "<code>" + rendered + "</code>"
		}
		rich = append(rich, "<b>"+EscapeText(label)+":</b> "+rendered)
	}
	return PresentationPart{Text: strings.Join(plain, "\n"), HTML: SafeHTML(strings.Join(rich, "\n"))}
}

func CompactList(items ...ListItem) PresentationPart {
	if len(items) > 20 {
		items = items[:20]
	}
	plain, rich := []string{}, []string{}
	for _, item := range items {
		label, detail := compactPresentationValue(item.Label), compactPresentationValue(item.Detail)
		if label == "" {
			continue
		}
		prefix := presentationToneIcon(item.Tone)
		line, richLine := strings.TrimSpace(prefix+" "+label), EscapeText(prefix)+" <b>"+EscapeText(label)+"</b>"
		if detail != "" {
			line += " - " + detail
			richLine += " - " + EscapeText(detail)
		}
		plain, rich = append(plain, line), append(rich, richLine)
	}
	return PresentationPart{Text: strings.Join(plain, "\n"), HTML: SafeHTML(strings.Join(rich, "\n"))}
}

func LoadingState(message string) PresentationPart { return StatusRow(TonePending, "Working", message) }
func SuccessState(message string) PresentationPart {
	return StatusRow(ToneSuccess, "Completed", message)
}
func ErrorState(message string) PresentationPart { return StatusRow(ToneFailure, "Failed", message) }

func DetailBlock(title, detail string) PresentationPart {
	title = compactPresentationValue(title)
	detail = compactPresentationValue(detail)
	if title == "" {
		return PresentationPart{Text: detail, HTML: SafeHTML(EscapeText(detail))}
	}
	return PresentationPart{Text: title + "\n" + detail, HTML: SafeHTML("<b>" + EscapeText(title) + "</b>\n" + EscapeText(detail))}
}

func DestructiveConfirmation(title, target, detail string) PresentationPart {
	return StatusRow(ToneDestructive, title, strings.TrimSpace(target+" "+detail))
}

func PaginationFooter(page, pages int) PresentationPart {
	if pages < 1 {
		pages = 1
	}
	if page < 1 {
		page = 1
	}
	if page > pages {
		page = pages
	}
	value := fmt.Sprintf("Page %d/%d", page, pages)
	return PresentationPart{Text: value, HTML: SafeHTML("<i>" + EscapeText(value) + "</i>")}
}

func presentationToneIcon(tone PresentationTone) string {
	switch tone {
	case ToneHealthy:
		return "●"
	case ToneWarning:
		return "!"
	case ToneStopped:
		return "○"
	case TonePending:
		return "…"
	case ToneSuccess:
		return "✓"
	case ToneFailure:
		return "×"
	case ToneSecurity:
		return "◇"
	case ToneDestructive:
		return "▲"
	default:
		return "·"
	}
}

func compactPresentationValue(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n"))
	if utf8.RuneCountInString(value) <= presentationMaxValue {
		return value
	}
	runes := []rune(value)
	return string(runes[:presentationMaxValue-3]) + "..."
}
