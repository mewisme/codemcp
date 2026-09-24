package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func TestRootLogFormatJSON(t *testing.T) {
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs(testCommandArgs(t, "--log-format=json", "version"))
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("output %q is not JSON: %v", output.String(), err)
	}
	if event["component"] != "VERSION" || event["level"] != "info" || event["message"] == "" {
		t.Fatalf("event = %#v", event)
	}
}

func TestRootDebugUsesDiagnosticRenderer(t *testing.T) {
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs(testCommandArgs(t, "--debug", "version"))
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, "INF") || !strings.Contains(text, "VERSION") {
		t.Fatalf("debug output = %q", text)
	}
}

func TestRootRejectsInvalidLogFormat(t *testing.T) {
	cmd := newRootCommand()
	cmd.SetArgs(testCommandArgs(t, "--log-format=yaml", "version"))
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "unsupported log format") {
		t.Fatalf("error = %v", err)
	}
}

func TestCommandLoggerJSONFailure(t *testing.T) {
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs(testCommandArgs(t, "--log-format=json", "version"))
	if err := cmd.ParseFlags([]string{"--log-format=json"}); err != nil {
		t.Fatal(err)
	}
	commandLogger(cmd).Failure("CLI", "cli.command.failed", "Command failed", errors.New("boom"))
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("output %q is not JSON: %v", output.String(), err)
	}
	if event["event"] != "cli.command.failed" || event["error"] != "boom" {
		t.Fatalf("event = %#v", event)
	}
}

func TestCommandLoggerIsSharedForCommandLifecycle(t *testing.T) {
	cmd := newRootCommand()
	first := commandLogger(cmd)
	second := commandLogger(cmd)
	if first != second {
		t.Fatal("command logger was recreated within the same command lifecycle")
	}
	closeCommandLogger(cmd)
	next := commandLogger(cmd)
	if next == first {
		t.Fatal("closed command logger was retained")
	}
	closeCommandLogger(cmd)
}

func TestStartCommandSpinnerIsSilentWithoutTerminal(t *testing.T) {
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	log := commandLogger(cmd)
	startCommandSpinner(cmd, log, "TEST", "test.waiting", "Waiting")
	log.Close()
	if output.Len() != 0 {
		t.Fatalf("spinner wrote to non-terminal output: %q", output.String())
	}
}

func TestExecuteCommandVerboseEmitsLifecycle(t *testing.T) {
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs(testCommandArgs(t, "--verbose", "version"))
	if err := executeCommand(cmd); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, expected := range []string{"Executing command", "Command completed", "command:", "cwd:", "pid:", "duration_ms:"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("verbose lifecycle missing %q: %s", expected, text)
		}
	}
	if strings.Contains(text, "error_chain") || strings.Contains(text, "changed_flags") {
		t.Fatalf("verbose output leaked debug-only fields: %s", text)
	}
}

func TestExecuteCommandDebugFailureEmitsDiagnostics(t *testing.T) {
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.AddCommand(&cobra.Command{Use: "explode", RunE: func(*cobra.Command, []string) error { return errors.Join(errors.New("outer"), errors.New("inner")) }})
	cmd.SetArgs(testCommandArgs(t, "--debug", "explode"))
	err := executeCommand(cmd)
	if err == nil {
		t.Fatal("expected failure")
	}
	text := output.String()
	for _, expected := range []string{"cli.command.starting", "cli.command.context", "cli.command.failed", "error_type=", "error_chain=", "changed_flags=", "duration_ms="} {
		if !strings.Contains(text, expected) {
			t.Fatalf("debug failure missing %q: %s", expected, text)
		}
	}
}

func TestExecuteCommandDebugDoesNotLogFlagValues(t *testing.T) {
	var output bytes.Buffer
	var token string
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	child := &cobra.Command{Use: "secret", RunE: func(*cobra.Command, []string) error { return errors.New("expected failure") }}
	child.Flags().StringVar(&token, "token", "", "test secret")
	cmd.AddCommand(child)
	cmd.SetArgs(testCommandArgs(t, "--debug", "secret", "--token", "supersecret-value"))
	if err := executeCommand(cmd); err == nil {
		t.Fatal("expected failure")
	}
	text := output.String()
	if !strings.Contains(text, "--token") {
		t.Fatalf("debug output did not identify changed flag: %s", text)
	}
	if strings.Contains(text, "supersecret-value") {
		t.Fatalf("debug output leaked flag value: %s", text)
	}
}

func TestExecuteCommandVerboseDoesNotLogFlagValues(t *testing.T) {
	var output bytes.Buffer
	var apiKey string
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	child := &cobra.Command{Use: "verbose-secret", RunE: func(cmd *cobra.Command, _ []string) error {
		span := tracepkg.Start(cmd.Context(), "TEST", "test.verbose.secret", "Running verbose secret-safe operation", tracepkg.Bool("credential_configured", apiKey != ""))
		span.EndMessage("Verbose secret-safe operation completed", tracepkg.Bool("credential_configured", apiKey != ""))
		return nil
	}}
	child.Flags().StringVar(&apiKey, "api-key", "", "test secret")
	cmd.AddCommand(child)
	cmd.SetArgs(testCommandArgs(t, "--verbose", "verbose-secret", "--api-key", "verbose-supersecret-value"))
	if err := executeCommand(cmd); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, "credential_configured") {
		t.Fatalf("verbose output missing safe credential fact: %s", text)
	}
	if strings.Contains(text, "verbose-supersecret-value") {
		t.Fatalf("verbose output leaked flag value: %s", text)
	}
}

func TestMachineJSONOutputKeepsDiagnosticsOnStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	var asJSON bool
	cmd := newRootCommand()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	child := &cobra.Command{Use: "machine", RunE: func(cmd *cobra.Command, _ []string) error {
		logCommandStep(cmd, "TEST", "test.machine.loading", "Loading machine output")
		return writeResultJSON(cmd, map[string]any{"ok": true})
	}}
	addJSONResultFlag(child, &asJSON)
	cmd.AddCommand(child)
	cmd.SetArgs(testCommandArgs(t, "--verbose", "machine", "--json"))
	if err := executeCommand(cmd); err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &value); err != nil || value["ok"] != true {
		t.Fatalf("stdout is not clean JSON: %q err=%v value=%#v", stdout.String(), err, value)
	}
	if strings.Contains(stdout.String(), "Executing command") || !strings.Contains(stderr.String(), "Executing command") || !strings.Contains(stderr.String(), "Loading machine output") {
		t.Fatalf("diagnostic routing stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestJSONResultModeRoutesDiagnosticsAcrossLogModes(t *testing.T) {
	tests := []struct {
		name     string
		logArgs  []string
		jsonLogs bool
	}{
		{name: "default-text"},
		{name: "verbose-text", logArgs: []string{"--verbose"}},
		{name: "debug-text", logArgs: []string{"--debug"}},
		{name: "default-json-logs", logArgs: []string{"--log-format=json"}, jsonLogs: true},
		{name: "verbose-json-logs", logArgs: []string{"--verbose", "--log-format=json"}, jsonLogs: true},
		{name: "debug-json-logs", logArgs: []string{"--debug", "--log-format=json"}, jsonLogs: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			var asJSON bool
			cmd := newRootCommand()
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			child := &cobra.Command{Use: "result-contract", RunE: func(cmd *cobra.Command, _ []string) error {
				commandLogger(cmd).Notice("TEST", "test.result.diagnostic", "Result diagnostic")
				return writeResultJSON(cmd, map[string]any{"value": "<redacted>"})
			}}
			addJSONResultFlag(child, &asJSON)
			cmd.AddCommand(child)
			args := append([]string{}, test.logArgs...)
			args = append(args, "result-contract", "--json")
			cmd.SetArgs(testCommandArgs(t, args...))
			if err := executeCommand(cmd); err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if err := json.Unmarshal(stdout.Bytes(), &value); err != nil || value["value"] != "<redacted>" {
				t.Fatalf("stdout is not one clean result JSON value: %q err=%v value=%#v", stdout.String(), err, value)
			}
			if strings.Contains(stdout.String(), "\x1b") || strings.Contains(stdout.String(), "\r") || strings.Contains(stdout.String(), "\\u003c") {
				t.Fatalf("stdout contains terminal/escaped presentation residue: %q", stdout.String())
			}
			if !strings.Contains(stderr.String(), "Result diagnostic") {
				t.Fatalf("diagnostic did not route to stderr: %q", stderr.String())
			}
			if test.jsonLogs {
				for _, line := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
					var event map[string]any
					if err := json.Unmarshal([]byte(line), &event); err != nil {
						t.Fatalf("stderr diagnostic is not JSONL: %q err=%v", line, err)
					}
				}
			}
		})
	}
}

func TestEveryJSONFlagUsesCanonicalResultMode(t *testing.T) {
	root := newRootCommand()
	var visit func(*cobra.Command)
	visit = func(cmd *cobra.Command) {
		if flag := cmd.Flags().Lookup("json"); flag != nil {
			if cmd.Annotations[resultJSONAnnotation] != "true" {
				t.Errorf("%s exposes --json without canonical result-mode binding", cmd.CommandPath())
			}
		}
		for _, child := range cmd.Commands() {
			visit(child)
		}
	}
	visit(root)
}

func TestResultModeSeparatesResultJSONFromDiagnosticJSON(t *testing.T) {
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	if mode := commandResultModeFor(cmd); mode != resultModePlain {
		t.Fatalf("buffer-backed command mode=%v want plain", mode)
	}
	usage := cmd.PersistentFlags().Lookup("log-format").Usage
	if !strings.Contains(usage, "diagnostic") || !strings.Contains(usage, "does not change command result format") {
		t.Fatalf("log-format help does not distinguish diagnostics from results: %q", usage)
	}
}

func TestRootColorFlagsAndEnvironmentPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		noColorEnv string
		forceEnv   string
		wantANSI   bool
	}{
		{name: "plain-non-tty", args: []string{"version"}, wantANSI: false},
		{name: "explicit-color", args: []string{"--color", "version"}, wantANSI: true},
		{name: "explicit-no-color", args: []string{"--no-color", "version"}, forceEnv: "1", wantANSI: false},
		{name: "explicit-no-color-wins-both-flags", args: []string{"--color", "--no-color", "version"}, wantANSI: false},
		{name: "no-color-env", args: []string{"version"}, noColorEnv: "1", forceEnv: "1", wantANSI: false},
		{name: "force-color-env", args: []string{"version"}, forceEnv: "1", wantANSI: true},
		{name: "explicit-color-overrides-no-color-env", args: []string{"--color", "version"}, noColorEnv: "1", wantANSI: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", test.noColorEnv)
			t.Setenv("FORCE_COLOR", test.forceEnv)
			var output bytes.Buffer
			cmd := newRootCommand()
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SetArgs(testCommandArgs(t, test.args...))
			if err := executeCommand(cmd); err != nil {
				t.Fatal(err)
			}
			hasANSI := strings.Contains(output.String(), "\x1b[")
			if hasANSI != test.wantANSI {
				t.Fatalf("ANSI=%t want %t output=%q", hasANSI, test.wantANSI, output.String())
			}
		})
	}
}

func TestJSONMachineOutputIgnoresColorForStdout(t *testing.T) {
	t.Setenv("FORCE_COLOR", "1")
	var stdout, stderr bytes.Buffer
	var asJSON bool
	cmd := newRootCommand()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	child := &cobra.Command{Use: "colored-json", RunE: func(cmd *cobra.Command, _ []string) error {
		commandLogger(cmd).Notice("TEST", "test.colored-json", "Diagnostic")
		return writeResultJSON(cmd, map[string]any{"ok": true})
	}}
	addJSONResultFlag(child, &asJSON)
	cmd.AddCommand(child)
	cmd.SetArgs(testCommandArgs(t, "--color", "colored-json", "--json"))
	if err := executeCommand(cmd); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "\x1b[") {
		t.Fatalf("machine result stdout contains ANSI: %q", stdout.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result["ok"] != true {
		t.Fatalf("stdout=%q result=%#v err=%v", stdout.String(), result, err)
	}
}

func TestCommandTraceObserverUsesSharedVerboseLogger(t *testing.T) {
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.AddCommand(&cobra.Command{Use: "trace-test", RunE: func(cmd *cobra.Command, _ []string) error {
		span := tracepkg.Start(cmd.Context(), "TEST", "test.operation", "Running traced operation", tracepkg.String("path", "/tmp/test"))
		span.EndMessage("Traced operation completed", tracepkg.Int("count", 2))
		return nil
	}})
	cmd.SetArgs(testCommandArgs(t, "--verbose", "trace-test"))
	if err := executeCommand(cmd); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, expected := range []string{"Running traced operation", "Traced operation completed", "path:", "/tmp/test", "count:", "duration_ms:"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("trace output missing %q: %s", expected, text)
		}
	}
}

func TestCommandTraceProgressIsVisibleByDefault(t *testing.T) {
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.AddCommand(&cobra.Command{Use: "progress-test", RunE: func(cmd *cobra.Command, _ []string) error {
		span := tracepkg.Start(cmd.Context(), "CONFIG", "config.persist", "Persisting configuration")
		span.EndMessage("Configuration persisted")
		return nil
	}})
	cmd.SetArgs(testCommandArgs(t, "progress-test"))
	if err := executeCommand(cmd); err != nil {
		t.Fatal(err)
	}
	if text := output.String(); !strings.Contains(text, "Saving configuration... done") {
		t.Fatalf("default progress output missing completion: %q", text)
	}
}

func TestCommandTraceProgressRepresentativeSequencesAreOrdered(t *testing.T) {
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.AddCommand(&cobra.Command{Use: "progress-sequence", RunE: func(cmd *cobra.Command, _ []string) error {
		for _, phase := range []struct {
			component string
			name      string
			message   string
		}{
			{component: "CONFIG", name: "config.persist", message: "Persisting configuration"},
			{component: "INSTALL", name: "install.stage", message: "Staging installation"},
			{component: "TUNNEL", name: "tunnel.metadata.fetch", message: "Fetching tunnel metadata"},
		} {
			span := tracepkg.Start(cmd.Context(), phase.component, phase.name, phase.message)
			span.End()
		}
		return nil
	}})
	cmd.SetArgs(testCommandArgs(t, "progress-sequence"))
	if err := executeCommand(cmd); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	wants := []string{"Saving configuration... done", "Staging installation binary... done", "Fetching tunnel metadata... done"}
	previous := -1
	for _, want := range wants {
		index := strings.Index(text, want)
		if index < 0 || index <= previous {
			t.Fatalf("progress sequence missing or out of order %q: %q", want, text)
		}
		previous = index
	}
	if strings.ContainsAny(text, "\r\x1b") {
		t.Fatalf("plain progress sequence contains terminal control bytes: %q", text)
	}
}

func TestCommandTraceProgressDeduplicatesTerminalEvents(t *testing.T) {
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	observer := commandTraceObserver(cmd)
	observer(tracepkg.Event{Component: "CONFIG", Name: "config.persist.started", Message: "Persisting configuration", Phase: tracepkg.PhaseStart})
	completed := tracepkg.Event{Component: "CONFIG", Name: "config.persist.completed", Message: "Configuration persisted", Phase: tracepkg.PhaseEnd}
	observer(completed)
	observer(completed)
	closeCommandProgress(cmd, nil)
	closeCommandLogger(cmd)
	if count := strings.Count(output.String(), "Saving configuration... done"); count != 1 {
		t.Fatalf("terminal progress rendered %d times: %q", count, output.String())
	}
}

func TestCommandTraceProgressFailureIsStableAndErrorChainRemainsAvailable(t *testing.T) {
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.AddCommand(&cobra.Command{Use: "progress-failure", RunE: func(cmd *cobra.Command, _ []string) error {
		cause := errors.New("disk full")
		span := tracepkg.Start(cmd.Context(), "CONFIG", "config.persist", "Persisting configuration")
		span.FailMessage("Configuration persist failed", cause)
		return fmt.Errorf("save config: %w", cause)
	}})
	cmd.SetArgs(testCommandArgs(t, "--debug", "progress-failure"))
	err := executeCommand(cmd)
	if err == nil {
		t.Fatal("expected progress failure")
	}
	text := output.String()
	for _, want := range []string{"Saving configuration... failed: Configuration persist failed", "save config: disk full", "error_chain="} {
		if !strings.Contains(text, want) {
			t.Fatalf("failure output missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, "\r") {
		t.Fatalf("debug progress left transient carriage return: %q", text)
	}
}

func TestCommandTraceProgressVerboseKeepsDiagnosticsWithoutCursorControl(t *testing.T) {
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.AddCommand(&cobra.Command{Use: "progress-verbose", RunE: func(cmd *cobra.Command, _ []string) error {
		span := tracepkg.Start(cmd.Context(), "CONFIG", "config.persist", "Persisting configuration", tracepkg.String("path", "/tmp/config.json"))
		span.EndMessage("Configuration persisted", tracepkg.Int("bytes", 12))
		return nil
	}})
	cmd.SetArgs(testCommandArgs(t, "--verbose", "progress-verbose"))
	if err := executeCommand(cmd); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{"Saving configuration... done", "path:", "/tmp/config.json", "bytes:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("verbose progress missing %q: %q", want, text)
		}
	}
	if strings.ContainsAny(text, "\r\x1b") {
		t.Fatalf("verbose progress contains transient control bytes: %q", text)
	}
}

func TestCommandTraceProgressJSONDiagnosticsStayJSONL(t *testing.T) {
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.AddCommand(&cobra.Command{Use: "progress-json", RunE: func(cmd *cobra.Command, _ []string) error {
		span := tracepkg.Start(cmd.Context(), "CONFIG", "config.persist", "Persisting configuration", tracepkg.String("path", "/tmp/config.json"))
		span.EndMessage("Configuration persisted")
		return nil
	}})
	cmd.SetArgs(testCommandArgs(t, "--log-format=json", "progress-json"))
	if err := executeCommand(cmd); err != nil {
		t.Fatal(err)
	}
	text := strings.TrimSpace(output.String())
	if text == "" || strings.Contains(text, "... done") || strings.Contains(text, "\r") {
		t.Fatalf("JSON diagnostic progress was corrupted: %q", text)
	}
	for _, line := range strings.Split(text, "\n") {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("progress diagnostic is not JSONL: %q: %v", line, err)
		}
	}
}

func TestCommandTraceNonProgressRemainsHiddenByDefault(t *testing.T) {
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.AddCommand(&cobra.Command{Use: "quiet-trace-test", RunE: func(cmd *cobra.Command, _ []string) error {
		span := tracepkg.Start(cmd.Context(), "TEST", "test.internal", "Internal traced operation")
		span.EndMessage("Internal traced operation completed")
		return nil
	}})
	cmd.SetArgs(testCommandArgs(t, "quiet-trace-test"))
	if err := executeCommand(cmd); err != nil {
		t.Fatal(err)
	}
	if text := output.String(); strings.Contains(text, "Internal traced operation") {
		t.Fatalf("default output exposed non-progress trace: %q", text)
	}
}

func TestCommandTraceProgressPreservesSkippedResult(t *testing.T) {
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.AddCommand(&cobra.Command{Use: "skip-progress-test", RunE: func(cmd *cobra.Command, _ []string) error {
		span := tracepkg.Start(cmd.Context(), "CONFIG", "config.runtime.reload", "Reloading persisted configuration into runtime")
		span.EndMessage("Runtime reload skipped")
		return nil
	}})
	cmd.SetArgs(testCommandArgs(t, "skip-progress-test"))
	if err := executeCommand(cmd); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, "Runtime reload skipped") || strings.Contains(text, "Runtime configuration reloaded") {
		t.Fatalf("skipped progress result was not preserved: %q", text)
	}
}

func TestMachineOutputTraceStaysOnStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	child := &cobra.Command{Use: "trace-machine", RunE: func(cmd *cobra.Command, _ []string) error {
		span := tracepkg.Start(cmd.Context(), "TEST", "test.machine.trace", "Tracing machine command")
		span.End(tracepkg.Int("bytes", 4))
		_, err := cmd.OutOrStdout().Write([]byte("DATA"))
		return err
	}}
	markMachineOutput(child)
	cmd.AddCommand(child)
	cmd.SetArgs(testCommandArgs(t, "--verbose", "trace-machine"))
	if err := executeCommand(cmd); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "DATA" {
		t.Fatalf("stdout was polluted: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Tracing machine command") || !strings.Contains(stderr.String(), "duration_ms") {
		t.Fatalf("stderr missing trace: %q", stderr.String())
	}
}

func TestCommandTraceJSONPreservesStructuredFields(t *testing.T) {
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.AddCommand(&cobra.Command{Use: "trace-json", RunE: func(cmd *cobra.Command, _ []string) error {
		tracepkg.Emit(cmd.Context(), "TEST", "test.trace.json", "Structured trace", tracepkg.Int("count", 3), tracepkg.Bool("cache_hit", true))
		return nil
	}})
	cmd.SetArgs(testCommandArgs(t, "--verbose", "--log-format=json", "trace-json"))
	if err := executeCommand(cmd); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	var found map[string]any
	for _, line := range lines {
		var event map[string]any
		if json.Unmarshal([]byte(line), &event) != nil || event["event"] != "test.trace.json" {
			continue
		}
		found = event
		break
	}
	if found == nil {
		t.Fatalf("trace JSON event not found: %s", output.String())
	}
	fields, ok := found["fields"].(map[string]any)
	if !ok || fields["count"] != float64(3) || fields["cache_hit"] != true {
		t.Fatalf("structured fields lost: %#v", found)
	}
}
