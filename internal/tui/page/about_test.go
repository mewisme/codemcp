package page

import (
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/application"
)

func TestAboutPageRendersBuildUptimeAndPaths(t *testing.T) {
	page, _ := NewAbout(t.Context())
	page.loaded = true
	page.info = application.AboutInfo{
		Version: "v1.2.3", Commit: "abc123", BuildTime: "2026-09-06T00:00:00Z", Executable: "/tmp/cm",
		ConfigPath: "/tmp/config.toml", ConfigRoot: "/tmp/config", LogsPath: "/tmp/runtime-events.jsonl",
		RuntimeRunning: true, ServerUptime: 2 * time.Minute, MachineUptime: time.Hour, MachineUptimeOK: true,
	}
	view := page.View(100, 30)
	for _, value := range []string{"v1.2.3", "abc123", "2m0s", "1h0m0s", "/tmp/cm", "/tmp/config.toml", "/tmp/runtime-events.jsonl"} {
		if !strings.Contains(view, value) {
			t.Fatalf("about view missing %q: %q", value, view)
		}
	}
	lines := strings.Split(view, "\n")
	last := len(lines) - 1
	for last >= 0 && strings.TrimSpace(lines[last]) == "" {
		last--
	}
	if last != 29 || !strings.Contains(lines[last], "refresh") {
		t.Fatalf("about help line=%d want=29 view=%q", last, view)
	}
}
