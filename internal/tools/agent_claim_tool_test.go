package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	managedagent "go.mewis.me/codemcp/internal/agent"
)

type claimToolBackend struct{}

func (claimToolBackend) ID() managedagent.BackendID { return "claim-test" }
func (claimToolBackend) Ready(context.Context) (managedagent.Readiness, error) {
	return managedagent.Readiness{Available: true, Capacity: managedagent.Capacity{MaxParallel: 5}}, nil
}
func (claimToolBackend) Spawn(_ context.Context, request managedagent.BackendSpawnRequest) (managedagent.Handle, error) {
	return string(request.AgentID), nil
}
func (claimToolBackend) Send(context.Context, managedagent.Handle, managedagent.Message) error {
	return nil
}
func (claimToolBackend) Snapshot(context.Context, managedagent.Handle) (managedagent.BackendSnapshot, error) {
	return managedagent.BackendSnapshot{Phase: managedagent.BackendPhaseWorking}, nil
}
func (claimToolBackend) Cancel(context.Context, managedagent.Handle) error { return nil }
func (claimToolBackend) Close(context.Context, managedagent.Handle) error  { return nil }

func newClaimToolRuntime(t *testing.T) (*Runtime, string, string, managedagent.Snapshot, managedagent.ClaimCredential) {
	t.Helper()
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := NewRuntime()
	first, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Agents.RegisterBackend(claimToolBackend{}); err != nil {
		t.Fatal(err)
	}
	spawned, err := runtime.Agents.Spawn(t.Context(), managedagent.OperatorController(), managedagent.ManagedSpawnRequest{
		Input: managedagent.SpawnInput{
			WorkspaceID: first.ID,
			Prompt:      "claim security test",
			Backend:     "claim-test",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := runtime.Agents.IssueClaim(spawned.ID)
	if err != nil {
		t.Fatal(err)
	}
	return runtime, first.ID, second.ID, spawned, credential
}

func TestAgentClaimBindsExactWorkspaceAndClearsOrdinaryGrants(t *testing.T) {
	runtime, workspaceID, otherWorkspaceID, spawned, credential := newClaimToolRuntime(t)
	childCtx := WithMCPSessionID(context.Background(), "managed-child-session")

	if _, err := runtime.ResolveWorkspaceAccess(childCtx, otherWorkspaceID); err != nil {
		t.Fatalf("pre-claim ordinary access failed: %v", err)
	}
	if access, ok := runtime.SessionAccess.Lookup("managed-child-session"); !ok || len(access.Workspaces) != 1 {
		t.Fatalf("pre-claim access=%#v ok=%t", access, ok)
	}

	missingSession, err := runtime.Call(context.Background(), AgentClaimToolName, map[string]any{
		"agent_id": string(spawned.ID), "token": credential.Token(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !missingSession.IsError || !strings.Contains(missingSession.Content[0].Text, "trusted MCP session") {
		t.Fatalf("missing-session claim=%#v", missingSession)
	}

	extraWorkspace, err := runtime.Call(childCtx, AgentClaimToolName, map[string]any{
		"agent_id": string(spawned.ID), "token": credential.Token(), "workspace_id": otherWorkspaceID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !extraWorkspace.IsError || !strings.Contains(extraWorkspace.Content[0].Text, "unsupported") {
		t.Fatalf("claim accepted caller workspace=%#v", extraWorkspace)
	}

	claimed, err := runtime.Call(childCtx, AgentClaimToolName, map[string]any{
		"agent_id": string(spawned.ID), "token": credential.Token(),
	})
	if err != nil || claimed.IsError {
		t.Fatalf("claim err=%v result=%#v", err, claimed)
	}
	value, ok := claimed.StructuredContent.(AgentClaimResult)
	if !ok || value.WorkspaceID != workspaceID || value.AgentID != spawned.ID || !value.Claimed {
		t.Fatalf("claim result=%#v", claimed.StructuredContent)
	}
	if _, ok := runtime.SessionAccess.Lookup("managed-child-session"); ok {
		t.Fatal("ordinary workspace grants remained after managed claim")
	}

	resolution, err := runtime.ResolveWorkspaceAccess(childCtx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.WorkspaceID != workspaceID || resolution.SessionAccess != SessionWorkspaceAccessClaimed || resolution.SessionWorkspaceCount != 1 {
		t.Fatalf("claimed resolution=%#v", resolution)
	}
	if _, err := runtime.ResolveWorkspaceAccess(childCtx, otherWorkspaceID); err == nil || !strings.Contains(err.Error(), "bound to workspace") {
		t.Fatalf("claimed child escaped workspace: %v", err)
	}

	parentCtx := WithMCPSessionID(context.Background(), "ordinary-parent-session")
	if _, err := runtime.ResolveWorkspaceAccess(parentCtx, workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ResolveWorkspaceAccess(parentCtx, otherWorkspaceID); err != nil {
		t.Fatal(err)
	}
	if access, ok := runtime.SessionAccess.Lookup("ordinary-parent-session"); !ok || len(access.Workspaces) != 2 {
		t.Fatalf("ordinary session behavior changed: %#v ok=%t", access, ok)
	}

	replayed, err := runtime.Call(WithMCPSessionID(context.Background(), "replay-session"), AgentClaimToolName, map[string]any{
		"agent_id": string(spawned.ID), "token": credential.Token(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.IsError || !strings.Contains(replayed.Content[0].Text, managedagent.ErrClaimRejected.Error()) {
		t.Fatalf("claim replay=%#v", replayed)
	}
}

func TestAgentClaimWrongTokenDoesNotCreateAuthority(t *testing.T) {
	runtime, workspaceID, otherWorkspaceID, spawned, credential := newClaimToolRuntime(t)
	ctx := WithMCPSessionID(context.Background(), "wrong-token-session")
	wrong := strings.Repeat("x", len(credential.Token()))
	result, err := runtime.Call(ctx, AgentClaimToolName, map[string]any{
		"agent_id": string(spawned.ID), "token": wrong,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("wrong token succeeded: %#v", result)
	}
	if _, ok := runtime.Agents.SessionBinding("wrong-token-session"); ok {
		t.Fatal("wrong token created managed child binding")
	}
	// An invalid claim must not silently create exact-workspace authority.
	if _, err := runtime.ResolveWorkspaceAccess(ctx, otherWorkspaceID); err != nil {
		t.Fatalf("invalid claim changed ordinary session behavior: %v", err)
	}
	if _, err := runtime.ResolveWorkspaceAccess(ctx, workspaceID); err != nil {
		t.Fatalf("ordinary session could not access another workspace: %v", err)
	}
}

func TestAgentClaimInactiveBindingRejectsWorkspaceAccess(t *testing.T) {
	runtime, workspaceID, _, spawned, credential := newClaimToolRuntime(t)
	ctx := WithMCPSessionID(context.Background(), "terminal-child-session")
	result, err := runtime.Call(ctx, AgentClaimToolName, map[string]any{
		"agent_id": string(spawned.ID), "token": credential.Token(),
	})
	if err != nil || result.IsError {
		t.Fatalf("claim err=%v result=%#v", err, result)
	}
	if _, err := runtime.Agents.Cancel(t.Context(), managedagent.OperatorController(), spawned.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ResolveWorkspaceAccess(ctx, workspaceID); err == nil || !strings.Contains(err.Error(), "no longer active") {
		t.Fatalf("inactive managed session retained workspace authority: %v", err)
	}
}

func TestAgentClaimObservabilityRedactsTokenAndDigest(t *testing.T) {
	runtime, _, _, spawned, credential := newClaimToolRuntime(t)
	var observations []CallObservation
	runtime.SetCallObserver(func(value CallObservation) {
		observations = append(observations, value)
	})
	ctx := WithMCPSessionID(context.Background(), "observed-child-session")
	result, err := runtime.Call(ctx, AgentClaimToolName, map[string]any{
		"agent_id": string(spawned.ID), "token": credential.Token(),
	})
	if err != nil || result.IsError {
		t.Fatalf("claim err=%v result=%#v", err, result)
	}
	data, err := json.Marshal(observations)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(credential.Token()))
	if strings.Contains(string(data), credential.Token()) || strings.Contains(string(data), hex.EncodeToString(digest[:])) {
		t.Fatalf("claim secret leaked into observations: %s", data)
	}
	if len(observations) == 0 {
		t.Fatal("claim produced no observation")
	}
	for _, observation := range observations {
		if observation.Tool != AgentClaimToolName {
			continue
		}
		raw, _ := json.Marshal(observation.Raw)
		if strings.Contains(string(raw), "\"token\"") {
			t.Fatalf("claim token field survived redaction: %s", raw)
		}
	}
}

func TestAgentClaimToolSchemaIsStrictAndWorkspaceFree(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := NewRuntime()
	schema, ok := runtime.Registry.Schema(AgentClaimToolName)
	if !ok {
		t.Fatal("agent_claim schema missing")
	}
	text := string(schema.InputSchema)
	for _, expected := range []string{`"agent_id"`, `"token"`, `"additionalProperties":false`, `"minLength":40`, `"maxLength":80`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("agent_claim schema missing %s: %s", expected, text)
		}
	}
	if strings.Contains(text, "workspace_id") {
		t.Fatalf("agent_claim accepts caller-supplied workspace: %s", text)
	}
}

func TestAgentClaimRejectsMalformedTokenBeforeConsumption(t *testing.T) {
	runtime, _, _, spawned, credential := newClaimToolRuntime(t)
	ctx := WithMCPSessionID(context.Background(), "malformed-token-session")
	for _, token := range []string{"short", strings.Repeat("x", 81)} {
		result, err := runtime.Call(ctx, AgentClaimToolName, map[string]any{
			"agent_id": string(spawned.ID), "token": token,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !result.IsError || !strings.Contains(result.Content[0].Text, "invalid length") {
			t.Fatalf("malformed token result=%#v", result)
		}
	}
	if _, err := runtime.Agents.ConsumeClaim(spawned.ID, credential.Token(), "direct-valid-session"); err != nil {
		t.Fatalf("malformed tool calls consumed valid claim: %v", err)
	}
}

func TestAgentClaimToolRequiresExistingManagedAgent(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := NewRuntime()
	ctx := WithMCPSessionID(context.Background(), "unknown-agent-session")
	result, err := runtime.Call(ctx, AgentClaimToolName, map[string]any{
		"agent_id": "agent_0123456789abcdef", "token": strings.Repeat("x", 43),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content[0].Text, managedagent.ErrClaimRejected.Error()) {
		t.Fatalf("unknown agent claim=%#v", result)
	}
}
