package logger

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fatih/color"
)

func TestDefaultTextRendererIsReadableRuntimeOutput(t *testing.T) {
	restoreColor := disableColor()
	defer restoreColor()
	var output bytes.Buffer
	log := NewWithOptions(Options{Level: Info, Writer: &output})
	log.Ready("SERVER", "server.ready", "Server ready", With("mcp", "http://127.0.0.1:37421/mcp"), WithVerbose("bind", "127.0.0.1:37421"), WithDebug("listener_id", "listener-1"))
	text := output.String()
	for _, expected := range []string{"✓ Server ready", "mcp: http://127.0.0.1:37421/mcp"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("output %q missing %q", text, expected)
		}
	}
	for _, hidden := range []string{"INF", "SERVER", "bind", "listener_id", "server.ready"} {
		if strings.Contains(text, hidden) {
			t.Fatalf("default output %q unexpectedly contains %q", text, hidden)
		}
	}
}

func TestTextRendererCapitalizesMessagesAfterIcons(t *testing.T) {
	restoreColor := disableColor()
	defer restoreColor()
	var output bytes.Buffer
	log := NewWithOptions(Options{Level: Info, Writer: &output})
	log.Ready("SERVICE", "service.updated", "managed service updated")
	log.Action("SERVICE", "service.updating", "updating managed service")
	log.Info("WORKSPACE", "registered workspaces loaded")
	text := output.String()
	for _, expected := range []string{"✓ Managed service updated", "· Updating managed service", "· Registered workspaces loaded"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("output %q missing %q", text, expected)
		}
	}
}

func TestActionRendererIsStaticAndDoesNotOwnTerminalAnimation(t *testing.T) {
	restoreColor := disableColor()
	defer restoreColor()
	var output bytes.Buffer
	log := NewWithOptions(Options{Level: Info, Writer: &output})
	log.Action("TUNNEL", "tunnel.connecting", "connecting tunnel")
	log.Ready("TUNNEL", "tunnel.connected", "tunnel connected")
	text := output.String()
	for _, expected := range []string{"· Connecting tunnel\n", "✓ Tunnel connected\n"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("static action output %q missing %q", text, expected)
		}
	}
	for _, forbidden := range []string{"⠋", "\r", "\x1b[?25"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("logger retained terminal animation residue %q: %q", forbidden, text)
		}
	}
}

func TestInjectedTerminalOptionsControlColorAndUnicode(t *testing.T) {
	var output bytes.Buffer
	terminal := &TerminalOptions{Color: false, Unicode: false}
	log := NewWithOptions(Options{Level: Info, Writer: &output, Terminal: terminal})
	log.Ready("SERVER", "server.ready", "Server ready")
	if text := output.String(); text != "[OK] Server ready\n" || strings.Contains(text, "\x1b[") {
		t.Fatalf("ASCII logger output = %q", text)
	}

	output.Reset()
	terminal = &TerminalOptions{Color: true, Unicode: true}
	log = NewWithOptions(Options{Level: Info, Writer: &output, Terminal: terminal})
	log.Ready("SERVER", "server.ready", "Server ready")
	if text := output.String(); !strings.Contains(text, "✓") || !strings.Contains(text, "Server ready") || !strings.Contains(text, "\x1b[") {
		t.Fatalf("Unicode colored logger output = %q", text)
	}
}

func TestVerboseRendererAddsUsefulContext(t *testing.T) {
	restoreColor := disableColor()
	defer restoreColor()
	var output bytes.Buffer
	log := NewWithOptions(Options{Level: Info, Mode: ModeVerbose, Writer: &output})
	log.Ready("TUNNEL", "tunnel.connected", "Tunnel connected", WithVerbose("tunnel_id", "tunnel_123"), WithDebug("client_instance_id", "client_123"))
	text := output.String()
	if !strings.Contains(text, "tunnel_id: tunnel_123") || strings.Contains(text, "client_instance_id") {
		t.Fatalf("verbose output = %q", text)
	}
}

func TestTimeModeShowsTimestampOnlyOnPrimaryTextLine(t *testing.T) {
	restoreColor := disableColor()
	defer restoreColor()
	var output bytes.Buffer
	log := NewWithOptions(Options{Level: Info, TimeMode: TimeShow, Writer: &output})
	log.now = func() time.Time { return time.Date(2026, 8, 31, 12, 34, 56, 0, time.UTC) }
	log.Ready("SERVER", "server.ready", "Server ready", With("mcp", "http://127.0.0.1:37421/mcp"))
	text := output.String()
	if !strings.Contains(text, "12:34:56 ✓ Server ready") {
		t.Fatalf("timestamped output = %q", text)
	}
	if strings.Contains(text, "12:34:56     mcp:") {
		t.Fatalf("detail line unexpectedly timestamped: %q", text)
	}
}

func TestTimeHideDisablesDebugTimestamp(t *testing.T) {
	restoreColor := disableColor()
	defer restoreColor()
	var output bytes.Buffer
	log := NewWithOptions(Options{Level: Debug, Mode: ModeDebug, TimeMode: TimeHide, Writer: &output})
	log.now = func() time.Time { return time.Date(2026, 8, 31, 12, 34, 56, 0, time.UTC) }
	log.Diagnostic(Debug, "TEST", "test.debug", "Debug line")
	if strings.Contains(output.String(), "12:34:56") {
		t.Fatalf("debug timestamp was not disabled: %q", output.String())
	}
}

func TestDebugRendererIncludesStructuredMetadata(t *testing.T) {
	restoreColor := disableColor()
	defer restoreColor()
	var output bytes.Buffer
	log := NewWithOptions(Options{Level: Debug, Mode: ModeDebug, Writer: &output})
	log.now = func() time.Time { return time.Date(2026, 8, 31, 0, 27, 32, 0, time.UTC) }
	log.Diagnostic(Debug, "TUNNEL", "tunnel.route.resolved", "route resolved", WithDebug("client_instance_id", "client_123"), WithDebug("route_kind", "mcp_channel"))
	text := output.String()
	for _, expected := range []string{"DBG", "TUNNEL", "tunnel.route.resolved", "client_instance_id=client_123", "route_kind=mcp_channel"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("debug output %q missing %q", text, expected)
		}
	}
	if strings.Contains(text, "00:27:32") {
		t.Fatalf("normal CLI debug output unexpectedly contains timestamp: %q", text)
	}
}

func TestJSONRendererRespectsModeAndKeepsStructure(t *testing.T) {
	var output bytes.Buffer
	log := NewWithOptions(Options{Level: Info, Mode: ModeVerbose, Format: FormatJSON, Writer: &output})
	log.now = func() time.Time { return time.Date(2026, 8, 31, 0, 27, 32, 0, time.UTC) }
	log.Failure("TUNNEL", "tunnel.failed", "Tunnel failed", errors.New("dial failed"), With("tunnel_id", "tunnel_123"), WithVerbose("transport", "in-memory"), WithDebug("client_instance_id", "client_123"))
	var value map[string]any
	if err := json.Unmarshal(output.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if value["event"] != "tunnel.failed" || value["level"] != "error" || value["kind"] != "error" || value["error"] != "dial failed" {
		t.Fatalf("json event = %#v", value)
	}
	fields, ok := value["fields"].(map[string]any)
	if !ok || fields["tunnel_id"] != "tunnel_123" || fields["transport"] != "in-memory" {
		t.Fatalf("json fields = %#v", value["fields"])
	}
	if _, ok := fields["client_instance_id"]; ok {
		t.Fatalf("debug field leaked into verbose json: %#v", fields)
	}
}

func TestJSONRendererIncludesReplaySessionMetadata(t *testing.T) {
	var output bytes.Buffer
	log := NewWithOptions(Options{Level: Info, Format: FormatJSON, Writer: &output})
	log.Emit(Event{Level: Info, Kind: KindSuccess, Name: "server.ready", Message: "Server ready", Component: "SERVER", RunID: "run_test", PID: 42, Managed: true, ServiceID: "service_test", ServiceScope: "system"})
	var value map[string]any
	if err := json.Unmarshal(output.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if value["run_id"] != "run_test" || value["pid"] != float64(42) || value["managed"] != true || value["service_id"] != "service_test" || value["service_scope"] != "system" {
		t.Fatalf("json metadata = %#v", value)
	}
}

func TestTextRendererUsesVisualHierarchyAndRespectsNoColor(t *testing.T) {
	previous := color.NoColor
	color.NoColor = false
	defer func() { color.NoColor = previous }()
	t.Setenv("NO_COLOR", "")
	var output bytes.Buffer
	log := NewWithOptions(Options{Level: Info, TimeMode: TimeShow, Writer: &output})
	log.now = func() time.Time { return time.Date(2026, 8, 31, 12, 34, 56, 0, time.UTC) }
	log.Ready("SERVER", "server.ready", "Server ready", With("mcp", "http://127.0.0.1:37421/mcp"))
	text := output.String()
	if !strings.Contains(text, "\x1b[") || !strings.Contains(text, "\x1b[2m") {
		t.Fatalf("styled output = %q", text)
	}
	if !strings.Contains(text, "Server ready") || !strings.Contains(text, "http://127.0.0.1:37421/mcp") {
		t.Fatalf("styled output lost content: %q", text)
	}

	t.Setenv("NO_COLOR", "1")
	output.Reset()
	log = NewWithOptions(Options{Level: Info, TimeMode: TimeShow, Writer: &output})
	log.now = func() time.Time { return time.Date(2026, 8, 31, 12, 34, 56, 0, time.UTC) }
	log.Ready("SERVER", "server.ready", "Server ready", With("mcp", "http://127.0.0.1:37421/mcp"))
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("NO_COLOR output contains ANSI: %q", output.String())
	}
}

type captureSink struct{ events []Event }

func (s *captureSink) WriteEvent(event Event) error {
	s.events = append(s.events, event)
	return nil
}

func TestSinkReceivesNormalizedEventBeforeVisibilityFiltering(t *testing.T) {
	var output bytes.Buffer
	log := NewWithOptions(Options{Level: Info, Writer: &output})
	sink := &captureSink{}
	log.AddSink(sink)
	log.now = func() time.Time { return time.Date(2026, 8, 31, 1, 2, 3, 0, time.UTC) }
	log.Diagnostic(Debug, "TOOL", "tool.call.started", "Tool call started", WithDebug("tool", "run_command"))
	if output.Len() != 0 {
		t.Fatalf("debug event leaked into default output: %q", output.String())
	}
	if len(sink.events) != 1 {
		t.Fatalf("sink events = %#v", sink.events)
	}
	event := sink.events[0]
	if event.Time.IsZero() || event.Name != "tool.call.started" || event.Component != "TOOL" || event.Visibility != VisibilityDebug {
		t.Fatalf("sink event = %#v", event)
	}
}

func TestLoggerRedactsCredentialMaterialBeforeRenderAndSinks(t *testing.T) {
	restoreColor := disableColor()
	defer restoreColor()
	const secret = "logger-secret-marker"
	var output bytes.Buffer
	log := NewWithOptions(Options{Level: Info, Mode: ModeDebug, Writer: &output})
	sink := &captureSink{}
	log.AddSink(sink)
	log.Failure(
		"CONFIG",
		"config.failed",
		"Bearer "+secret,
		errors.New("token="+secret),
		With("command", "cm config set tunnel.api_key "+secret),
		With("authorization", "Bearer "+secret),
		With("url", "https://example.test/mcp?token="+secret+"&safe=1"),
	)
	serialized, err := json.Marshal(sink.events)
	if err != nil {
		t.Fatal(err)
	}
	text := output.String() + string(serialized)
	if strings.Contains(text, secret) {
		t.Fatalf("logger leaked credential material: %s", text)
	}
	if !strings.Contains(text, "<redacted>") {
		t.Fatalf("logger output missing redaction marker: %s", text)
	}
}

func TestLogFormatModeAndEnumMappings(t *testing.T) {
	for input, want := range map[string]Format{"": FormatText, "TEXT": FormatText, "json": FormatJSON} {
		got, err := ParseFormat(input)
		if err != nil || got != want {
			t.Fatalf("ParseFormat(%q)=%q, %v", input, got, err)
		}
	}
	if _, err := ParseFormat("yaml"); err == nil {
		t.Fatal("invalid format accepted")
	}
	if ModeFor(false, false) != ModeDefault || ModeFor(true, false) != ModeVerbose || ModeFor(false, true) != ModeDebug || ModeFor(true, true) != ModeDebug {
		t.Fatal("ModeFor mapping mismatch")
	}
	for level, want := range map[Level]string{Debug: "debug", Info: "info", Warn: "warn", Error: "error", Level(99): "info"} {
		if got := level.String(); got != want {
			t.Fatalf("level %d=%q want %q", level, got, want)
		}
	}
	for kind, want := range map[Kind]string{KindInfo: "info", KindAction: "action", KindSuccess: "success", KindWarning: "warning", KindError: "error", Kind(99): "info"} {
		if got := kind.String(); got != want {
			t.Fatalf("kind %d=%q want %q", kind, got, want)
		}
	}
}

func TestLoggerConvenienceMethodsNormalizeAndReachSinks(t *testing.T) {
	var output bytes.Buffer
	log := NewCLIWithWriter(&output)
	sink := &captureSink{}
	log.AddSink(nil)
	log.AddSink(sink)
	log.now = func() time.Time { return time.Date(2026, 9, 6, 1, 2, 3, 0, time.UTC) }
	log.Notice("", "", "notice")
	log.Warning("WARN", "warn.event", "warning", errors.New("warn"))
	log.Verbose("VERBOSE", "verbose.event", "verbose")
	log.Debug("DEBUG", "debug", "value", 1)
	log.Warn("WARN", "warn")
	log.Error("ERROR", "error")
	log.Success("SUCCESS", "success")
	log.Detail("key", "value")
	log.Close()
	if len(sink.events) != 8 {
		t.Fatalf("sink events=%d", len(sink.events))
	}
	if sink.events[0].Name == "" || sink.events[0].Component != "CLI" || sink.events[0].Time.IsZero() {
		t.Fatalf("normalized notice=%#v", sink.events[0])
	}
	if sink.events[1].Kind != KindWarning || sink.events[1].Err == nil || sink.events[2].Visibility != VisibilityVerbose || sink.events[3].Visibility != VisibilityDebug || sink.events[4].Kind != KindWarning || sink.events[5].Kind != KindError || sink.events[6].Kind != KindSuccess || sink.events[7].Name != "cli.detail" {
		t.Fatalf("events=%#v", sink.events)
	}
}

func TestLegacyFieldsAndEventNamesCoverErrorShapes(t *testing.T) {
	fields, err := legacyFields("one", 1, "error", errors.New("boom"), "two", 2)
	if err == nil || err.Error() != "boom" || len(fields) != 2 {
		t.Fatalf("fields=%#v err=%v", fields, err)
	}
	fields, err = legacyFields("error", "string-error", "dangling")
	if err == nil || err.Error() != "string-error" || len(fields) != 0 {
		t.Fatalf("fields=%#v err=%v", fields, err)
	}
	if name := legacyEventName("", ""); name != "log" {
		t.Fatalf("empty event name=%q", name)
	}
	if name := legacyEventName("Component", ""); name != "component" {
		t.Fatalf("component event name=%q", name)
	}
	if kindForLevel(Debug) != KindInfo || kindForLevel(Warn) != KindWarning || kindForLevel(Error) != KindError {
		t.Fatal("kindForLevel mismatch")
	}
}

func TestRenderHelpersCoverSlicesErrorsSymbolsAndLevels(t *testing.T) {
	var output bytes.Buffer
	log := NewWithOptions(Options{Level: Info, Writer: &output, Terminal: &TerminalOptions{Color: false, Unicode: true}})
	log.renderField("items", []string{"one", "two"})
	log.renderField("single", [1]string{"value"})
	log.renderField("", 42)
	text := output.String()
	for _, want := range []string{"items:", "- one", "- two", "single: value", "value: 42"} {
		if !strings.Contains(text, want) {
			t.Fatalf("rendered %q missing %q", text, want)
		}
	}
	if values, ok := stringSlice(nil); ok || values != nil {
		t.Fatalf("nil stringSlice=%#v,%t", values, ok)
	}
	if values, ok := stringSlice("value"); ok || values != nil {
		t.Fatalf("scalar stringSlice=%#v,%t", values, ok)
	}
	if got := jsonValue(errors.New("boom")); got != "boom" {
		t.Fatalf("json error=%v", got)
	}
	for _, kind := range []Kind{KindInfo, KindAction, KindSuccess, KindWarning, KindError} {
		if log.symbol(kind) == "" || log.symbolStyle(kind) == nil {
			t.Fatalf("kind=%v symbol/style missing", kind)
		}
	}
	for _, level := range []Level{Debug, Info, Warn, Error} {
		code := levelCode(level)
		if code == "" || log.levelStyle(code) == nil {
			t.Fatalf("level=%v code/style missing", level)
		}
	}
}
