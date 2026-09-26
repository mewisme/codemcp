package mcp

import (
	"encoding/json"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	"go.mewis.me/codemcp/internal/tools"
)

type tasksDisabledProfile struct{ baseProfile }

func (tasksDisabledProfile) ID() ProfileID { return "tasks-disabled-test" }
func (tasksDisabledProfile) BackgroundCapabilities() BackgroundCapabilities {
	return BackgroundCapabilities{Execution: true}
}

func TestTasksExtensionIsAdvertisedByCanonicalCapabilities(t *testing.T) {
	capabilities := DefaultCapabilities()
	if _, ok := capabilities.Extensions[TasksExtensionID]; !ok {
		t.Fatalf("canonical capabilities do not advertise %q: %#v", TasksExtensionID, capabilities.Extensions)
	}
	_, options := ProjectSDKServer(BaseProfile(), DescribeProtocol(nil))
	if options == nil || options.Capabilities == nil {
		t.Fatal("SDK server capabilities are unavailable")
	}
	if _, ok := options.Capabilities.Extensions[TasksExtensionID]; !ok {
		t.Fatalf("SDK capabilities do not advertise %q: %#v", TasksExtensionID, options.Capabilities.Extensions)
	}
}

func TestTaskExtensionNegotiationIsExplicitPerRequest(t *testing.T) {
	if taskExtensionNegotiated(BaseProfile(), nil) {
		t.Fatal("missing request capabilities unexpectedly negotiated Tasks")
	}
	meta := map[string]any{
		sdkmcp.MetaKeyClientCapabilities: map[string]any{
			"extensions": map[string]any{"example/other": map[string]any{}},
		},
	}
	if taskExtensionNegotiated(BaseProfile(), meta) {
		t.Fatal("unrelated extension unexpectedly negotiated Tasks")
	}
	meta[sdkmcp.MetaKeyClientCapabilities] = map[string]any{
		"extensions": map[string]any{TasksExtensionID: map[string]any{}},
	}
	if !taskExtensionNegotiated(BaseProfile(), meta) {
		t.Fatal("Tasks extension was not negotiated")
	}
	if taskExtensionNegotiated(tasksDisabledProfile{}, meta) {
		t.Fatal("client Tasks support bypassed profile capability")
	}
}

func TestBackgroundCapabilitiesKeepTasksAndContinuationIndependent(t *testing.T) {
	profile := OpenAIProfile()
	static := ProfileBackgroundCapabilities(profile)
	if !static.Execution || !static.TaskObservation {
		t.Fatalf("OpenAI static background capabilities=%#v", static)
	}
	if static.ServerNotification || static.ModelContinuation || static.InFlightSteering {
		t.Fatalf("profile claimed unproven background delivery capabilities=%#v", static)
	}

	request := RequestContext{NegotiatedExtensions: map[string]any{TasksExtensionID: map[string]any{}}}
	negotiated := RequestBackgroundCapabilities(profile, request)
	if !negotiated.TaskObservation {
		t.Fatalf("Tasks-capable request did not negotiate task observation: %#v", negotiated)
	}
	if negotiated.ModelContinuation || negotiated.InFlightSteering {
		t.Fatalf("Tasks support incorrectly implied continuation: %#v", negotiated)
	}

	withoutTasks := RequestBackgroundCapabilities(profile, RequestContext{})
	if withoutTasks.TaskObservation {
		t.Fatalf("request without Tasks extension negotiated task observation: %#v", withoutTasks)
	}
}

func TestProfileProjectionAdvertisesTasksOnlyWhenEligible(t *testing.T) {
	descriptor := DescribeProtocol(nil)
	discovery := ProjectCapabilities(BaseProfile(), descriptor.Capabilities, false)
	if _, ok := discovery.Extensions[TasksExtensionID]; ok {
		t.Fatalf("non-Task transport advertised Tasks: %#v", discovery.Extensions)
	}
	_, baseOptions := ProjectSDKServer(BaseProfile(), descriptor)
	if _, ok := baseOptions.Capabilities.Extensions[TasksExtensionID]; !ok {
		t.Fatalf("base profile lost Tasks capability: %#v", baseOptions.Capabilities.Extensions)
	}
	_, disabledOptions := ProjectSDKServer(tasksDisabledProfile{}, descriptor)
	if _, ok := disabledOptions.Capabilities.Extensions[TasksExtensionID]; ok {
		t.Fatalf("Tasks-disabled profile advertised Tasks: %#v", disabledOptions.Capabilities.Extensions)
	}
}

func TestSDKServerDoesNotAllocateTasksForIneligibleProfile(t *testing.T) {
	runtime := &tools.Runtime{Registry: tools.NewRegistry()}
	server, err := NewSDKServerWithProfile(runtime, "test", "", "", tasksDisabledProfile{})
	if err != nil {
		t.Fatal(err)
	}
	if server.Tasks != nil {
		t.Fatal("Tasks-disabled profile allocated a task registry")
	}
}

func TestTaskRegistryDerivesOneTerminalTransitionFromProcessEvent(t *testing.T) {
	registry := NewTaskRegistry(nil)
	t.Cleanup(registry.Close)
	start := shellruntime.StartResult{ID: "proc_1", ExecutionID: "exec_1", PID: 123, Command: "example", CWD: "/tmp", StartedAt: "2026-09-26T00:00:00Z"}
	initial := &sdkmcp.CallToolResult{StructuredContent: start, Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "started"}}}
	created, err := registry.Create(start, initial)
	if err != nil {
		t.Fatal(err)
	}
	if created.ResultType != "task" || created.Status != TaskStatusWorking || created.TaskID == "" {
		t.Fatalf("created task = %#v", created)
	}
	event := shellruntime.BackgroundWorkTerminalEvent{
		WorkspaceID: "ws_1", ProcessID: "proc_1", ExecutionID: "exec_1", Tool: "start_process",
		Status: "success", Reason: shellruntime.BackgroundTerminalExit,
		StartedAt: start.StartedAt, FinishedAt: "2026-09-26T00:00:01Z",
	}
	registry.ApplyTerminal(event)
	first, ok := registry.Get(created.TaskID)
	if !ok || first.Status != TaskStatusCompleted || first.Result == nil {
		t.Fatalf("terminal task = %#v ok=%t", first, ok)
	}
	firstUpdated := first.LastUpdatedAt
	registry.ApplyTerminal(event)
	second, ok := registry.Get(created.TaskID)
	if !ok || second.Status != TaskStatusCompleted || second.LastUpdatedAt != firstUpdated {
		t.Fatalf("duplicate terminal event changed task: first=%#v second=%#v", first, second)
	}
}

func TestTaskRegistryReplaysTerminalEventThatWonCreationRace(t *testing.T) {
	registry := NewTaskRegistry(nil)
	t.Cleanup(registry.Close)
	registry.ApplyTerminal(shellruntime.BackgroundWorkTerminalEvent{
		WorkspaceID: "ws_1", ProcessID: "proc_fast", ExecutionID: "exec_fast", Tool: "start_process",
		Status: "success", Reason: shellruntime.BackgroundTerminalExit,
		StartedAt: "2026-09-26T00:00:00Z", FinishedAt: "2026-09-26T00:00:00.001Z",
	})
	start := shellruntime.StartResult{ID: "proc_fast", ExecutionID: "exec_fast", PID: 456, Command: "true", CWD: "/tmp", StartedAt: "2026-09-26T00:00:00Z"}
	created, err := registry.Create(start, &sdkmcp.CallToolResult{StructuredContent: start})
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != TaskStatusCompleted {
		t.Fatalf("fast terminal event was not replayed into task: %#v", created)
	}
}

func TestTaskGetIsIdempotentAndDoesNotConsumeResult(t *testing.T) {
	registry := NewTaskRegistry(nil)
	t.Cleanup(registry.Close)
	start := shellruntime.StartResult{ID: "proc_read", ExecutionID: "exec_read", PID: 789, Command: "example", CWD: "/tmp", StartedAt: "2026-09-26T00:00:00Z"}
	created, err := registry.Create(start, &sdkmcp.CallToolResult{StructuredContent: start})
	if err != nil {
		t.Fatal(err)
	}
	registry.ApplyTerminal(shellruntime.BackgroundWorkTerminalEvent{ProcessID: start.ID, ExecutionID: start.ExecutionID, Reason: shellruntime.BackgroundTerminalExit})
	one, ok := registry.Get(created.TaskID)
	if !ok {
		t.Fatal("task missing on first get")
	}
	two, ok := registry.Get(created.TaskID)
	if !ok {
		t.Fatal("task missing on second get")
	}
	oneJSON, _ := json.Marshal(one)
	twoJSON, _ := json.Marshal(two)
	if string(oneJSON) != string(twoJSON) {
		t.Fatalf("tasks/get observation is not idempotent: %s != %s", oneJSON, twoJSON)
	}
}
