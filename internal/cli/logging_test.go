package cli

import (
	"bytes"
	"encoding/json"
	"errors"
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
		return printJSON(cmd, map[string]any{"ok": true})
	}}
	addJSONOutputFlag(child, &asJSON)
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
	if text := output.String(); !strings.Contains(text, "Configuration saved") {
		t.Fatalf("default progress output missing completion: %q", text)
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
	markMachineOutput(child, "always")
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
