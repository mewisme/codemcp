package page

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"go.mewis.me/codemcp/internal/interface/tui/component"
)

type Model interface {
	Init() tea.Cmd
	Update(tea.Msg) (Model, tea.Cmd)
	View(width, height int) string
	OverlayActive() bool
	InputActive() bool
}

type NoticeModel interface {
	Model
	Notice() string
	SetNotice(string)
}

type ToastNoticeModel interface {
	NoticeModel
	ShouldToastNotice() bool
}

type NavigationGuardModel interface {
	Model
	Dirty() bool
	Submitting() bool
}

type SessionViewStateModel interface {
	Model
	SessionViewState() any
	RestoreSessionViewState(any)
}

type NavigateMsg struct {
	Path         []string
	Replace      bool
	PreservePage bool
}

type ToastMsg struct {
	Title   string
	Message string
	Tone    component.Tone
}

func pageFeedbackHeight(value string) int {
	if strings.TrimSpace(value) == "" {
		return 0
	}
	return lipgloss.Height(value)
}

func prependPageFeedback(feedback, content string) string {
	if strings.TrimSpace(feedback) == "" {
		return content
	}
	return feedback + "\n" + content
}
