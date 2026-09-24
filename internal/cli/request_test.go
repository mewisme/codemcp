package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/controlguard"
)

func TestRequestCLIListViewApproveDenyAliasesAndOutput(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	manager := approval.NewManager("instance-test")
	first := seedApprovalRequest(t, manager, "session-a", "ws_a", "cm config set server.port 41001")
	second := seedApprovalRequest(t, manager, "session-b", "ws_b", "cm update")
	control, err := startRuntimeControl(runtimeControlOptions{Approvals: manager, Reload: func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil }, Status: func() runtimeStatusResult { return runtimeStatusResult{PID: os.Getpid()} }, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()

	plain := executeRequestCommand(t, root, []string{"request", "ls"})
	if !strings.Contains(plain, first.ID) || !strings.Contains(plain, second.ID) || !strings.Contains(plain, "Status") || !strings.Contains(plain, "pending") {
		t.Fatalf("plain request list = %q", plain)
	}
	firstPrefix := uniqueRequestPrefix(first.ID, second.ID)
	viewJSON := executeRequestCommand(t, root, []string{"req", "info", firstPrefix, "--json"})
	var viewed approval.Request
	if err := json.Unmarshal([]byte(strings.TrimSpace(viewJSON)), &viewed); err != nil || viewed.ID != first.ID || viewed.Status != approval.StatusPending {
		t.Fatalf("view json = %q value=%#v err=%v", viewJSON, viewed, err)
	}
	viewPlain := executeRequestCommand(t, root, []string{"request", "show", firstPrefix})
	if !strings.Contains(viewPlain, first.Title) || !strings.Contains(viewPlain, "workspace") || !strings.Contains(viewPlain, "Arguments") {
		t.Fatalf("plain request view = %q", viewPlain)
	}

	approvedJSON := executeRequestCommand(t, root, []string{"req", "accept", firstPrefix, "--reason", "reviewed", "--json"})
	var approved approval.Request
	if err := json.Unmarshal([]byte(strings.TrimSpace(approvedJSON)), &approved); err != nil || approved.Status != approval.StatusApproved || approved.ResolvedBy != "cli" || approved.Reason != "reviewed" || approved.RetryUntil.IsZero() {
		t.Fatalf("approve json = %q value=%#v err=%v", approvedJSON, approved, err)
	}
	secondPrefix := uniqueRequestPrefix(second.ID, first.ID)
	denied := executeRequestCommand(t, root, []string{"request", "reject", secondPrefix, "--reason", "not now"})
	if !strings.Contains(denied, "Control approval request denied") || !strings.Contains(denied, "not now") {
		t.Fatalf("plain deny output = %q", denied)
	}
	stored, err := manager.Resolve(second.ID)
	if err != nil || stored.Status != approval.StatusDenied || stored.ResolvedBy != "cli" || stored.Reason != "not now" {
		t.Fatalf("denied request = %#v err=%v", stored, err)
	}
}

func TestRequestReadPresentationKeepsListSafeAndViewExact(t *testing.T) {
	request := approval.Request{
		ID:          "req_safe",
		Status:      approval.StatusPending,
		WorkspaceID: "ws_safe",
		TargetTool:  "run_command",
		Title:       "Review exact command",
		Arguments:   json.RawMessage(`{"workspace_id":"ws_safe","command":"deploy --token TOP-SECRET"}`),
		GuardCode:   controlguard.CodeExternalMutation,
		GuardReason: "external mutation requires approval",
	}

	var listOutput bytes.Buffer
	renderApprovalRequests(presentation.New(&listOutput, presentation.ModePlain, presentation.Capabilities{Width: 100, Unicode: true}), []approval.Request{request})
	listText := listOutput.String()
	for _, expected := range []string{"Control approval requests", "req_safe", "pending", "ws_safe", "run_command", "Review exact command"} {
		if !strings.Contains(listText, expected) {
			t.Fatalf("request list missing %q: %q", expected, listText)
		}
	}
	for _, forbidden := range []string{"TOP-SECRET", "deploy --token", "external mutation requires approval"} {
		if strings.Contains(listText, forbidden) {
			t.Fatalf("request list leaked detail %q: %q", forbidden, listText)
		}
	}

	var viewOutput bytes.Buffer
	renderApprovalRequest(presentation.New(&viewOutput, presentation.ModePlain, presentation.Capabilities{Width: 100, Unicode: true}), request)
	viewText := viewOutput.String()
	for _, expected := range []string{"Control approval request", "req_safe", "Arguments", "deploy --token TOP-SECRET", "external mutation requires approval"} {
		if !strings.Contains(viewText, expected) {
			t.Fatalf("request view missing %q: %q", expected, viewText)
		}
	}
}

func TestRuntimeGrantListUsesStructuredSafeFields(t *testing.T) {
	grant := approval.Request{
		ID:                    "req_grant",
		WorkspaceID:           "ws_grant",
		SimilarCommandPattern: "git push **",
		GrantExpiresAt:        time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC),
		Arguments:             json.RawMessage(`{"command":"git push origin main","token":"DO-NOT-LIST"}`),
	}
	var output bytes.Buffer
	renderRuntimeGrants(presentation.New(&output, presentation.ModePlain, presentation.Capabilities{Width: 100, Unicode: true}), []approval.Request{grant})
	text := output.String()
	for _, expected := range []string{"Runtime session grants", "req_grant", "ws_grant", "git push **", "2026-09-25T12:00:00Z"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("runtime grant list missing %q: %q", expected, text)
		}
	}
	if strings.Contains(text, "DO-NOT-LIST") || strings.Contains(text, "git push origin main") {
		t.Fatalf("runtime grant list leaked exact arguments: %q", text)
	}
}

func TestRequestCLIPlainAndJSON(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	manager := approval.NewManager("instance-test")
	request := seedApprovalRequest(t, manager, "session-a", "ws_a", "cm update")
	control, err := startRuntimeControl(runtimeControlOptions{Approvals: manager, Reload: func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil }, Status: func() runtimeStatusResult { return runtimeStatusResult{PID: os.Getpid()} }, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()

	plain := executeRequestCommand(t, root, []string{"request", "list"})
	if !strings.Contains(plain, request.ID) {
		t.Fatalf("plain=%q", plain)
	}
	jsonOutput := executeRequestCommand(t, root, []string{"request", "list", "--json"})
	var values []approval.Request
	if err := json.Unmarshal([]byte(strings.TrimSpace(jsonOutput)), &values); err != nil || len(values) != 1 || values[0].ID != request.ID {
		t.Fatalf("json=%q values=%#v err=%v", jsonOutput, values, err)
	}
}

func TestRequestCLICreateDummy(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	manager := approval.NewManager("instance-test")
	control, err := startRuntimeControl(runtimeControlOptions{Approvals: manager, Reload: func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil }, Status: func() runtimeStatusResult { return runtimeStatusResult{PID: os.Getpid()} }, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()

	output := executeRequestCommand(t, root, []string{"request", "create", "dummy", "--workspace", "ws_demo", "--title", "Allow demo", "--command", "echo hello", "--json"})
	var request approval.Request
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &request); err != nil {
		t.Fatalf("dummy json=%q err=%v", output, err)
	}
	if request.Status != approval.StatusPending || request.WorkspaceID != "ws_demo" || request.Title != "Allow demo" || request.Source != "cli-dummy" || request.TargetTool != "run_command" {
		t.Fatalf("dummy request=%#v", request)
	}
	var arguments map[string]any
	if err := json.Unmarshal(request.Arguments, &arguments); err != nil || arguments["command"] != "echo hello" || arguments["dummy"] != true || arguments["workspace_id"] != "ws_demo" {
		t.Fatalf("dummy arguments=%s value=%#v err=%v", request.Arguments, arguments, err)
	}
	stored, ok := manager.Get(request.ID)
	if !ok || stored.ID != request.ID || stored.Status != approval.StatusPending {
		t.Fatalf("stored dummy=%#v ok=%t", stored, ok)
	}
	if _, err := manager.Approve(request.ID, "test", ""); err != nil {
		t.Fatal(err)
	}
	if matched, ok, err := manager.MatchApproved(approval.RetryInput{SessionID: "real-session", WorkspaceID: "ws_demo", Source: "cli-dummy", TargetTool: "run_command", Arguments: map[string]any{"workspace_id": "ws_demo", "command": "echo hello", "dummy": true}}); err != nil || ok || matched.ID != "" {
		t.Fatalf("dummy grant matched real session: request=%#v matched=%t err=%v", matched, ok, err)
	}

	plain := executeRequestCommand(t, root, []string{"req", "create", "dummy"})
	if !strings.Contains(plain, "Dummy control approval request created") {
		t.Fatalf("plain dummy=%q", plain)
	}
}

func TestRequestCLIAmbiguousPrefixAndStoppedRuntimeFailClosed(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	manager := approval.NewManager("instance-test")
	seedApprovalRequest(t, manager, "session-a", "ws_a", "cm update")
	seedApprovalRequest(t, manager, "session-b", "ws_b", "cm install")
	control, err := startRuntimeControl(runtimeControlOptions{Approvals: manager, Reload: func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil }, Status: func() runtimeStatusResult { return runtimeStatusResult{PID: os.Getpid()} }, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executeRequestCommandError(root, []string{"request", "view", "req_"}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous prefix err=%v", err)
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := executeRequestCommandError(root, []string{"request", "list"}); err == nil || !strings.Contains(err.Error(), "no running server") {
		t.Fatalf("stopped runtime err=%v", err)
	}
}

func seedApprovalRequest(t *testing.T, manager *approval.Manager, sessionID, workspaceID, command string) approval.Request {
	t.Helper()
	challenge, _, err := manager.CreateChallenge(approval.ChallengeInput{
		SessionID: sessionID, SessionHash: "hash-" + sessionID, WorkspaceID: workspaceID, Source: "tunnel", TargetTool: "run_command",
		Arguments: map[string]any{"workspace_id": workspaceID, "command": command}, GuardCode: controlguard.CodeControlPlaneMutation, GuardReason: "control-plane mutation denied", Title: "Test command approval", Command: command,
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequest(challenge.ID, sessionID, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func uniqueRequestPrefix(id string, others ...string) string {
	for length := len("req_") + 1; length < len(id); length++ {
		prefix := id[:length]
		unique := true
		for _, other := range others {
			if strings.HasPrefix(other, prefix) {
				unique = false
				break
			}
		}
		if unique {
			return prefix
		}
	}
	return id
}

func executeRequestCommand(t *testing.T, root string, args []string) string {
	t.Helper()
	output, err := executeRequestCommandError(root, args)
	if err != nil {
		t.Fatal(err)
	}
	return output
}

func executeRequestCommandError(root string, args []string) (string, error) {
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetContext(context.Background())
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs(append([]string{"--config-dir", root}, args...))
	err := cmd.Execute()
	return output.String(), err
}
