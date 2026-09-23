package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/controlguard"
	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/runtimeevent"
	shellruntime "go.mewis.me/codemcp/internal/shell"
)

func TestServeRuntimeControlLogsUnexpectedFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	log := logger.NewWithOptions(logger.Options{Level: logger.Debug, Mode: logger.ModeDebug, Writer: &output})
	serveRuntimeControl(newHTTPServer(http.NewServeMux()), listener, log)
	text := output.String()
	if !strings.Contains(text, "runtime.control.failed") || !strings.Contains(text, "Runtime control server stopped unexpectedly") {
		t.Fatalf("unexpected control failure was not logged: %s", text)
	}
}

func TestRuntimeControlReloadStatusAndShutdownRoundTrip(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	journal, err := runtimeevent.NewJournal(root, runtimeevent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	stream := runtimeevent.NewStream(runtimeevent.Metadata{RunID: "run_test", PID: os.Getpid()})
	shutdown := make(chan struct{}, 1)
	control, err := startRuntimeControl(runtimeControlOptions{RunID: "run_test", StartedAt: time.Now(), Events: stream, Reload: func(context.Context) (runtimeReloadResult, error) {
		return runtimeReloadResult{PID: os.Getpid(), NetworkRestarted: true, ServerPort: 41001, AdminEnabled: true, AdminPort: 41002, Exposure: config.ExposureNone}, nil
	}, Status: func() runtimeStatusResult {
		return runtimeStatusResult{PID: os.Getpid(), RunID: "run_test", ConfigRoot: root, ServerPort: 41001, AdminEnabled: true, AdminPort: 41002, Exposure: config.ExposureNone, TunnelEnabled: true, TunnelConfigured: true, TunnelRunning: true, TunnelReady: true, TunnelID: "tunnel_test"}
	}, Shutdown: func() {
		select {
		case shutdown <- struct{}{}:
		default:
		}
	}, ClearLogs: journal.Clear})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := requestRuntimeReload(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.PID != os.Getpid() || !result.NetworkRestarted || result.ServerPort != 41001 || result.AdminPort != 41002 {
		t.Fatalf("reload result = %#v", result)
	}
	status, err := requestRuntimeStatus(ctx)
	if err != nil || status.RunID != "run_test" || status.ServerPort != 41001 || !status.TunnelEnabled || !status.TunnelConfigured || !status.TunnelReady || status.TunnelID != "tunnel_test" {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	if err := requestRuntimeShutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-shutdown:
	case <-time.After(time.Second):
		t.Fatal("shutdown callback was not invoked")
	}
}

func TestRuntimeControlRejectsUnauthenticatedEvents(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	control, err := startRuntimeControl(runtimeControlOptions{RunID: "run_test", Events: runtimeevent.NewStream(runtimeevent.Metadata{}), Reload: func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil }, Status: func() runtimeStatusResult { return runtimeStatusResult{PID: os.Getpid()} }, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + control.state.Address + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

func TestRuntimeControlConsumesOneShotCLIApproval(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	manager := approval.NewManager("instance-test")
	challenge, _, err := manager.CreateChallenge(approval.ChallengeInput{
		SessionID: "session-a", SessionHash: "hash-a", WorkspaceID: "ws_x", Source: "tunnel", TargetTool: "run_command",
		Arguments: map[string]any{"workspace_id": "ws_x", "command": "cgm update"}, GuardCode: controlguard.CodeControlPlaneMutation, GuardReason: "denied", Title: "Allow cgm update",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequest(challenge.ID, "session-a", "ws_x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Approve(request.ID, "test", ""); err != nil {
		t.Fatal(err)
	}
	_, capability, matched, err := manager.ClaimApprovedCLI(approval.RetryInput{SessionID: "session-a", WorkspaceID: "ws_x", Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"workspace_id": "ws_x", "command": "cgm update"}}, approval.CLIInvocation{Program: "cgm", Args: []string{"update"}})
	if err != nil || !matched || capability == "" {
		t.Fatalf("claim capability=%q matched=%t err=%v", capability, matched, err)
	}
	control, err := startRuntimeControl(runtimeControlOptions{Approvals: manager, Events: runtimeevent.NewStream(runtimeevent.Metadata{}), Reload: func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil }, Status: func() runtimeStatusResult { return runtimeStatusResult{PID: os.Getpid()} }, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := requestRuntimeCLIApproval(ctx, capability, []string{"update", "--version", "v2"}); err == nil {
		t.Fatal("mismatched CLI approval unexpectedly succeeded")
	}
	if err := requestRuntimeCLIApproval(ctx, capability, []string{"update"}); err != nil {
		t.Fatalf("exact CLI approval failed: %v", err)
	}
	if err := requestRuntimeCLIApproval(ctx, capability, []string{"update"}); err == nil {
		t.Fatal("CLI approval replay unexpectedly succeeded")
	}
}

func TestRuntimeControlRequestListViewApproveAndDeny(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	manager := approval.NewManager("instance-test")
	first := seedApprovalRequest(t, manager, "session-a", "ws_a", "cgm update")
	second := seedApprovalRequest(t, manager, "session-b", "ws_b", "cgm install")
	control, err := startRuntimeControl(runtimeControlOptions{Approvals: manager, Events: runtimeevent.NewStream(runtimeevent.Metadata{}), Reload: func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil }, Status: func() runtimeStatusResult { return runtimeStatusResult{PID: os.Getpid()} }, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	requests, err := requestRuntimeApprovalList(ctx)
	if err != nil || len(requests) != 2 {
		t.Fatalf("request list = %#v err=%v", requests, err)
	}
	firstPrefix := uniqueRequestPrefix(first.ID, second.ID)
	viewed, err := requestRuntimeApprovalView(ctx, firstPrefix)
	if err != nil || viewed.ID != first.ID {
		t.Fatalf("request view = %#v err=%v", viewed, err)
	}
	approved, err := requestRuntimeApprovalApprove(ctx, firstPrefix, "reviewed")
	if err != nil || approved.Status != approval.StatusApproved || approved.ResolvedBy != "cli" || approved.Reason != "reviewed" {
		t.Fatalf("request approve = %#v err=%v", approved, err)
	}
	secondPrefix := uniqueRequestPrefix(second.ID, first.ID)
	denied, err := requestRuntimeApprovalDeny(ctx, secondPrefix, "not now")
	if err != nil || denied.Status != approval.StatusDenied || denied.ResolvedBy != "cli" || denied.Reason != "not now" {
		t.Fatalf("request deny = %#v err=%v", denied, err)
	}
	if _, err := requestRuntimeApprovalView(ctx, "req_"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous request prefix err=%v", err)
	}
}

func TestRuntimeControlListsApprovalAfterWaiterCancellation(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	manager := approval.NewManager("instance-test")
	request := seedApprovalRequest(t, manager, "session-a", "ws_a", "cgm update")
	waitCtx, cancelWait := context.WithCancel(context.Background())
	cancelWait()
	if value, err := manager.Wait(waitCtx, request.ID); !errors.Is(err, context.Canceled) || value.Status != approval.StatusPending {
		t.Fatalf("detached wait = %#v err=%v", value, err)
	}
	control, err := startRuntimeControl(runtimeControlOptions{Approvals: manager, Events: runtimeevent.NewStream(runtimeevent.Metadata{}), Reload: func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil }, Status: func() runtimeStatusResult { return runtimeStatusResult{PID: os.Getpid()} }, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	requests, err := requestRuntimeApprovalList(ctx)
	if err != nil || len(requests) != 1 || requests[0].ID != request.ID || requests[0].Status != approval.StatusPending {
		t.Fatalf("runtime request list = %#v err=%v", requests, err)
	}
}

func TestRuntimeControlExecutionFeedReplaysAndStreamsCombinedOutput(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	hub := shellruntime.NewExecutionHub()
	run := hub.Begin(shellruntime.ExecutionInput{WorkspaceID: "ws_a", Tool: "run_command", Command: "printf test", CWD: "/tmp", Source: "mcp"})
	_, _ = run.Writer("stdout").Write([]byte("before\n"))
	control, err := startRuntimeControl(runtimeControlOptions{Executions: hub, Events: runtimeevent.NewStream(runtimeevent.Metadata{}), Reload: func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil }, Status: func() runtimeStatusResult { return runtimeStatusResult{PID: os.Getpid()} }, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	request, _ := http.NewRequest(http.MethodGet, "http://"+control.state.Address+"/executions/stream", nil)
	request.Header.Set("Authorization", "Bearer "+control.state.Token)
	response, err := (&http.Client{Timeout: time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	ready := scanRuntimeControlEventData(t, scanner, "ready")
	if !strings.Contains(ready, `"latest_sequence":2`) || !strings.Contains(ready, `"replay_count":2`) {
		t.Fatalf("ready=%q", ready)
	}
	replayStarted := scanRuntimeControlEventData(t, scanner, shellruntime.ExecutionEventStarted)
	replayOutput := scanRuntimeControlEventData(t, scanner, shellruntime.ExecutionEventOutput)
	if !strings.Contains(replayStarted, run.ID()) || !strings.Contains(replayOutput, "before") {
		t.Fatalf("replay started=%q output=%q", replayStarted, replayOutput)
	}
	_, _ = run.Writer("stderr").Write([]byte("after\n"))
	output := scanRuntimeControlEventData(t, scanner, shellruntime.ExecutionEventOutput)
	if !strings.Contains(output, `"stream":"stderr"`) || !strings.Contains(output, "after") {
		t.Fatalf("output=%q", output)
	}
}

func scanRuntimeControlEventData(t *testing.T, scanner *bufio.Scanner, eventName string) string {
	t.Helper()
	current, data := "", ""
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if current == eventName {
				return data
			}
			current, data = "", ""
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			current = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		} else if strings.HasPrefix(line, "data: ") {
			data += strings.TrimPrefix(line, "data: ")
		}
	}
	t.Fatalf("event %q not found", eventName)
	return ""
}

func TestRuntimeControlRejectsUnknownApprovalActionBeforeMutation(t *testing.T) {
	if _, err := requestRuntimeApprovalResolve(t.Context(), "unknown", "req_test", ""); err == nil || !strings.Contains(err.Error(), "unsupported approval action") {
		t.Fatalf("err=%v", err)
	}
}

func TestRuntimeControlRejectsUnauthenticatedCLIApprovalConsume(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	control, err := startRuntimeControl(runtimeControlOptions{Approvals: approval.NewManager("instance-test"), Events: runtimeevent.NewStream(runtimeevent.Metadata{}), Reload: func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil }, Status: func() runtimeStatusResult { return runtimeStatusResult{PID: os.Getpid()} }, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	listResponse, err := (&http.Client{Timeout: time.Second}).Get("http://" + control.state.Address + "/requests")
	if err != nil {
		t.Fatal(err)
	}
	defer listResponse.Body.Close()
	if listResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("request list status = %d", listResponse.StatusCode)
	}
	executionResponse, err := (&http.Client{Timeout: time.Second}).Get("http://" + control.state.Address + "/executions/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer executionResponse.Body.Close()
	if executionResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("execution stream status = %d", executionResponse.StatusCode)
	}
	request, err := http.NewRequest(http.MethodPost, "http://"+control.state.Address+"/requests/consume-cli", strings.NewReader(`{"capability":"cap_test","args":["update"]}`))
	if err != nil {
		t.Fatal(err)
	}
	response, err := (&http.Client{Timeout: time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

func TestRuntimeReloadRequiresRunningServerInSelectedConfigDir(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := requestRuntimeReload(ctx); err == nil {
		t.Fatal("reload succeeded without a running server")
	}
}
