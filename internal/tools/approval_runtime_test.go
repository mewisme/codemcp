package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	managedagent "go.mewis.me/codemcp/internal/agent"
	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/checkpoint"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/controlguard"
	"go.mewis.me/codemcp/internal/integrations/semantic"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	"go.mewis.me/codemcp/internal/workspace"
)

type approvalToolCallResult struct {
	result Result
	err    error
}

func newApprovalRuntime(t *testing.T) (*Runtime, string) {
	t.Helper()
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	identity, err := manager.Instance()
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	runtime := &Runtime{Registry: registry, Workspaces: manager, SessionAccess: NewSessionWorkspaceAccessManager(), Approvals: approval.NewManager(identity.ID)}
	guardedSchema := Schema{Name: "guarded_action", InputSchema: json.RawMessage(`{"type":"object","properties":{"workspace_id":{"type":"string"},"command":{"type":"string"}},"required":["workspace_id","command"],"additionalProperties":false}`), Approval: inlineApprovalMetadata()}
	registry.MustRegister("guarded_action", guardedSchema, func(ctx context.Context, args map[string]any) (Result, error) {
		if requestID := ApprovalRequestID(ctx); requestID != "" {
			_, hasApprovalMetadata := args[InlineApprovalArgumentKey]
			return JSONResult(map[string]any{"approved_request": requestID, "command": args["command"], "runtime_metadata_present": hasApprovalMetadata}), nil
		}
		command, _ := args["command"].(string)
		return Result{}, controlguard.New(controlguard.CodeControlPlaneMutation, "guarded action requires approval", true, &controlguard.Invocation{Program: "cm", Args: []string{"update"}, Command: command})
	})
	registry.MustRegister("hard_guarded_action", Schema{Name: "hard_guarded_action", InputSchema: guardedSchema.InputSchema}, func(context.Context, map[string]any) (Result, error) {
		return Result{}, controlguard.New(controlguard.CodeProtectedState, "protected state access denied", false, nil)
	})
	return runtime, item.ID
}

func newApprovalShellRuntime(t *testing.T) (*Runtime, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(configformat.EnvConfigDir, "")
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	identity, err := manager.Instance()
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	shell := shellruntime.NewManager(manager, filepath.Join(t.TempDir(), "shell-state"))
	processes := shellruntime.NewProcessManager(manager, shell)
	runtime := &Runtime{Registry: registry, Workspaces: manager, Checkpoints: checkpoint.NewStore(filepath.Join(t.TempDir(), "checkpoints")), SessionAccess: NewSessionWorkspaceAccessManager(), Approvals: approval.NewManager(identity.ID), Shell: shell, Processes: processes, Semantic: semantic.NewManager(semantic.ManagerOptions{})}
	runtime.SetSemanticApprovalPolicy(DefaultSemanticApprovalPolicy())
	RegisterShellTools(registry, manager, shell, processes)
	return runtime, item.ID
}

func newApprovalCodeMCPSourceRuntime(t *testing.T) (*Runtime, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(configformat.EnvConfigDir, "")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module go.mewis.me/codemcp\n\ngo 1.27\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := manager.Instance()
	if err != nil {
		t.Fatal(err)
	}
	shell := shellruntime.NewManager(manager, filepath.Join(t.TempDir(), "shell-state"))
	runtime := &Runtime{Workspaces: manager, Approvals: approval.NewManager(identity.ID), Shell: shell}
	return runtime, item.ID
}

func configureSemanticApprovalClassifier(t *testing.T, runtime *Runtime, classifier semantic.RiskClassifier) {
	t.Helper()
	if runtime.Semantic == nil {
		runtime.Semantic = semantic.NewManager(semantic.ManagerOptions{})
	}
	if err := runtime.Semantic.RegisterProvider("fake", semantic.ProviderRegistration{RiskClassifier: classifier, Model: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Semantic.SelectProvider("fake"); err != nil {
		t.Fatal(err)
	}
	policy := DefaultSemanticApprovalPolicy()
	policy.Enabled = true
	policy.Provider = "fake"
	runtime.SetSemanticApprovalPolicy(policy)
}

func newApprovalDispatchRuntime(t *testing.T) (*Runtime, string) {
	t.Helper()
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	identity, err := manager.Instance()
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	runtime := &Runtime{Registry: registry, Workspaces: manager, SessionAccess: NewSessionWorkspaceAccessManager(), Approvals: approval.NewManager(identity.ID)}
	registry.MustRegister("run_command", Schema{Name: "run_command", InputSchema: json.RawMessage(`{"type":"object","properties":{"workspace_id":{"type":"string"},"command":{"type":"string"}},"required":["workspace_id","command"],"additionalProperties":false}`), Approval: inlineApprovalMetadata()}, func(ctx context.Context, args map[string]any) (Result, error) {
		if granted, ok := controlguard.ApprovalFromContext(ctx); ok {
			return JSONResult(map[string]any{"request_id": granted.RequestID, "capability": granted.Capability, "command": granted.Invocation.Command}), nil
		}
		command, _ := args["command"].(string)
		invocation, _ := workspace.DirectControlPlaneInvocation(command)
		return Result{}, controlguard.New(controlguard.CodeControlPlaneMutation, "control-plane mutation denied", true, invocation)
	})
	return runtime, item.ID
}

func approvalContext(sessionID string) context.Context {
	ctx := WithCallSource(WithMCPSessionID(context.Background(), sessionID), "tunnel")
	return WithApprovalCorrelation(ctx, sessionID, "apr-test")
}

func inlineApprovalArgs(args map[string]any, challengeID, title string) map[string]any {
	result := cloneMap(args)
	result[InlineApprovalArgumentKey] = map[string]any{
		InlineApprovalChallengeID: challengeID,
		InlineApprovalTitle:       title,
	}
	return result
}

func TestApprovalAuthorityRequiresCoreCorrelationNotRawMCPSessionID(t *testing.T) {
	runtime, workspaceID := newApprovalRuntime(t)
	rawOnly := WithCallSource(WithMCPSessionID(context.Background(), "session-shared"), "tunnel")
	result, err := runtime.Call(rawOnly, "guarded_action", map[string]any{"workspace_id": workspaceID, "command": "cm update"})
	if err != nil || !result.IsError {
		t.Fatalf("raw-session call=%#v err=%v", result, err)
	}
	if _, ok := result.StructuredContent.(approvalRequiredResponse); ok {
		t.Fatalf("raw MCP session id became approval authority: %#v", result.StructuredContent)
	}

	trusted := WithApprovalCorrelation(rawOnly, "caller-trusted", "apr-one")
	guarded, err := runtime.Call(trusted, "guarded_action", map[string]any{"workspace_id": workspaceID, "command": "cm update"})
	if err != nil || !guarded.IsError {
		t.Fatalf("trusted guarded call=%#v err=%v", guarded, err)
	}
	challenge := guarded.StructuredContent.(approvalRequiredResponse)
	spoofed := WithApprovalCorrelation(rawOnly, "caller-other", "apr-two")
	requested, err := runtime.Call(spoofed, "guarded_action", inlineApprovalArgs(map[string]any{"workspace_id": workspaceID, "command": "cm update"}, challenge.ChallengeID, "Update CodeMCP"))
	if err != nil || !requested.IsError || !strings.Contains(requested.Content[0].Text, approval.ErrChallengeMismatch.Error()) {
		t.Fatalf("spoofed caller reused challenge: %#v err=%v", requested, err)
	}
}

func TestHostConfirmationMetadataCannotBypassCodeMCPApproval(t *testing.T) {
	runtime, workspaceID := newApprovalRuntime(t)
	ctx := approvalContext("session-confirmed")
	ctx = WithCallDetails(ctx, "tools/call", map[string]any{
		"name":  "guarded_action",
		"_meta": map[string]any{"confirmed": true, "approval": "granted"},
	})
	result, err := runtime.Call(ctx, "guarded_action", map[string]any{"workspace_id": workspaceID, "command": "cm update"})
	if err != nil || !result.IsError {
		t.Fatalf("host-confirmed guarded call=%#v err=%v", result, err)
	}
	challenge, ok := result.StructuredContent.(approvalRequiredResponse)
	if !ok || challenge.Code != "approval_required" {
		t.Fatalf("host confirmation bypassed CodeMCP approval: %#v", result.StructuredContent)
	}
}

func TestApprovalRequiredGuidanceUsesSameToolInlineRetry(t *testing.T) {
	runtime, workspaceID := newApprovalRuntime(t)
	ctx := approvalContext("session-a")
	args := map[string]any{"workspace_id": workspaceID, "command": "cm update"}
	first, err := runtime.Call(ctx, "guarded_action", args)
	if err != nil || !first.IsError {
		t.Fatalf("guarded call = %#v err=%v", first, err)
	}
	challenge, ok := first.StructuredContent.(approvalRequiredResponse)
	if !ok || challenge.Code != "approval_required" || challenge.ChallengeID == "" || challenge.WorkspaceID != workspaceID || challenge.TargetTool != "guarded_action" || challenge.Command != "cm update" || challenge.Instruction == "" {
		t.Fatalf("challenge = %#v", first.StructuredContent)
	}
	arguments, ok := challenge.Arguments.(map[string]any)
	if !ok || arguments["workspace_id"] != workspaceID || arguments["command"] != "cm update" {
		t.Fatalf("challenge arguments = %#v", challenge.Arguments)
	}
	for _, required := range []string{"guarded_action", InlineApprovalArgumentKey, challenge.ChallengeID, "original business arguments", "action title", "same call"} {
		if !strings.Contains(challenge.Instruction, required) || !strings.Contains(first.Content[0].Text, required) {
			t.Fatalf("approval guidance missing %q: instruction=%q text=%q", required, challenge.Instruction, first.Content[0].Text)
		}
	}
	if strings.Contains(first.Content[0].Text, "request_control_approval") {
		t.Fatalf("approval guidance still references standalone request tool: %q", first.Content[0].Text)
	}
}

func TestWorkspaceRegisterIsNotExposedAsAgentTool(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	registry := NewRegistry()
	RegisterWorkspaceTools(registry, manager)
	if _, ok := registry.Schema("workspace_register"); ok {
		t.Fatal("workspace_register is still exposed")
	}
}

func TestRuntimeApprovalMismatchDoesNotConsumeGrant(t *testing.T) {
	runtime, workspaceID := newApprovalRuntime(t)
	ctx := approvalContext("session-a")
	args := map[string]any{"workspace_id": workspaceID, "command": "cm update"}
	first, _ := runtime.Call(ctx, "guarded_action", args)
	challenge := first.StructuredContent.(approvalRequiredResponse)
	request, _, err := runtime.Approvals.CreateRequestWithTitle(challenge.ChallengeID, "session-a", workspaceID, "Update CodeMCP")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Approvals.Approve(request.ID, "test", ""); err != nil {
		t.Fatal(err)
	}
	mismatched, err := runtime.Call(ctx, "guarded_action", map[string]any{"workspace_id": workspaceID, "command": "cm update --version v2.0.0"})
	if err != nil || !mismatched.IsError {
		t.Fatalf("mismatch = %#v err=%v", mismatched, err)
	}
	body, ok := mismatched.StructuredContent.(approvalMismatchResponse)
	if !ok || body.Code != "approval_mismatch" || body.RequestID != request.ID || body.Expected.Tool != "guarded_action" || body.Actual.Tool != "guarded_action" {
		t.Fatalf("mismatch body = %#v", mismatched.StructuredContent)
	}
	value, ok := runtime.Approvals.Get(request.ID)
	if !ok || value.Status != approval.StatusApproved {
		t.Fatalf("mismatch consumed grant: %#v ok=%t", value, ok)
	}
	retry, err := runtime.Call(ctx, "guarded_action", args)
	if err != nil || retry.IsError {
		t.Fatalf("exact retry after mismatch = %#v err=%v", retry, err)
	}
}

func TestRuntimeInlineApprovalExecutesOnSecondCallAndStripsControlMetadata(t *testing.T) {
	runtime, workspaceID := newApprovalRuntime(t)
	ctx := approvalContext("session-inline")
	args := map[string]any{"workspace_id": workspaceID, "command": "cm update"}
	first, err := runtime.Call(ctx, "guarded_action", args)
	if err != nil || !first.IsError {
		t.Fatalf("first guarded call = %#v err=%v", first, err)
	}
	challenge := first.StructuredContent.(approvalRequiredResponse)
	inlineArgs := inlineApprovalArgs(args, challenge.ChallengeID, "Update CodeMCP")
	observed := make([]CallObservation, 0, 2)
	runtime.SetCallObserver(func(value CallObservation) {
		observed = append(observed, value)
	})
	callCtx := WithCallDetails(ctx, "tools/call", map[string]any{"name": "guarded_action", "arguments": inlineArgs})
	callCtx = WithCallRequest(callCtx, map[string]any{
		"jsonrpc": "2.0",
		"id":      "inline-call",
		"method":  "tools/call",
		"params":  map[string]any{"name": "guarded_action", "arguments": inlineArgs},
	})
	resultCh := make(chan approvalToolCallResult, 1)
	go func() {
		result, err := runtime.Call(callCtx, "guarded_action", inlineArgs)
		resultCh <- approvalToolCallResult{result: result, err: err}
	}()
	request := waitForPendingApproval(t, runtime.Approvals)
	if request.Title != "Update CodeMCP" || request.TargetTool != "guarded_action" {
		t.Fatalf("pending inline request = %#v", request)
	}
	if _, err := runtime.Approvals.Approve(request.ID, "reviewer", "reviewed"); err != nil {
		t.Fatal(err)
	}
	resolved := <-resultCh
	if resolved.err != nil || resolved.result.IsError {
		t.Fatalf("inline approved call = %#v err=%v", resolved.result, resolved.err)
	}
	payload, ok := resolved.result.StructuredContent.(map[string]any)
	if !ok || payload["approved_request"] != request.ID || payload["command"] != "cm update" || payload["runtime_metadata_present"] != false {
		t.Fatalf("inline approved payload = %#v", resolved.result.StructuredContent)
	}
	consumed, ok := runtime.Approvals.Get(request.ID)
	if !ok || consumed.Status != approval.StatusConsumed || consumed.ConsumedAt.IsZero() {
		t.Fatalf("inline request not consumed = %#v ok=%t", consumed, ok)
	}
	data, err := json.Marshal(observed)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{InlineApprovalArgumentKey, "Update CodeMCP", challenge.ChallengeID} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("inline control metadata leaked into observation %q: %s", forbidden, data)
		}
	}
	replay, err := runtime.Call(ctx, "guarded_action", args)
	if err != nil || !replay.IsError {
		t.Fatalf("inline approval replay = %#v err=%v", replay, err)
	}
	if next, ok := replay.StructuredContent.(approvalRequiredResponse); !ok || next.ChallengeID == "" || next.ChallengeID == challenge.ChallengeID {
		t.Fatalf("inline replay did not require fresh challenge: %#v", replay.StructuredContent)
	}
}

func TestManagedChildInlineApprovalKeepsCanonicalReviewAndSuppressesUserNotifications(t *testing.T) {
	runtime, workspaceID := newCompletionToolRuntime(t)
	backend := &completionManagedBackend{}
	if err := runtime.Agents.RegisterBackend(backend); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Agents.Configure("completion-test", managedagent.Capacity{MaxParallel: 5}); err != nil {
		t.Fatal(err)
	}
	spawned, err := runtime.Agents.Spawn(context.Background(), managedagent.OperatorController(), managedagent.ManagedSpawnRequest{Input: managedagent.SpawnInput{
		WorkspaceID: workspaceID, Prompt: "managed child inline approval",
	}})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := runtime.Agents.IssueClaim(spawned.ID)
	if err != nil {
		t.Fatal(err)
	}
	childCtx := WithTrustedControllerID(context.Background(), "openai:managed-child-inline-approval")
	childIdentity := RuntimeStateIdentity(childCtx)
	if _, err := runtime.Agents.ConsumeClaim(spawned.ID, credential.Token(), childIdentity); err != nil {
		t.Fatal(err)
	}
	const toolName = "managed_child_guarded_action"
	runtime.Registry.MustRegister(toolName, Schema{
		Name:        toolName,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"workspace_id":{"type":"string"},"command":{"type":"string"}},"required":["workspace_id","command"],"additionalProperties":false}`),
		Approval:    inlineApprovalMetadata(),
	}, func(ctx context.Context, args map[string]any) (Result, error) {
		if requestID := ApprovalRequestID(ctx); requestID != "" {
			return JSONResult(map[string]any{"approved_request": requestID, "command": args["command"]}), nil
		}
		command, _ := args["command"].(string)
		return Result{}, controlguard.New(
			controlguard.CodeControlPlaneMutation,
			"managed child action requires approval",
			true,
			&controlguard.Invocation{Program: "cm", Args: []string{"update"}, Command: command},
		)
	})
	childCtx = WithCallSource(childCtx, "tunnel")
	childCtx = WithApprovalCorrelation(childCtx, childIdentity, "managed-child-inline-request")
	args := map[string]any{"workspace_id": workspaceID, "command": "cm update"}
	first, err := runtime.Call(childCtx, toolName, args)
	if err != nil || !first.IsError {
		t.Fatalf("managed child challenge=%#v err=%v", first, err)
	}
	challenge := first.StructuredContent.(approvalRequiredResponse)
	challengeSuppressed := false
	for _, event := range runtime.Approvals.Events().Recent(16) {
		if event.ChallengeID == challenge.ChallengeID && event.SuppressNotifications {
			challengeSuppressed = true
		}
	}
	if !challengeSuppressed {
		t.Fatal("managed child approval challenge did not suppress user notifications")
	}
	resultCh := make(chan approvalToolCallResult, 1)
	go func() {
		result, err := runtime.Call(childCtx, toolName, inlineApprovalArgs(args, challenge.ChallengeID, "Update CodeMCP"))
		resultCh <- approvalToolCallResult{result: result, err: err}
	}()
	request := waitForPendingApproval(t, runtime.Approvals)
	requestSuppressed := false
	for _, event := range runtime.Approvals.Events().Recent(16) {
		if event.RequestID == request.ID && event.SuppressNotifications {
			requestSuppressed = true
		}
	}
	if !requestSuppressed {
		t.Fatal("managed child approval request did not suppress user notifications")
	}
	review := approval.NewReviewService(runtime.Approvals)
	visible, err := review.View(request.ID)
	if err != nil || visible.ID != request.ID || visible.Status != approval.StatusPending {
		t.Fatalf("canonical review view=%#v err=%v", visible, err)
	}
	approved, err := review.Resolve(approval.ReviewInput{
		Request: request.ID, Decision: approval.ReviewApprove, ResolvedBy: "reviewer", Reason: "reviewed",
	})
	if err != nil || approved.Status != approval.StatusApproved {
		t.Fatalf("canonical review approval=%#v err=%v", approved, err)
	}
	resolved := <-resultCh
	if resolved.err != nil || resolved.result.IsError {
		t.Fatalf("managed child approved result=%#v err=%v", resolved.result, resolved.err)
	}
	consumed, ok := runtime.Approvals.Get(request.ID)
	if !ok || consumed.Status != approval.StatusConsumed {
		t.Fatalf("managed child approval request=%#v ok=%t", consumed, ok)
	}
}

func TestRuntimeInlineApprovalTerminalDecisionsDoNotDispatch(t *testing.T) {
	for _, test := range []struct {
		name    string
		resolve func(*approval.Manager, string) error
		status  approval.Status
	}{
		{name: "deny", status: approval.StatusDenied, resolve: func(manager *approval.Manager, id string) error {
			_, err := manager.Deny(id, "reviewer", "not now")
			return err
		}},
		{name: "cancel", status: approval.StatusCancelled, resolve: func(manager *approval.Manager, id string) error {
			_, err := manager.Cancel(id, "reviewer", "cancelled")
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime, workspaceID := newApprovalRuntime(t)
			ctx := approvalContext("session-inline-" + test.name)
			args := map[string]any{"workspace_id": workspaceID, "command": "cm update"}
			first, _ := runtime.Call(ctx, "guarded_action", args)
			challenge := first.StructuredContent.(approvalRequiredResponse)
			resultCh := make(chan approvalToolCallResult, 1)
			go func() {
				result, err := runtime.Call(ctx, "guarded_action", inlineApprovalArgs(args, challenge.ChallengeID, "Update CodeMCP"))
				resultCh <- approvalToolCallResult{result: result, err: err}
			}()
			request := waitForPendingApproval(t, runtime.Approvals)
			if err := test.resolve(runtime.Approvals, request.ID); err != nil {
				t.Fatal(err)
			}
			resolved := <-resultCh
			if resolved.err != nil || !resolved.result.IsError {
				t.Fatalf("terminal inline result = %#v err=%v", resolved.result, resolved.err)
			}
			body, ok := resolved.result.StructuredContent.(approvalResolutionResponse)
			if !ok || body.Status != test.status {
				t.Fatalf("terminal inline body = %#v", resolved.result.StructuredContent)
			}
		})
	}
}

func TestRuntimeInlineApprovalWaitCancellationDoesNotExecute(t *testing.T) {
	runtime, workspaceID := newApprovalRuntime(t)
	base := approvalContext("session-inline-cancelled")
	args := map[string]any{"workspace_id": workspaceID, "command": "cm update"}
	first, _ := runtime.Call(base, "guarded_action", args)
	challenge := first.StructuredContent.(approvalRequiredResponse)
	ctx, cancel := context.WithCancel(base)
	resultCh := make(chan approvalToolCallResult, 1)
	go func() {
		result, err := runtime.Call(ctx, "guarded_action", inlineApprovalArgs(args, challenge.ChallengeID, "Update CodeMCP"))
		resultCh <- approvalToolCallResult{result: result, err: err}
	}()
	request := waitForPendingApproval(t, runtime.Approvals)
	cancel()
	resolved := <-resultCh
	if resolved.err != nil || !resolved.result.IsError || !strings.Contains(resolved.result.Content[0].Text, "context canceled") {
		t.Fatalf("cancelled inline wait = %#v err=%v", resolved.result, resolved.err)
	}
	value, ok := runtime.Approvals.Get(request.ID)
	if !ok || value.Status != approval.StatusPending {
		t.Fatalf("cancelled inline wait mutated request = %#v ok=%t", value, ok)
	}
	if _, err := runtime.Approvals.Approve(request.ID, "reviewer", "reviewed after waiter detached"); err != nil {
		t.Fatal(err)
	}
	retry, err := runtime.Call(base, "guarded_action", args)
	if err != nil || retry.IsError {
		t.Fatalf("exact recovery after cancelled inline wait = %#v err=%v", retry, err)
	}
}

func TestRuntimeRejectsInlineApprovalOnUnmarkedTool(t *testing.T) {
	runtime, workspaceID := newApprovalRuntime(t)
	result, err := runtime.Call(approvalContext("session-unmarked"), "hard_guarded_action", map[string]any{
		"workspace_id": workspaceID,
		"command":      "read protected state",
		InlineApprovalArgumentKey: map[string]any{
			InlineApprovalChallengeID: "chg_fake",
			InlineApprovalTitle:       "Read protected state",
		},
	})
	if err != nil || !result.IsError || !strings.Contains(result.Content[0].Text, "does not support inline approval") {
		t.Fatalf("unmarked inline approval = %#v err=%v", result, err)
	}
}

func TestRuntimeRejectsMalformedInlineApprovalBeforeDispatch(t *testing.T) {
	for _, test := range []struct {
		name     string
		envelope any
	}{
		{name: "not object", envelope: "chg_fake"},
		{name: "missing title", envelope: map[string]any{InlineApprovalChallengeID: "chg_fake"}},
		{name: "extra field", envelope: map[string]any{InlineApprovalChallengeID: "chg_fake", InlineApprovalTitle: "Review", "approved": true}},
		{name: "empty challenge", envelope: map[string]any{InlineApprovalChallengeID: " ", InlineApprovalTitle: "Review"}},
		{name: "empty title", envelope: map[string]any{InlineApprovalChallengeID: "chg_fake", InlineApprovalTitle: " "}},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime, workspaceID := newApprovalRuntime(t)
			result, err := runtime.Call(approvalContext("malformed-"+test.name), "guarded_action", map[string]any{
				"workspace_id":            workspaceID,
				"command":                 "cm update",
				InlineApprovalArgumentKey: test.envelope,
			})
			if err != nil || !result.IsError {
				t.Fatalf("malformed inline result=%#v err=%v", result, err)
			}
			if requests := runtime.Approvals.List(approval.Filter{}); len(requests) != 0 {
				t.Fatalf("malformed inline envelope created approval state: %#v", requests)
			}
		})
	}
}

func TestRuntimeSessionGrantAllowsSimilarCommandAcrossMCPSessions(t *testing.T) {
	runtime, workspaceID := newApprovalRuntime(t)
	firstCtx := approvalContext("session-a")
	first, err := runtime.Call(firstCtx, "guarded_action", map[string]any{"workspace_id": workspaceID, "command": "git push origin main"})
	if err != nil || !first.IsError {
		t.Fatalf("first guarded call = %#v err=%v", first, err)
	}
	challenge := first.StructuredContent.(approvalRequiredResponse)
	request, _, err := runtime.Approvals.CreateRequestWithTitle(challenge.ChallengeID, "session-a", workspaceID, "Push Git commits")
	if err != nil {
		t.Fatal(err)
	}
	if request.SimilarCommandPattern != "git push **" {
		t.Fatalf("similar pattern=%q", request.SimilarCommandPattern)
	}
	if _, err := runtime.Approvals.ApproveRuntimeSession(request.ID, "test", ""); err != nil {
		t.Fatal(err)
	}
	second, err := runtime.Call(approvalContext("session-b"), "guarded_action", map[string]any{"workspace_id": workspaceID, "command": "git push origin feature"})
	if err != nil || second.IsError {
		t.Fatalf("runtime-session grant call = %#v err=%v", second, err)
	}
	payload, ok := second.StructuredContent.(map[string]any)
	if !ok || payload["approved_request"] != request.ID || payload["command"] != "git push origin feature" {
		t.Fatalf("runtime-session grant payload = %#v", second.StructuredContent)
	}
	blocked, err := runtime.Call(approvalContext("session-c"), "guarded_action", map[string]any{"workspace_id": workspaceID, "command": "git status"})
	if err != nil || !blocked.IsError {
		t.Fatalf("different command unexpectedly granted = %#v err=%v", blocked, err)
	}
}

func TestRuntimeInlineApprovalRejectsFakeChallengeAndSessionMismatch(t *testing.T) {
	runtime, workspaceID := newApprovalRuntime(t)
	ctxA := approvalContext("session-a")
	args := map[string]any{"workspace_id": workspaceID, "command": "cm update"}
	fake, err := runtime.Call(ctxA, "guarded_action", inlineApprovalArgs(args, "chg_missing", "Update CodeMCP"))
	if err != nil || !fake.IsError || !strings.Contains(fake.Content[0].Text, approval.ErrChallengeNotFound.Error()) {
		t.Fatalf("fake challenge = %#v err=%v", fake, err)
	}
	guarded, _ := runtime.Call(ctxA, "guarded_action", args)
	challenge := guarded.StructuredContent.(approvalRequiredResponse)
	ctxB := approvalContext("session-b")
	mismatch, err := runtime.Call(ctxB, "guarded_action", inlineApprovalArgs(args, challenge.ChallengeID, "Update CodeMCP"))
	if err != nil || !mismatch.IsError || !strings.Contains(mismatch.Content[0].Text, approval.ErrChallengeMismatch.Error()) {
		t.Fatalf("session mismatch = %#v err=%v", mismatch, err)
	}
}

func TestRuntimeDoesNotChallengeNonApprovableGuard(t *testing.T) {
	runtime, workspaceID := newApprovalRuntime(t)
	result, err := runtime.Call(approvalContext("session-a"), "hard_guarded_action", map[string]any{"workspace_id": workspaceID, "command": "read protected state"})
	if err != nil || !result.IsError || result.StructuredContent != nil || !strings.Contains(result.Content[0].Text, "protected state access denied") {
		t.Fatalf("hard guard = %#v err=%v", result, err)
	}
	if requests := runtime.Approvals.List(approval.Filter{}); len(requests) != 0 {
		t.Fatalf("hard guard created approval requests: %#v", requests)
	}
}

func TestShellControlGuardProducesChallengeOnlyForDirectLiteralCLI(t *testing.T) {
	runtime, workspaceID := newApprovalShellRuntime(t)
	ctx := approvalContext("session-a")
	direct, err := runtime.Call(ctx, "run_command", map[string]any{"workspace_id": workspaceID, "command": "cm update"})
	if err != nil || !direct.IsError {
		t.Fatalf("direct guard = %#v err=%v", direct, err)
	}
	challenge, ok := direct.StructuredContent.(approvalRequiredResponse)
	if !ok || challenge.TargetTool != "run_command" || challenge.GuardCode != string(controlguard.CodeControlPlaneMutation) || challenge.Command != "cm update" {
		t.Fatalf("direct challenge = %#v", direct.StructuredContent)
	}
	for _, command := range []string{`bash -lc "cm update"`, `exec cm update`, `cm update && echo done`, `unset CM_TOOL_CONTEXT`} {
		result, err := runtime.Call(ctx, "run_command", map[string]any{"workspace_id": workspaceID, "command": command})
		if err != nil || !result.IsError {
			t.Fatalf("hard denied command %q = %#v err=%v", command, result, err)
		}
		if _, ok := result.StructuredContent.(approvalRequiredResponse); ok {
			t.Fatalf("hard denied command became approval challenge: %q -> %#v", command, result.StructuredContent)
		}
	}
	background, err := runtime.Call(ctx, "start_process", map[string]any{"workspace_id": workspaceID, "command": "cm update"})
	if err != nil || !background.IsError {
		t.Fatalf("background guard = %#v err=%v", background, err)
	}
	backgroundChallenge, ok := background.StructuredContent.(approvalRequiredResponse)
	if !ok || backgroundChallenge.TargetTool != "start_process" {
		t.Fatalf("background challenge = %#v", background.StructuredContent)
	}
}

func TestApprovalRuntimeResolvesSourceRunThroughUnifiedClassifier(t *testing.T) {
	runtime, workspaceID := newApprovalCodeMCPSourceRuntime(t)
	command := "go run . config set http.mcp.port 41001"
	invocation, ok := runtime.directControlPlaneInvocation(workspaceID, command)
	if !ok || invocation == nil {
		t.Fatalf("source-run invocation unresolved: %#v ok=%t", invocation, ok)
	}
	if invocation.Program != "cm" || strings.Join(invocation.Args, " ") != "config set http.mcp.port 41001" || invocation.Command != command {
		t.Fatalf("source-run invocation was not canonicalized for approval: %#v", invocation)
	}
}

func TestDestructiveShellApprovalIsExactOneShotAndWorkspaceBound(t *testing.T) {
	runtime, workspaceID := newApprovalShellRuntime(t)
	item, err := runtime.Workspaces.Get(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(item.Path, "delete-me.txt")
	if err := os.WriteFile(target, []byte("keep until approved"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := approvalContext("session-destructive")
	args := map[string]any{"workspace_id": workspaceID, "command": "rm delete-me.txt"}
	guarded, err := runtime.Call(ctx, "run_command", args)
	if err != nil || !guarded.IsError {
		t.Fatalf("destructive guard = %#v err=%v", guarded, err)
	}
	challenge, ok := guarded.StructuredContent.(approvalRequiredResponse)
	if !ok || challenge.GuardCode != string(controlguard.CodeDestructiveMutation) || challenge.TargetTool != "run_command" || challenge.Command != "rm delete-me.txt" {
		t.Fatalf("destructive challenge = %#v", guarded.StructuredContent)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("guarded command mutated file before approval: %v", err)
	}
	request, _, err := runtime.Approvals.CreateRequestWithTitle(challenge.ChallengeID, "session-destructive", workspaceID, "Delete generated file")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Approvals.Approve(request.ID, "test", "reviewed deletion"); err != nil {
		t.Fatal(err)
	}
	mismatch, err := runtime.Call(ctx, "run_command", map[string]any{"workspace_id": workspaceID, "command": "rm another.txt"})
	if err != nil || !mismatch.IsError {
		t.Fatalf("destructive mismatch = %#v err=%v", mismatch, err)
	}
	mismatchBody, ok := mismatch.StructuredContent.(approvalMismatchResponse)
	if !ok || mismatchBody.RequestID != request.ID {
		t.Fatalf("destructive mismatch body = %#v", mismatch.StructuredContent)
	}
	approved, err := runtime.Call(ctx, "run_command", args)
	if err != nil || approved.IsError {
		t.Fatalf("approved destructive retry = %#v err=%v", approved, err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("approved deletion did not remove target: %v", err)
	}
	consumed, ok := runtime.Approvals.Get(request.ID)
	if !ok || consumed.Status != approval.StatusConsumed || consumed.ConsumedAt.IsZero() {
		t.Fatalf("destructive approval not consumed: %#v ok=%t", consumed, ok)
	}
	replayed, err := runtime.Call(ctx, "run_command", args)
	if err != nil || !replayed.IsError {
		t.Fatalf("destructive replay = %#v err=%v", replayed, err)
	}
	replayChallenge, ok := replayed.StructuredContent.(approvalRequiredResponse)
	if !ok || replayChallenge.ChallengeID == "" || replayChallenge.ChallengeID == challenge.ChallengeID {
		t.Fatalf("destructive replay did not require new approval: %#v", replayed.StructuredContent)
	}
}

func TestInlineApprovedShellRetryCarriesOneShotChildCapability(t *testing.T) {
	runtime, workspaceID := newApprovalDispatchRuntime(t)
	ctx := approvalContext("session-inline-shell")
	args := map[string]any{"workspace_id": workspaceID, "command": "cm update"}
	guarded, err := runtime.Call(ctx, "run_command", args)
	if err != nil || !guarded.IsError {
		t.Fatalf("guarded shell call = %#v err=%v", guarded, err)
	}
	challenge := guarded.StructuredContent.(approvalRequiredResponse)
	resultCh := make(chan approvalToolCallResult, 1)
	go func() {
		result, err := runtime.Call(ctx, "run_command", inlineApprovalArgs(args, challenge.ChallengeID, "Update CodeMCP"))
		resultCh <- approvalToolCallResult{result: result, err: err}
	}()
	request := waitForPendingApproval(t, runtime.Approvals)
	if _, err := runtime.Approvals.Approve(request.ID, "reviewer", "reviewed"); err != nil {
		t.Fatal(err)
	}
	resolved := <-resultCh
	if resolved.err != nil || resolved.result.IsError {
		t.Fatalf("inline approved shell retry = %#v err=%v", resolved.result, resolved.err)
	}
	payload := resolved.result.StructuredContent.(map[string]any)
	capability, _ := payload["capability"].(string)
	if payload["request_id"] != request.ID || capability == "" || payload["command"] != "cm update" {
		t.Fatalf("inline shell payload = %#v", payload)
	}
	value, ok := runtime.Approvals.Get(request.ID)
	if !ok || value.Status != approval.StatusConsumed {
		t.Fatalf("inline shell approval not consumed = %#v ok=%t", value, ok)
	}
	if requestID, err := runtime.Approvals.ConsumeCLI(capability, []string{"update"}); err != nil || requestID != request.ID {
		t.Fatalf("inline child capability consume = %q err=%v", requestID, err)
	}
	if _, err := runtime.Approvals.ConsumeCLI(capability, []string{"update"}); !errors.Is(err, approval.ErrCapabilityNotFound) {
		t.Fatalf("inline child capability replay err=%v", err)
	}
}

func TestApprovedShellRetryCarriesOneShotChildCapability(t *testing.T) {
	runtime, workspaceID := newApprovalDispatchRuntime(t)
	ctx := approvalContext("session-a")
	args := map[string]any{"workspace_id": workspaceID, "command": "cm update"}
	guarded, _ := runtime.Call(ctx, "run_command", args)
	challenge := guarded.StructuredContent.(approvalRequiredResponse)
	request, _, err := runtime.Approvals.CreateRequestWithTitle(challenge.ChallengeID, "session-a", workspaceID, "Update CodeMCP")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Approvals.Approve(request.ID, "test", ""); err != nil {
		t.Fatal(err)
	}
	retry, err := runtime.Call(ctx, "run_command", args)
	if err != nil || retry.IsError {
		t.Fatalf("approved shell retry = %#v err=%v", retry, err)
	}
	payload := retry.StructuredContent.(map[string]any)
	capability, _ := payload["capability"].(string)
	if payload["request_id"] != request.ID || capability == "" || payload["command"] != "cm update" {
		t.Fatalf("approved shell payload = %#v", payload)
	}
	value, ok := runtime.Approvals.Get(request.ID)
	if !ok || value.Status != approval.StatusConsumed {
		t.Fatalf("shell retry did not claim one-shot grant: %#v ok=%t", value, ok)
	}
	if requestID, err := runtime.Approvals.ConsumeCLI(capability, []string{"update"}); err != nil || requestID != request.ID {
		t.Fatalf("child capability consume = %q err=%v", requestID, err)
	}
	if _, err := runtime.Approvals.ConsumeCLI(capability, []string{"update"}); !errors.Is(err, approval.ErrCapabilityNotFound) {
		t.Fatalf("child capability replay err=%v", err)
	}
}

func waitForPendingApproval(t *testing.T, manager *approval.Manager) approval.Request {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		requests := manager.List(approval.Filter{Status: approval.StatusPending})
		if len(requests) == 1 {
			return requests[0]
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("approval request did not become pending")
	return approval.Request{}
}

func TestApprovalContextRoundTrip(t *testing.T) {
	ctx := WithApprovalRequest(context.Background(), "req_test")
	if got := ApprovalRequestID(ctx); got != "req_test" {
		t.Fatalf("approval request id = %q", got)
	}
	if got := ApprovalRequestID(context.TODO()); got != "" {
		t.Fatalf("empty approval request id = %q", got)
	}
}

func TestApprovalMismatchErrorRemainsTyped(t *testing.T) {
	var mismatch *approval.MismatchError
	err := &approval.MismatchError{RequestID: "req_test", TargetTool: "run_command"}
	if !errors.As(err, &mismatch) || mismatch.RequestID != "req_test" {
		t.Fatalf("mismatch error = %#v", mismatch)
	}
}
