package page

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/interface/tui/component"
	"go.mewis.me/codemcp/internal/logger"
	runtimeevent "go.mewis.me/codemcp/internal/runtime/event"
)

type logsDisplayView string

const (
	logsViewBrowser  logsDisplayView = "browser"
	logsViewTimeline logsDisplayView = "timeline"
)

type logsTimelineState struct {
	viewport viewport.Model
	render   executionFeedRender
}

func newLogsTimelineState() logsTimelineState {
	view := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	view.SoftWrap = false
	view.FillHeight = false
	return logsTimelineState{viewport: view}
}

func normalizeLogsDisplayView(value logsDisplayView) logsDisplayView {
	if value == logsViewTimeline {
		return logsViewTimeline
	}
	return logsViewBrowser
}

func (page *LogsPage) toggleLogsView() {
	if page.view == logsViewTimeline {
		page.view = logsViewBrowser
	} else {
		page.view = logsViewTimeline
	}
	page.refreshActiveLogsView()
}

func (page *LogsPage) refreshActiveLogsView() {
	if page == nil || page.resourceID != "" {
		return
	}
	switch page.tab {
	case logsTabRuntime:
		if page.view == logsViewTimeline {
			page.refreshRuntimeTimeline()
		} else {
			_ = page.rebuildBrowser(page.selectedID())
		}
	case logsTabCommandExec:
		if page.view == logsViewTimeline {
			page.refreshExecutionViewport()
		} else {
			page.rebuildExecutionBrowser()
		}
	case logsTabToolCalls:
		page.refreshToolCallView()
	}
}

func (page *LogsPage) resizeRuntimeTimeline(width, height int) {
	width, height = max(1, width), max(1, height)
	offset := page.timeline.viewport.YOffset()
	if page.timeline.viewport.Width() != width {
		page.timeline.viewport.SetWidth(width)
		page.timeline.render = renderRuntimeTimeline(page.visibleRuntimeEvents(), width, page.visibility)
		page.timeline.viewport.SetContent(page.timeline.render.Content)
	}
	page.timeline.viewport.SetHeight(height)
	if !page.paused {
		page.timeline.viewport.GotoBottom()
		return
	}
	page.timeline.viewport.SetYOffset(min(offset, max(0, page.timeline.viewport.TotalLineCount()-page.timeline.viewport.Height())))
}

func (page *LogsPage) refreshRuntimeTimeline() {
	if page == nil {
		return
	}
	offset := page.timeline.viewport.YOffset()
	page.timeline.render = renderRuntimeTimeline(page.visibleRuntimeEvents(), max(1, page.timeline.viewport.Width()), page.visibility)
	page.timeline.viewport.SetContent(page.timeline.render.Content)
	if !page.paused {
		page.timeline.viewport.GotoBottom()
		return
	}
	page.timeline.viewport.SetYOffset(min(offset, max(0, page.timeline.viewport.TotalLineCount()-page.timeline.viewport.Height())))
}

func (page *LogsPage) runtimeTimelineBody(width, height int) string {
	page.resizeRuntimeTimeline(width, height)
	sticky := stickyLogHeader(page.timeline.render, page.timeline.viewport.YOffset(), width)
	if sticky != "" {
		page.resizeRuntimeTimeline(width, max(1, height-1))
		sticky = stickyLogHeader(page.timeline.render, page.timeline.viewport.YOffset(), width)
	}
	body := page.timeline.viewport.View()
	if len(page.visibleRuntimeEvents()) == 0 {
		empty := page.timeline.viewport
		empty.SetContent(component.Muted("Waiting for runtime events"))
		body = empty.View()
	}
	if sticky != "" {
		return sticky + "\n" + body
	}
	return body
}

func (page *LogsPage) visibleRuntimeEvents() []runtimeevent.Event {
	if page == nil {
		return nil
	}
	result := make([]runtimeevent.Event, 0, len(page.events))
	for _, event := range page.events {
		if isToolCallRuntimeEvent(event) || !page.matchLogsScope(event.WorkspaceID) {
			continue
		}
		result = append(result, event)
	}
	return result
}

func renderRuntimeTimeline(events []runtimeevent.Event, width int, visibility logger.Visibility) executionFeedRender {
	rendered := executionFeedRender{}
	var output strings.Builder
	line := 0
	for index, event := range events {
		if index > 0 {
			output.WriteByte('\n')
			line++
		}
		clock := event.Time.Local().Format("15:04:05.000")
		top := strings.TrimSpace("EVENT " + clock)
		bottom := strings.ToUpper(strings.TrimSpace(event.Level))
		if bottom == "" {
			bottom = "EVENT"
		}
		fields := []logFrameField{}
		if event.Component != "" {
			fields = append(fields, logFrameField{Label: "Component", Values: []string{event.Component}})
		}
		if event.WorkspaceID != "" {
			fields = append(fields, logFrameField{Label: "Workspace", Values: []string{event.WorkspaceID}})
		}
		if event.Status != "" {
			fields = append(fields, logFrameField{Label: "Status", Values: []string{event.Status}})
		}
		for _, field := range application.LogFields(event, visibility) {
			fields = append(fields, logFrameField{Label: field.Key, Values: []string{fmt.Sprint(field.Value)}})
		}
		content := []string{}
		if event.Message != "" {
			content = append(content, event.Message)
		}
		if event.Error != "" {
			content = append(content, "ERROR\n"+event.Error)
		}
		if len(content) == 0 {
			content = append(content, event.Name)
		}
		block := renderLogBlock(top, bottom, "Event", event.Name, fields, content, nil, width)
		start := line
		bodyStart := start
		for blockLine, value := range strings.Split(strings.TrimSuffix(block, "\n"), "\n") {
			if strings.HasPrefix(value, "├") {
				bodyStart = start + blockLine + 1
				break
			}
		}
		lines := strings.Count(block, "\n")
		line += lines
		output.WriteString(block)
		sticky := strings.Join([]string{"EVENT", clock, event.Name}, " · ")
		rendered.Segments = append(rendered.Segments, executionRenderedSegment{StartLine: start, BodyStartLine: bodyStart, EndLine: line, StickyLabel: sticky})
	}
	rendered.Content = output.String()
	return rendered
}

func stickyLogHeader(render executionFeedRender, top, width int) string {
	for _, segment := range render.Segments {
		if top >= segment.BodyStartLine && top < segment.EndLine {
			return executionStickyFrame(segment.StickyLabel, width)
		}
	}
	return ""
}

func isToolCallRuntimeEvent(event runtimeevent.Event) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(event.Name)), "tool.call.")
}

type logsTimelineMouseMsg struct {
	Tab   logsTab
	Wheel int
}

func (page *LogsPage) handleTimelineMouse(msg logsTimelineMouseMsg) {
	if page == nil || msg.Wheel == 0 {
		return
	}
	switch msg.Tab {
	case logsTabRuntime:
		if msg.Wheel < 0 {
			page.timeline.viewport.ScrollUp(3)
		} else {
			page.timeline.viewport.ScrollDown(3)
		}
		page.paused = !page.timeline.viewport.AtBottom()
	case logsTabToolCalls:
		if msg.Wheel < 0 {
			page.tools.viewport.ScrollUp(3)
		} else {
			page.tools.viewport.ScrollDown(3)
		}
		page.tools.paused = !page.tools.viewport.AtBottom()
	}
}

func timelineMouseTarget(tab logsTab, originX, originY, z, width, height int) component.MouseTarget {
	return component.MouseTarget{
		ID: "logs.timeline.scroll", Rect: component.Rect{X: originX, Y: originY, Width: width, Height: height}, Z: z,
		Handle: func(event component.MouseEvent) tea.Msg {
			switch event.Button {
			case tea.MouseWheelUp:
				return logsTimelineMouseMsg{Tab: tab, Wheel: -1}
			case tea.MouseWheelDown:
				return logsTimelineMouseMsg{Tab: tab, Wheel: 1}
			default:
				return nil
			}
		},
	}
}
