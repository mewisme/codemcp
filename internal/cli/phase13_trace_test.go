package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func TestCompletionTraceKeepsMachineOutputClean(t *testing.T) {
	collector := &serverTraceCollector{}
	cmd := completionCommand()
	cmd.SetContext(tracepkg.WithObserver(context.Background(), collector.Observe))
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	if err := cmd.Flags().Set("go-run", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, []string{"bash"}); err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 || !strings.Contains(out.String(), "complete") {
		t.Fatalf("completion output is invalid: %q", out.String())
	}
	for _, traceName := range []string{"completion.generate", "completion.alias-transform", "completion.go-run-extension"} {
		if strings.Contains(out.String(), traceName) {
			t.Fatalf("completion trace leaked into stdout: %q", out.String())
		}
	}
	events := collector.Snapshot()
	if !serverTraceHasFields(events, "completion.generate.completed", map[string]any{"shell": "bash", "descriptions_enabled": true, "go_run_added": true, "output_bytes": out.Len()}) {
		t.Fatalf("missing completion output trace: %#v", events)
	}
	if !serverTraceHasFields(events, "completion.alias-transform", map[string]any{"shell": "bash", "transformed": true}) {
		t.Fatalf("missing completion alias transform trace: %#v", events)
	}
	if !serverTraceHasFields(events, "completion.go-run-extension", map[string]any{"shell": "bash", "requested": true, "supported": true, "added": true}) {
		t.Fatalf("missing go-run completion trace: %#v", events)
	}
}

func TestConfigExplainTraceReportsSchemaAndMarkdownRenderFacts(t *testing.T) {
	collector := &serverTraceCollector{}
	cmd := configExplainCommand()
	cmd.SetContext(tracepkg.WithObserver(context.Background(), collector.Observe))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"shell"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	events := collector.Snapshot()
	lookup, ok := serverTraceEvent(events, "config.explain.schema.completed")
	if !ok {
		t.Fatalf("missing config explain lookup trace: %#v", events)
	}
	childCount, _ := serverTraceField(lookup, "child_count")
	if fmt.Sprint(childCount) == "0" {
		t.Fatalf("config explain child_count=%v", childCount)
	}
	render, ok := serverTraceEvent(events, "config.explain.render.completed")
	if !ok {
		t.Fatalf("missing config explain render trace: %#v", events)
	}
	for key, want := range map[string]any{"mode": "markdown", "terminal_width": defaultMarkdownWidth, "style": "ascii"} {
		got, found := serverTraceField(render, key)
		if !found || fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("config explain %s=%v, want %v: %#v", key, got, want, render)
		}
	}
	markdownBytes, found := serverTraceField(render, "markdown_bytes")
	if !found || fmt.Sprint(markdownBytes) == "0" {
		t.Fatalf("config explain markdown_bytes=%v: %#v", markdownBytes, render)
	}
}

func TestStatusTraceCompletesSnapshotBeforeRenderingWhenUninitialized(t *testing.T) {
	previous := configformat.RootPath()
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	collector := &serverTraceCollector{}
	cmd := statusCommand()
	addConfigDirFlag(cmd)
	addLoggingFlags(cmd)
	cmd.SetContext(tracepkg.WithObserver(context.Background(), collector.Observe))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := runStatus(cmd, nil); err != nil {
		t.Fatal(err)
	}
	events := collector.Snapshot()
	if !serverTraceHasFields(events, "status.snapshot.completed", map[string]any{"initialized": false, "running": false}) {
		t.Fatalf("missing uninitialized status snapshot trace: %#v", events)
	}
	for _, name := range []string{"status.service-context.completed", "status.config.load.completed"} {
		if _, ok := serverTraceEvent(events, name); !ok {
			t.Fatalf("missing %s: %#v", name, events)
		}
	}
}
