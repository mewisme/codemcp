package telegram

import (
	"fmt"
	"html"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	presentationMaxBytes = 3900
	presentationMaxValue = 512
	richMaxBlocks        = 24
	richMaxRows          = 12
	richMaxColumns       = 6
)

type RichBlockKind string

const (
	RichHeading  RichBlockKind = "heading"
	RichSection  RichBlockKind = "section"
	RichList     RichBlockKind = "list"
	RichTable    RichBlockKind = "table"
	RichDetails  RichBlockKind = "details"
	RichQuote    RichBlockKind = "quote"
	RichCode     RichBlockKind = "code"
	RichLink     RichBlockKind = "link"
	RichDocument RichBlockKind = "document"
	RichButtons  RichBlockKind = "buttons"
	RichCopy     RichBlockKind = "copy"
)

type RichBlock struct {
	Kind         RichBlockKind
	Title        string
	Text         string
	Items        []string
	Rows         [][]string
	LinkURL      string
	Buttons      [][]Button
	DocumentName string
	CopyText     string
}

type RichPresentation struct {
	Blocks []RichBlock
}

func BuildRichPresentation(blocks ...RichBlock) *RichPresentation {
	out := make([]RichBlock, 0, min(len(blocks), richMaxBlocks))
	for _, block := range blocks {
		if len(out) >= richMaxBlocks {
			break
		}
		block.Title = compactPresentationValue(block.Title)
		block.Text = compactPresentationValue(block.Text)
		block.CopyText = strings.TrimSpace(block.CopyText)
		if block.Kind == RichCopy {
			if block.CopyText == "" {
				block.CopyText = block.Text
			}
			if !validCopyText(block.CopyText) {
				block.CopyText = ""
			}
		}
		if len(block.Items) > richMaxRows {
			block.Items = block.Items[:richMaxRows]
		}
		if len(block.Rows) > richMaxRows {
			block.Rows = block.Rows[:richMaxRows]
		}
		for i := range block.Rows {
			if len(block.Rows[i]) > richMaxColumns {
				block.Rows[i] = block.Rows[i][:richMaxColumns]
			}
		}
		if len(block.Buttons) > maxActionGroupRows {
			block.Buttons = block.Buttons[:maxActionGroupRows]
		}
		for i := range block.Buttons {
			if len(block.Buttons[i]) > maxActionButtonsPerRow {
				block.Buttons[i] = block.Buttons[i][:maxActionButtonsPerRow]
			}
		}
		if block.Kind == RichLink && block.LinkURL != "" {
			parsed, err := url.Parse(block.LinkURL)
			if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
				block.LinkURL = ""
			}
		}
		out = append(out, block)
	}
	return &RichPresentation{Blocks: out}
}

func RichFallback(rich *RichPresentation) Presentation {
	if rich == nil {
		return Presentation{}
	}
	parts := make([]PresentationPart, 0, len(rich.Blocks))
	for _, block := range rich.Blocks {
		switch block.Kind {
		case RichHeading, RichSection:
			parts = append(parts, TitleBlock(block.Title, block.Text))
		case RichList:
			items := make([]ListItem, 0, len(block.Items))
			for _, item := range block.Items {
				items = append(items, ListItem{Label: item})
			}
			parts = append(parts, CompactList(items...))
		case RichTable:
			items := make([]MetadataItem, 0, len(block.Rows))
			for _, row := range block.Rows {
				if len(row) == 0 {
					continue
				}
				label, value := row[0], ""
				if len(row) > 1 {
					value = strings.Join(row[1:], " · ")
				}
				items = append(items, MetadataItem{Label: label, Value: value})
			}
			parts = append(parts, MetadataBlock(items...))
		case RichQuote:
			text := "> " + strings.ReplaceAll(block.Text, "\n", "\n> ")
			parts = append(parts, PresentationPart{Text: text, HTML: SafeHTML("<blockquote>" + EscapeText(block.Text) + "</blockquote>")})
		case RichCode:
			parts = append(parts, PresentationPart{Text: block.Text, HTML: SafeHTML("<pre>" + EscapeText(block.Text) + "</pre>")})
		case RichLink:
			label := block.Title
			if label == "" {
				label = block.LinkURL
			}
			parts = append(parts, PresentationPart{Text: label + ": " + block.LinkURL, HTML: SafeHTML("<a href=\"" + EscapeText(block.LinkURL) + "\">" + EscapeText(label) + "</a>")})
		case RichDetails:
			parts = append(parts, DetailBlock(block.Title, block.Text))
		case RichDocument:
			parts = append(parts, MetadataBlock(MetadataItem{Label: "Document", Value: block.DocumentName}, MetadataItem{Label: "Detail", Value: block.Text}))
		case RichButtons:
			labels := make([]ListItem, 0)
			for _, row := range block.Buttons {
				for _, button := range row {
					if strings.TrimSpace(button.Text) != "" {
						labels = append(labels, ListItem{Label: button.Text})
					}
				}
			}
			parts = append(parts, CompactList(labels...))
		case RichCopy:
			label := strings.TrimSpace(block.Title)
			if label == "" {
				label = "Value"
			}
			value := strings.TrimSpace(block.Text)
			if value == "" {
				value = strings.TrimSpace(block.CopyText)
			}
			parts = append(parts, PresentationPart{
				Text: label + ": " + value,
				HTML: SafeHTML("<b>" + EscapeText(label) + ":</b> <code>" + EscapeText(value) + "</code>"),
			})
		}
	}
	return Present(parts...)
}

func RichMessageHTML(rich *RichPresentation) SafeHTML {
	if rich == nil {
		return ""
	}
	parts := make([]string, 0, len(rich.Blocks))
	bytes := 0
	for _, block := range rich.Blocks {
		rendered := richBlockHTML(block)
		if rendered == "" {
			continue
		}
		separator := 0
		if len(parts) > 0 {
			separator = 1
		}
		if bytes+separator+len(rendered) > presentationMaxBytes {
			break
		}
		parts = append(parts, rendered)
		bytes += separator + len(rendered)
	}
	return SafeHTML(strings.Join(parts, "\n"))
}

func richBlockHTML(block RichBlock) string {
	switch block.Kind {
	case RichHeading:
		return richHeadingHTML("h2", block.Title, block.Text)
	case RichSection:
		return richHeadingHTML("h3", block.Title, block.Text)
	case RichList:
		items := make([]string, 0, len(block.Items))
		for _, item := range block.Items {
			item = strings.TrimSpace(item)
			if item != "" {
				items = append(items, "<li>"+richInlineHTML(item)+"</li>")
			}
		}
		if len(items) == 0 {
			return ""
		}
		return "<ul>" + strings.Join(items, "") + "</ul>"
	case RichTable:
		rows := make([]string, 0, len(block.Rows))
		for _, row := range block.Rows {
			if len(row) == 0 {
				continue
			}
			cells := make([]string, 0, len(row))
			for index, cell := range row {
				tag := "td"
				if index == 0 {
					tag = "th"
				}
				cells = append(cells, "<"+tag+">"+richInlineHTML(cell)+"</"+tag+">")
			}
			rows = append(rows, "<tr>"+strings.Join(cells, "")+"</tr>")
		}
		if len(rows) == 0 {
			return ""
		}
		return "<table compact>" + strings.Join(rows, "") + "</table>"
	case RichDetails:
		title, text := strings.TrimSpace(block.Title), strings.TrimSpace(block.Text)
		if title == "" {
			return richParagraphHTML(text)
		}
		body := ""
		if text != "" {
			body = richParagraphHTML(text)
		}
		return "<details open><summary>" + richInlineHTML(title) + "</summary>" + body + "</details>"
	case RichQuote:
		text := strings.TrimSpace(block.Text)
		if text == "" {
			return ""
		}
		return "<blockquote>" + richInlineHTML(text) + "</blockquote>"
	case RichCode:
		text := strings.TrimSpace(block.Text)
		if text == "" {
			return ""
		}
		return "<pre><code>" + EscapeText(text) + "</code></pre>"
	case RichLink:
		link := strings.TrimSpace(block.LinkURL)
		if link == "" {
			return ""
		}
		label := strings.TrimSpace(block.Title)
		if label == "" {
			label = link
		}
		return "<p><a href=\"" + EscapeText(link) + "\">" + richInlineHTML(label) + "</a></p>"
	case RichDocument:
		name := strings.TrimSpace(block.DocumentName)
		detail := strings.TrimSpace(block.Text)
		if name == "" && detail == "" {
			return ""
		}
		content := ""
		if name != "" {
			content = "<b>Document:</b> " + richInlineHTML(name)
		}
		if detail != "" {
			if content != "" {
				content += "<br>"
			}
			content += richInlineHTML(detail)
		}
		return "<p>" + content + "</p>"
	case RichButtons:
		items := make([]string, 0)
		for _, row := range block.Buttons {
			for _, button := range row {
				if label := strings.TrimSpace(button.Text); label != "" {
					items = append(items, "<li>"+richInlineHTML(label)+"</li>")
				}
			}
		}
		if len(items) == 0 {
			return ""
		}
		return "<ul>" + strings.Join(items, "") + "</ul>"
	case RichCopy:
		label := strings.TrimSpace(block.Title)
		if label == "" {
			label = "Value"
		}
		value := strings.TrimSpace(block.Text)
		if value == "" {
			value = strings.TrimSpace(block.CopyText)
		}
		if value == "" {
			return ""
		}
		if validCopyText(block.CopyText) {
			return "<p><b>" + richInlineHTML(label) + ":</b> <tg-button type=\"copy_text\" text=\"" + EscapeText(block.CopyText) + "\">" + richInlineHTML(value) + "</tg-button></p>"
		}
		return "<p><b>" + richInlineHTML(label) + ":</b> <code>" + richInlineHTML(value) + "</code></p>"
	default:
		return ""
	}
}

func richHeadingHTML(tag, title, subtitle string) string {
	title, subtitle = strings.TrimSpace(title), strings.TrimSpace(subtitle)
	parts := make([]string, 0, 2)
	if title != "" {
		parts = append(parts, "<"+tag+">"+richInlineHTML(title)+"</"+tag+">")
	}
	if subtitle != "" {
		parts = append(parts, richParagraphHTML(subtitle))
	}
	return strings.Join(parts, "")
}

func richParagraphHTML(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return "<p>" + richInlineHTML(text) + "</p>"
}

func richInlineHTML(text string) string {
	return strings.ReplaceAll(EscapeText(strings.TrimSpace(text)), "\n", "<br>")
}

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
		return "~"
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
