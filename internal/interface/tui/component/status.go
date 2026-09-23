package component

import (
	"strings"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
)

type OperationState uint8

const (
	OperationIdle OperationState = iota
	OperationRunning
	OperationSuccess
	OperationFailed
	OperationCancelled
)

type Progress struct {
	spinner spinner.Model
	state   OperationState
	title   string
	detail  string
}

func NewProgress(title string) Progress {
	model := spinner.New()
	return Progress{spinner: model, state: OperationRunning, title: strings.TrimSpace(title)}
}

func (progress Progress) Init() tea.Cmd { return progress.spinner.Tick }

func (progress Progress) Update(message tea.Msg) (Progress, tea.Cmd) {
	if progress.state != OperationRunning {
		return progress, nil
	}
	updated, cmd := progress.spinner.Update(message)
	progress.spinner = updated
	return progress, cmd
}

func (progress *Progress) Complete(detail string) {
	if progress == nil {
		return
	}
	progress.state, progress.detail = OperationSuccess, strings.TrimSpace(detail)
}

func (progress *Progress) Fail(detail string) {
	if progress == nil {
		return
	}
	progress.state, progress.detail = OperationFailed, strings.TrimSpace(detail)
}

func (progress *Progress) Cancel(detail string) {
	if progress == nil {
		return
	}
	progress.state, progress.detail = OperationCancelled, strings.TrimSpace(detail)
}

func (progress Progress) State() OperationState { return progress.state }

func (progress Progress) View() string {
	prefix := ""
	switch progress.state {
	case OperationRunning:
		prefix = progress.spinner.View()
	case OperationSuccess:
		prefix = ToneText("✓", ToneSuccess)
	case OperationFailed:
		prefix = ToneText("×", ToneDanger)
	case OperationCancelled:
		prefix = Muted("·")
	}
	line := strings.TrimSpace(prefix + " " + progress.title)
	if progress.detail != "" {
		line += "\n\n" + Muted(progress.detail)
	}
	return line
}

type PageState uint8

const (
	PageReady PageState = iota
	PageLoading
	PageEmpty
	PageError
)

func StateView(state PageState, title, detail string) string {
	title, detail = strings.TrimSpace(title), strings.TrimSpace(detail)
	switch state {
	case PageLoading:
		return Muted(title)
	case PageEmpty:
		return Muted(title + optionalDetail(detail))
	case PageError:
		return Banner(title+optionalDetail(detail), ToneDanger)
	default:
		return title + optionalDetail(detail)
	}
}

func optionalDetail(detail string) string {
	if detail == "" {
		return ""
	}
	return "\n" + detail
}
