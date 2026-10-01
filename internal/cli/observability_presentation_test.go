package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/runtime/activity"
	runtimeevent "go.mewis.me/codemcp/internal/runtime/event"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
)

func TestRestoredObservabilityCommandsLockTTYPlainAndJSONPresentation(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}

	toolCalls := activity.NewStream()
	toolCalls.Publish(activity.Event{
		CallID: "call_quality", Kind: string(activity.EventToolCall), Phase: "start",
		Method: "tools/call", Source: "mcp", Tool: "run_command", WorkspaceID: "ws_quality", Status: "running",
		Raw: map[string]any{
			"request": map[string]any{
				"method": "tools/call",
				"params": map[string]any{
					"name": "run_command",
					"arguments": map[string]any{
						"command": "printf quality",
						"token":   "private-marker",
					},
				},
			},
		},
	})
	toolCalls.Publish(activity.Event{
		CallID: "call_quality", Kind: string(activity.EventToolCall), Phase: "finish",
		Method: "tools/call", Source: "mcp", Tool: "run_command", WorkspaceID: "ws_quality", Status: "ok",
		Raw: map[string]any{
			"result": map[string]any{"stdout": "quality-output", "exit_code": 0},
		},
	})

	executions := shellruntime.NewExecutionHub()
	run := executions.Begin(shellruntime.ExecutionInput{
		WorkspaceID: "ws_quality",
		Tool:        "run_command",
		Command:     "printf execution",
		CWD:         "/workspace",
		Source:      "mcp",
	})
	_, _ = run.Writer("stdout").Write([]byte("execution-output\n"))
	exitCode := 0
	run.Finish(shellruntime.ExecutionStatusSuccess, &exitCode, false)

	control, err := startRuntimeControl(runtimeControlOptions{
		Activity:   toolCalls,
		Executions: executions,
		Events:     runtimeevent.NewStream(runtimeevent.Metadata{}),
		Reload: func(context.Context) (runtimeReloadResult, error) {
			return runtimeReloadResult{PID: os.Getpid()}, nil
		},
		Status:    func() runtimeStatusResult { return runtimeStatusResult{PID: os.Getpid()} },
		Shutdown:  func() {},
		ClearLogs: func() error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()

	for _, fixture := range []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "activity",
			args: []string{"activity", "view", "call_quality"},
			want: []string{"Tool-call activity", "Request", "printf quality", "Response", "quality-output"},
		},
		{
			name: "execution",
			args: []string{"execution", "view", "ws_quality", run.ID()},
			want: []string{"Execution details", "Request", "printf execution", "Response", "execution-output"},
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			plain := executeObservabilityPresentation(t, root, false, fixture.args...)
			for _, want := range fixture.want {
				if !strings.Contains(plain, want) {
					t.Fatalf("plain output missing %q: %q", want, plain)
				}
			}

			tty := executeObservabilityPresentation(t, root, true, fixture.args...)
			if !strings.Contains(tty, "┌") || !strings.Contains(tty, "└") {
				t.Fatalf("TTY output lost framed presentation: %q", tty)
			}
			for _, want := range fixture.want {
				if !strings.Contains(tty, want) {
					t.Fatalf("TTY output missing %q: %q", want, tty)
				}
			}

			jsonOutput := executeObservabilityPresentation(t, root, false, append(fixture.args, "--json")...)
			var decoded map[string]any
			if err := json.Unmarshal([]byte(strings.TrimSpace(jsonOutput)), &decoded); err != nil {
				t.Fatalf("JSON output is not canonical JSON: %v\n%s", err, jsonOutput)
			}
			if strings.Contains(jsonOutput, "private-marker") || strings.Contains(jsonOutput, `"raw"`) {
				t.Fatalf("JSON output leaked private diagnostic data: %s", jsonOutput)
			}
		})
	}
}

func executeObservabilityPresentation(t *testing.T, root string, tty bool, args ...string) string {
	t.Helper()
	var output bytes.Buffer
	command := newRootCommand()
	command.SetContext(context.Background())
	if tty {
		writer := presentation.WrapWriter(&output, presentation.Capabilities{
			StdoutTTY: true, StderrTTY: true, Width: 100,
			Unicode: true, RawUnicode: true, Interactive: true,
		})
		command.SetOut(writer)
		command.SetErr(writer)
	} else {
		command.SetOut(&output)
		command.SetErr(&output)
	}
	command.SetArgs(append([]string{"--config-dir", root}, args...))
	if err := command.Execute(); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, output.String())
	}
	return output.String()
}
