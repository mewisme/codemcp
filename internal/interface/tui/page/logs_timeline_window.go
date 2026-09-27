package page

const (
	logsTimelinePageSize        = 40
	logsTimelineNearOldestLines = 3
)

type logsTimelineWindow struct {
	end         int
	size        int
	initialized bool
}

func (window *logsTimelineWindow) invalidate() {
	if window == nil {
		return
	}
	*window = logsTimelineWindow{}
}

func (window *logsTimelineWindow) reset(total int) {
	if window == nil {
		return
	}
	window.end = max(0, total)
	window.size = min(logsTimelinePageSize, window.end)
	window.initialized = true
}

func (window *logsTimelineWindow) rangeFor(total int, following bool) (int, int) {
	if window == nil {
		return max(0, total-logsTimelinePageSize), max(0, total)
	}
	total = max(0, total)
	if !window.initialized || following {
		window.reset(total)
	}
	window.end = min(window.end, total)
	if window.size <= 0 && window.end > 0 {
		window.size = min(logsTimelinePageSize, window.end)
	}
	start := max(0, window.end-window.size)
	return start, window.end
}

func (window *logsTimelineWindow) expand(total int) bool {
	if window == nil {
		return false
	}
	start, _ := window.rangeFor(total, false)
	if start == 0 {
		return false
	}
	window.size += min(logsTimelinePageSize, start)
	return true
}

func cloneSequenceWatermarks(values map[string]uint64) map[string]uint64 {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]uint64, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func (page *LogsPage) invalidateActiveTimelineWindow() {
	if page == nil {
		return
	}
	switch page.tab {
	case logsTabCommandExec:
		page.exec.window.invalidate()
	case logsTabToolCalls:
		page.tools.window.invalidate()
	default:
		page.timeline.window.invalidate()
	}
}
