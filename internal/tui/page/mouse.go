package page

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"go.mewis.me/codemcp/internal/tui/component"
)

func overlayWidth(pageWidth, preferred int) int {
	return max(1, min(preferred, pageWidth-4))
}

func mouseBlocker(originX, originY, width, height, z int) component.MouseTarget {
	return component.MouseTarget{ID: "page.overlay", Rect: component.Rect{X: originX, Y: originY, Width: width, Height: height}, Z: z, Handle: func(component.MouseEvent) tea.Msg { return nil }}
}

func confirmOverlayBody(confirm component.ConfirmButtons, title, description string, modalWidth int) string {
	width := component.ModalContentWidth(modalWidth)
	return component.WrapContent(component.Title(title), width) + "\n\n" + component.WrapContent(component.Muted(description), width) + "\n\n" + confirm.View() + "\n" + component.WrapContent(component.Muted("Enter confirm · Esc cancel"), width)
}

func confirmOverlayMouseTargets(confirm component.ConfirmButtons, title, description string, modalWidth, pageWidth, pageHeight, originX, originY, z int) []component.MouseTarget {
	body := confirmOverlayBody(confirm, title, description, modalWidth)
	modal := component.Modal(body, modalWidth)
	targets, modalX, modalY := component.CenteredOverlayTargets(modal, pageWidth, pageHeight, originX, originY, z, tea.KeyPressMsg{Code: tea.KeyEscape})
	rect, ok := component.FindRenderedRect(modal, confirm.View())
	if !ok {
		return targets
	}
	return append(targets, confirm.MouseTargets(originX+modalX+rect.X, originY+modalY+rect.Y, z+2)...)
}

func dismissibleOverlayMouseTargets(body string, modalWidth, pageWidth, pageHeight, originX, originY, z int) []component.MouseTarget {
	modal := component.Modal(component.WrapModalBody(body, modalWidth), modalWidth)
	targets, _, _ := component.CenteredOverlayTargets(modal, pageWidth, pageHeight, originX, originY, z, tea.KeyPressMsg{Code: tea.KeyEscape})
	return targets
}

func keyHintMouseTargets(view string, bindings map[string]string, originX, originY, z int) []component.MouseTarget {
	targets := make([]component.MouseTarget, 0, len(bindings))
	for label, key := range bindings {
		if strings.TrimSpace(label) == "" || key == "" {
			continue
		}
		rect, ok := component.FindRenderedRect(view, label)
		if !ok {
			continue
		}
		keyValue := key
		targets = append(targets, component.MouseTarget{
			ID: "page.action", Rect: component.Rect{X: originX + rect.X, Y: originY + rect.Y, Width: rect.Width, Height: 1}, Z: z,
			Handle: func(event component.MouseEvent) tea.Msg {
				if event.Button != tea.MouseLeft {
					return nil
				}
				return pageActionKeyMsg(keyValue)
			},
		})
	}
	return targets
}

func pageActionKeyMsg(value string) tea.KeyPressMsg {
	switch value {
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace}
	default:
		runes := []rune(value)
		if len(runes) == 0 {
			return tea.KeyPressMsg{}
		}
		return tea.KeyPressMsg{Code: runes[0]}
	}
}
