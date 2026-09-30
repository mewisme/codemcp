package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/application"
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
	if !strings.Contains(denied, "Approval request denied") || !strings.Contains(denied, "not now") {
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
	for _, expected := range []string{"Approval request", "Pending", "req_safe", "Arguments", "deploy --token TOP-SECRET", "external mutation requires approval"} {
		if !strings.Contains(viewText, expected) {
			t.Fatalf("request view missing %q: %q", expected, viewText)
		}
	}
}

func TestRequestExplainDetailModesAreDeterministicAndKeepCanonicalRequestSeparate(t *testing.T) {
	request := approval.Request{
		ID:          "req_explain_detail",
		Status:      approval.StatusPending,
		WorkspaceID: "ws_explain",
		TargetTool:  "run_command",
		Title:       "Agent supplied title",
		Command:     "git push origin feature",
		Arguments:   json.RawMessage(`{"command":"git push origin feature"}`),
		GuardCode:   controlguard.CodeExternalMutation,
		GuardReason: "external mutation requires approval",
	}
	tests := []struct {
		name   string
		detail approvalRequestDetailResult
		want   []string
	}{
		{
			name: "off",
			detail: approvalRequestDetailResult{
				Request:       request,
				ExplainStatus: application.ApprovalExplainStatus{Mode: "off", Available: false, Readiness: "unknown", Reason: "approval explanations are disabled"},
				Explanation:   application.ApprovalExplanationResult{RequestID: request.ID, State: application.ApprovalExplanationNone},
			},
			want: []string{"mode", "off", "state", "none"},
		},
		{
			name: "manual-ready",
			detail: approvalRequestDetailResult{
				Request:       request,
				ExplainStatus: application.ApprovalExplainStatus{Mode: "manual", Available: true, ActiveProvider: "openrouter", Model: "openrouter/free", Configured: true, Readiness: "ready"},
				Explanation: application.ApprovalExplanationResult{
					RequestID: request.ID, State: application.ApprovalExplanationReady, Attempt: 1,
					Explanation: &application.ApprovalExplanation{
						Summary: "Pushes the local feature branch to origin.", Steps: []string{"Invoke git push."}, Effects: []string{"May update a remote branch."},
						RiskNotes: []string{"Remote write."}, Unknowns: []string{"Remote policy is unknown."}, ProviderID: "openrouter", Model: "openrouter/free",
					},
				},
			},
			want: []string{"mode", "manual", "state", "ready", "Explanation provenance", "openrouter", "openrouter/free", "Pushes the local feature branch to origin."},
		},
		{
			name: "auto-pending",
			detail: approvalRequestDetailResult{
				Request:       request,
				ExplainStatus: application.ApprovalExplainStatus{Mode: "auto", Available: true, ActiveProvider: "openrouter", Model: "openrouter/free", Configured: true, Readiness: "ready"},
				Explanation:   application.ApprovalExplanationResult{RequestID: request.ID, State: application.ApprovalExplanationPending, Attempt: 1},
			},
			want: []string{"mode", "auto", "state", "pending"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, mode := range []presentation.ResultMode{presentation.ModePlain, presentation.ModeHuman} {
				var first, second bytes.Buffer
				caps := presentation.Capabilities{Width: 120, Unicode: true, Color: false}
				renderApprovalRequestDetail(presentation.New(&first, mode, caps), test.detail)
				renderApprovalRequestDetail(presentation.New(&second, mode, caps), test.detail)
				if first.String() != second.String() {
					t.Fatalf("mode=%v output is not deterministic\nA=%q\nB=%q", mode, first.String(), second.String())
				}
				text := first.String()
				for _, required := range append([]string{"agent title", request.Title, "command", request.Command, "AI explanation"}, test.want...) {
					if !strings.Contains(text, required) {
						t.Fatalf("mode=%v missing %q: %q", mode, required, text)
					}
				}
			}

			firstJSON, err := json.Marshal(test.detail)
			if err != nil {
				t.Fatal(err)
			}
			secondJSON, err := json.Marshal(test.detail)
			if err != nil || !bytes.Equal(firstJSON, secondJSON) {
				t.Fatalf("JSON is not deterministic: err=%v A=%s B=%s", err, firstJSON, secondJSON)
			}
			var decoded map[string]any
			if err := json.Unmarshal(firstJSON, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded["title"] != request.Title || decoded["command"] != request.Command || decoded["explain_status"] == nil || decoded["explanation"] == nil {
				t.Fatalf("JSON lost canonical request or Explain separation: %s", firstJSON)
			}
		})
	}
}

func TestRequestRichViewUsesRailHierarchy(t *testing.T) {
	request := approval.Request{
		ID:          "req_rail",
		Status:      approval.StatusPending,
		WorkspaceID: "ws_rail",
		TargetTool:  "run_command",
		Title:       "Review command",
		Arguments:   json.RawMessage(`{"command":"git push origin feature"}`),
		GuardCode:   controlguard.CodeExternalMutation,
	}
	var output bytes.Buffer
	renderApprovalRequest(presentation.New(&output, presentation.ModeHuman, presentation.Capabilities{Width: 100, Unicode: true, Color: false}), request)
	text := output.String()
	for _, expected := range []string{
		"┌  Approval request",
		"◇  Pending",
		"│  ◆ req_rail",
		"│  │  workspace — ws_rail",
		"◆  Arguments",
		"│  ◆ {",
		"└  Awaiting decision",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("rich request view missing %q: %q", expected, text)
		}
	}
	if strings.ContainsRune(text, 'ℹ') {
		t.Fatalf("rich request view emitted information-source glyph: %q", text)
	}
}

func TestRequestRichPaletteLocalizesPendingAndStructureColor(t *testing.T) {
	request := approval.Request{
		ID:          "req_palette",
		Status:      approval.StatusPending,
		WorkspaceID: "ws_palette",
		TargetTool:  "run_command",
		Title:       "Review command",
		GuardCode:   controlguard.CodeExternalMutation,
	}
	var output bytes.Buffer
	caps := presentation.Capabilities{Width: 100, Unicode: true, Color: true}
	renderApprovalRequest(presentation.New(&output, presentation.ModeHuman, caps), request)
	theme := presentation.NewTheme(caps)
	text := output.String()
	for _, expected := range []string{
		theme.Render(presentation.RoleRail, "┌"),
		theme.Render(presentation.RoleMuted, "◇") + "  " + theme.Render(presentation.RoleHeading, "Pending"),
		theme.Render(presentation.RoleStructure, "◆"),
		theme.Render(presentation.RoleHeading, "req_palette"),
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("request palette missing %q: %q", expected, text)
		}
	}
	for _, forbidden := range []string{
		theme.Render(presentation.RoleActive, "Pending"),
		theme.Render(presentation.RoleStructure, "Pending"),
		theme.Render(presentation.RoleActive, "req_palette"),
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("request palette over-colored settled text %q: %q", forbidden, text)
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

func TestRequestCLIApproveAllowSimilarUsesCanonicalRuntimeGrant(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	manager := approval.NewManager("instance-test")
	challenge, _, err := manager.CreateChallenge(approval.ChallengeInput{
		CallerID: "session-a", WorkspaceID: "ws_a", Source: "tunnel", TargetTool: "run_command",
		Arguments: map[string]any{"workspace_id": "ws_a", "command": "git push origin main"},
		GuardCode: controlguard.CodeExternalMutation, Title: "Push commits", Command: "git push origin main", SimilarCommandPattern: "git push **",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequest(challenge.ID, "session-a", "ws_a")
	if err != nil {
		t.Fatal(err)
	}
	control, err := startRuntimeControl(runtimeControlOptions{Approvals: manager, Reload: func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil }, Status: func() runtimeStatusResult { return runtimeStatusResult{PID: os.Getpid()} }, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()

	output := executeRequestCommand(t, root, []string{"request", "approve", request.ID, "--allow-similar", "--json"})
	var approved approval.Request
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &approved); err != nil || !approved.RuntimeSessionGrant || approved.GrantExpiresAt.IsZero() {
		t.Fatalf("approve output=%q request=%#v err=%v", output, approved, err)
	}
	grants := manager.ListRuntimeGrants("ws_a")
	if len(grants) != 1 || grants[0].ID != request.ID {
		t.Fatalf("runtime grants=%#v", grants)
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

func TestRequestCLITestHelperIsNotPublic(t *testing.T) {
	if command := commandByRelativePath(newRootCommand(), "request create dummy"); command != nil {
		t.Fatalf("test-only approval helper is public at %q", command.CommandPath())
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
		CallerID: sessionID, SessionHash: "hash-" + sessionID, WorkspaceID: workspaceID, Source: "tunnel", TargetTool: "run_command",
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
