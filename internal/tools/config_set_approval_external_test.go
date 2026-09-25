package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	mcpconfigwire "go.mewis.me/codemcp/internal/mcpconfig/wire"
	"go.mewis.me/codemcp/internal/tools"
)

type configApprovalHarness struct {
	runtime     *tools.Runtime
	workspaceID string
	root        string
	applied     atomic.Int32
}

type configApprovalApplyFixture struct {
	applied *atomic.Int32
}

func (fixture configApprovalApplyFixture) ApplySet(_ context.Context, _ map[string]any, binding mcpconfigwire.SetApprovalBinding) (mcpconfigwire.MutationResult, *mcpconfigwire.MutationError) {
	fixture.applied.Add(1)
	keys := make([]string, 0, len(binding.Changes))
	outcomes := make([]mcpconfigwire.MutationOutcome, 0, len(binding.Changes))
	for _, change := range binding.Changes {
		keys = append(keys, change.Key)
		outcomes = append(outcomes, mcpconfigwire.MutationOutcome{Key: change.Key, Changed: true})
	}
	return mcpconfigwire.MutationResult{
		State: mcpconfigwire.MutationRuntimeSynced, Keys: keys, Outcomes: outcomes,
		ChangeCount: len(keys), Changed: true, RuntimeReloaded: true, RuntimeSync: mcpconfigwire.RuntimeSyncCurrent,
	}, nil
}

func newConfigApprovalHarness(t *testing.T) *configApprovalHarness {
	t.Helper()
	root := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, root)
	previous := configformat.RootPath()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	cfg := config.Default()
	cfg.Auth.MCPTokenHash = "mcp-configured-hash"
	cfg.Auth.AdminTokenHash = "admin-configured-hash"
	cfg.Permissions.MCPConfigWrite = true
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	runtime := tools.NewRuntime()
	provider := application.NewMCPConfigReadService()
	runtime.SetConfigSetApprovalProvider(provider)
	workspace, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	harness := &configApprovalHarness{runtime: runtime, workspaceID: workspace.ID, root: root}
	runtime.SetConfigSetApplyProvider(configApprovalApplyFixture{applied: &harness.applied})
	return harness
}

func configApprovalContext(caller, request string) context.Context {
	ctx := tools.WithCallSource(context.Background(), "tunnel")
	return tools.WithApprovalCorrelation(ctx, caller, request)
}

func configSetArgs(workspaceID string, changes ...mcpconfigwire.Change) map[string]any {
	values := make([]any, len(changes))
	for index, change := range changes {
		values[index] = map[string]any{"key": change.Key, "value": change.Value}
	}
	return map[string]any{"workspace_id": workspaceID, "changes": values}
}

func structuredMap(t *testing.T, result tools.Result) map[string]any {
	t.Helper()
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode structured content %s: %v", data, err)
	}
	return decoded
}

func challengeID(t *testing.T, result tools.Result) string {
	t.Helper()
	body := structuredMap(t, result)
	id, _ := body["challenge_id"].(string)
	if body["code"] != "approval_required" || id == "" {
		t.Fatalf("approval challenge=%#v", body)
	}
	return id
}

func TestConfigSetApprovalExactRetryIsPrivateAndOneShot(t *testing.T) {
	harness := newConfigApprovalHarness(t)
	firstValue := "token-like-private-value"
	secondValue := t.TempDir()
	args := configSetArgs(harness.workspaceID,
		mcpconfigwire.Change{Key: "tunnel.organization_id", Value: firstValue},
		mcpconfigwire.Change{Key: "permissions.allow_dirs", Value: secondValue},
	)
	observed := make([]tools.CallObservation, 0, 4)
	harness.runtime.SetCallObserver(func(value tools.CallObservation) {
		observed = append(observed, value)
	})
	first, err := harness.runtime.Call(configApprovalContext("caller-a", "transport-request-a"), mcpconfigwire.SetToolName, args)
	if err != nil || !first.IsError || harness.applied.Load() != 0 {
		t.Fatalf("first=%#v err=%v applied=%d", first, err, harness.applied.Load())
	}
	id := challengeID(t, first)
	publicChallenge, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	assertConfigApprovalPublicValueFree(t, string(publicChallenge), harness.root, firstValue, secondValue)

	request, created, err := harness.runtime.Approvals.CreateRequestWithTitle(id, "caller-a", harness.workspaceID, "Update CodeMCP settings")
	if err != nil || !created {
		t.Fatalf("request=%#v created=%t err=%v", request, created, err)
	}
	privateJSON := string(request.Arguments)
	for _, required := range []string{firstValue, secondValue, harness.root, "__codemcp_config_binding", "config_fingerprint"} {
		if !strings.Contains(privateJSON, required) {
			t.Fatalf("private approval binding lost %q: %s", required, privateJSON)
		}
	}
	publicRequest, err := json.Marshal(approval.PublicRequest(request))
	if err != nil {
		t.Fatal(err)
	}
	assertConfigApprovalPublicValueFree(t, string(publicRequest), harness.root, firstValue, secondValue)

	if _, err := harness.runtime.Approvals.Approve(request.ID, "reviewer", "reviewed"); err != nil {
		t.Fatal(err)
	}
	retry, err := harness.runtime.Call(configApprovalContext("caller-a", "transport-request-b"), mcpconfigwire.SetToolName, args)
	if err != nil || retry.IsError || harness.applied.Load() != 1 {
		t.Fatalf("retry=%#v err=%v applied=%d", retry, err, harness.applied.Load())
	}
	consumed, ok := harness.runtime.Approvals.Get(request.ID)
	if !ok || consumed.Status != approval.StatusConsumed || consumed.ConsumedAt.IsZero() {
		t.Fatalf("consumed=%#v ok=%t", consumed, ok)
	}

	duplicate, err := harness.runtime.Call(configApprovalContext("caller-a", "transport-request-c"), mcpconfigwire.SetToolName, args)
	if err != nil || !duplicate.IsError || harness.applied.Load() != 1 {
		t.Fatalf("duplicate=%#v err=%v applied=%d", duplicate, err, harness.applied.Load())
	}
	if next := challengeID(t, duplicate); next == id {
		t.Fatalf("consumed approval reused challenge %q", id)
	}
	observedJSON, err := json.Marshal(observed)
	if err != nil {
		t.Fatal(err)
	}
	assertConfigApprovalPublicValueFree(t, string(observedJSON), harness.root, firstValue, secondValue)
}

func TestConfigSetApprovalRejectsChangedOrderAndStaleConfig(t *testing.T) {
	t.Run("changed value and order", func(t *testing.T) {
		harness := newConfigApprovalHarness(t)
		base := configSetArgs(harness.workspaceID,
			mcpconfigwire.Change{Key: "server.port", Value: "41001"},
			mcpconfigwire.Change{Key: "server.enabled", Value: "true"},
		)
		first, _ := harness.runtime.Call(configApprovalContext("caller-a", "request-a"), mcpconfigwire.SetToolName, base)
		request, _, err := harness.runtime.Approvals.CreateRequestWithTitle(challengeID(t, first), "caller-a", harness.workspaceID, "Update settings")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := harness.runtime.Approvals.Approve(request.ID, "reviewer", ""); err != nil {
			t.Fatal(err)
		}
		for _, args := range []map[string]any{
			configSetArgs(harness.workspaceID,
				mcpconfigwire.Change{Key: "server.port", Value: "41002"},
				mcpconfigwire.Change{Key: "server.enabled", Value: "true"},
			),
			configSetArgs(harness.workspaceID,
				mcpconfigwire.Change{Key: "server.enabled", Value: "true"},
				mcpconfigwire.Change{Key: "server.port", Value: "41001"},
			),
		} {
			result, err := harness.runtime.Call(configApprovalContext("caller-a", "request-mismatch"), mcpconfigwire.SetToolName, args)
			if err != nil || !result.IsError || harness.applied.Load() != 0 {
				t.Fatalf("mismatch=%#v err=%v applied=%d", result, err, harness.applied.Load())
			}
			body := structuredMap(t, result)
			if body["code"] != "approval_mismatch" {
				t.Fatalf("mismatch body=%#v", body)
			}
			data, _ := json.Marshal(result)
			for _, forbidden := range []string{"41001", "41002", harness.root, "__codemcp_config_binding", "config_fingerprint"} {
				if strings.Contains(string(data), forbidden) {
					t.Fatalf("mismatch leaked %q: %s", forbidden, data)
				}
			}
		}
		current, _ := harness.runtime.Approvals.Get(request.ID)
		if current.Status != approval.StatusApproved {
			t.Fatalf("mismatch consumed request: %#v", current)
		}
	})

	t.Run("stale fingerprint", func(t *testing.T) {
		harness := newConfigApprovalHarness(t)
		args := configSetArgs(harness.workspaceID, mcpconfigwire.Change{Key: "server.port", Value: "41001"})
		first, _ := harness.runtime.Call(configApprovalContext("caller-a", "request-a"), mcpconfigwire.SetToolName, args)
		request, _, err := harness.runtime.Approvals.CreateRequestWithTitle(challengeID(t, first), "caller-a", harness.workspaceID, "Update port")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := harness.runtime.Approvals.Approve(request.ID, "reviewer", ""); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		cfg.Server.Port++
		if err := config.Save(cfg); err != nil {
			t.Fatal(err)
		}
		result, err := harness.runtime.Call(configApprovalContext("caller-a", "request-b"), mcpconfigwire.SetToolName, args)
		if err != nil || !result.IsError || structuredMap(t, result)["code"] != "approval_mismatch" || harness.applied.Load() != 0 {
			t.Fatalf("stale retry=%#v err=%v applied=%d", result, err, harness.applied.Load())
		}
	})

	t.Run("stale config root", func(t *testing.T) {
		harness := newConfigApprovalHarness(t)
		args := configSetArgs(harness.workspaceID, mcpconfigwire.Change{Key: "server.port", Value: "41001"})
		first, _ := harness.runtime.Call(configApprovalContext("caller-a", "request-a"), mcpconfigwire.SetToolName, args)
		request, _, err := harness.runtime.Approvals.CreateRequestWithTitle(challengeID(t, first), "caller-a", harness.workspaceID, "Update port")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := harness.runtime.Approvals.Approve(request.ID, "reviewer", ""); err != nil {
			t.Fatal(err)
		}
		otherRoot := t.TempDir()
		if err := configformat.SetRootPath(otherRoot); err != nil {
			t.Fatal(err)
		}
		cfg := config.Default()
		cfg.Auth.MCPTokenHash = "mcp-configured-hash"
		cfg.Auth.AdminTokenHash = "admin-configured-hash"
		cfg.Permissions.MCPConfigWrite = true
		if err := config.Save(cfg); err != nil {
			t.Fatal(err)
		}
		result, err := harness.runtime.Call(configApprovalContext("caller-a", "request-b"), mcpconfigwire.SetToolName, args)
		if err != nil || !result.IsError || structuredMap(t, result)["code"] != "approval_mismatch" || harness.applied.Load() != 0 {
			t.Fatalf("root retry=%#v err=%v applied=%d", result, err, harness.applied.Load())
		}
		data, _ := json.Marshal(result)
		if strings.Contains(string(data), harness.root) || strings.Contains(string(data), otherRoot) {
			t.Fatalf("root mismatch leaked config root: %s", data)
		}
	})
}

func TestConfigSetApprovalRejectsSecretsMissingScopeDenialAndUnavailableReviewer(t *testing.T) {
	t.Run("no-op and host confirmation still challenge", func(t *testing.T) {
		harness := newConfigApprovalHarness(t)
		args := configSetArgs(harness.workspaceID, mcpconfigwire.Change{Key: "notifications.approval.enabled", Value: "false"})
		ctx := configApprovalContext("caller-a", "request-a")
		ctx = tools.WithInputRound(ctx, "host-confirmed", map[string]any{"confirm": map[string]any{"accepted": true}})
		result, err := harness.runtime.Call(ctx, mcpconfigwire.SetToolName, args)
		if err != nil || !result.IsError || harness.applied.Load() != 0 {
			t.Fatalf("host-confirmed no-op=%#v err=%v applied=%d", result, err, harness.applied.Load())
		}
		if structuredMap(t, result)["code"] != "approval_required" {
			t.Fatalf("host confirmation bypassed CodeMCP approval: %#v", result.StructuredContent)
		}
	})

	t.Run("secret before approval", func(t *testing.T) {
		harness := newConfigApprovalHarness(t)
		secret := "must-never-enter-approval"
		result, err := harness.runtime.Call(configApprovalContext("caller-a", "request-a"), mcpconfigwire.SetToolName,
			configSetArgs(harness.workspaceID, mcpconfigwire.Change{Key: "tunnel.api_key", Value: secret}))
		if err != nil || !result.IsError || harness.applied.Load() != 0 {
			t.Fatalf("secret result=%#v err=%v applied=%d", result, err, harness.applied.Load())
		}
		data, _ := json.Marshal(result)
		if strings.Contains(string(data), secret) || !strings.Contains(string(data), string(mcpconfigwire.ErrorSecretWriteForbidden)) {
			t.Fatalf("secret rejection=%s", data)
		}
		if requests := harness.runtime.Approvals.List(approval.Filter{}); len(requests) != 0 {
			t.Fatalf("secret input created approval state: %#v", requests)
		}
	})

	t.Run("missing caller", func(t *testing.T) {
		harness := newConfigApprovalHarness(t)
		args := configSetArgs(harness.workspaceID, mcpconfigwire.Change{Key: "server.port", Value: "41001"})
		result, err := harness.runtime.Call(tools.WithCallSource(context.Background(), "tunnel"), mcpconfigwire.SetToolName, args)
		if err != nil || !result.IsError || harness.applied.Load() != 0 {
			t.Fatalf("missing caller=%#v err=%v applied=%d", result, err, harness.applied.Load())
		}
		if requests := harness.runtime.Approvals.List(approval.Filter{}); len(requests) != 0 {
			t.Fatalf("missing caller created approval state: %#v", requests)
		}
	})

	t.Run("missing request correlation", func(t *testing.T) {
		harness := newConfigApprovalHarness(t)
		args := configSetArgs(harness.workspaceID, mcpconfigwire.Change{Key: "server.port", Value: "41001"})
		ctx := tools.WithCallSource(context.Background(), "tunnel")
		ctx = tools.WithApprovalCorrelation(ctx, "caller-a", "")
		result, err := harness.runtime.Call(ctx, mcpconfigwire.SetToolName, args)
		if err != nil || !result.IsError || harness.applied.Load() != 0 {
			t.Fatalf("missing request correlation=%#v err=%v applied=%d", result, err, harness.applied.Load())
		}
		if requests := harness.runtime.Approvals.List(approval.Filter{}); len(requests) != 0 {
			t.Fatalf("missing request correlation created approval state: %#v", requests)
		}
	})

	t.Run("deny", func(t *testing.T) {
		harness := newConfigApprovalHarness(t)
		args := configSetArgs(harness.workspaceID, mcpconfigwire.Change{Key: "server.port", Value: "41001"})
		first, _ := harness.runtime.Call(configApprovalContext("caller-a", "request-a"), mcpconfigwire.SetToolName, args)
		request, _, err := harness.runtime.Approvals.CreateRequestWithTitle(challengeID(t, first), "caller-a", harness.workspaceID, "Update port")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := harness.runtime.Approvals.Deny(request.ID, "reviewer", "not now"); err != nil {
			t.Fatal(err)
		}
		retry, err := harness.runtime.Call(configApprovalContext("caller-a", "request-b"), mcpconfigwire.SetToolName, args)
		if err != nil || !retry.IsError || harness.applied.Load() != 0 {
			t.Fatalf("denied retry=%#v err=%v applied=%d", retry, err, harness.applied.Load())
		}
	})

	t.Run("pending reviewer and disconnected waiter", func(t *testing.T) {
		harness := newConfigApprovalHarness(t)
		args := configSetArgs(harness.workspaceID, mcpconfigwire.Change{Key: "server.port", Value: "41001"})
		first, _ := harness.runtime.Call(configApprovalContext("caller-a", "request-a"), mcpconfigwire.SetToolName, args)
		request, _, err := harness.runtime.Approvals.CreateRequestWithTitle(challengeID(t, first), "caller-a", harness.workspaceID, "Update port")
		if err != nil {
			t.Fatal(err)
		}
		waitCtx, cancel := context.WithCancel(context.Background())
		cancel()
		waiting, err := harness.runtime.Approvals.Wait(waitCtx, request.ID)
		if err == nil || waiting.Status != approval.StatusPending {
			t.Fatalf("disconnected wait=%#v err=%v", waiting, err)
		}
		retry, err := harness.runtime.Call(configApprovalContext("caller-a", "request-b"), mcpconfigwire.SetToolName, args)
		if err != nil || !retry.IsError || harness.applied.Load() != 0 {
			t.Fatalf("pending retry=%#v err=%v applied=%d", retry, err, harness.applied.Load())
		}
		current, ok := harness.runtime.Approvals.Get(request.ID)
		if !ok || current.Status != approval.StatusPending {
			t.Fatalf("pending request changed after disconnect/retry: %#v ok=%t", current, ok)
		}
	})

	t.Run("parallel review does not dispatch", func(t *testing.T) {
		harness := newConfigApprovalHarness(t)
		args := configSetArgs(harness.workspaceID, mcpconfigwire.Change{Key: "server.port", Value: "41001"})
		first, _ := harness.runtime.Call(configApprovalContext("caller-a", "request-a"), mcpconfigwire.SetToolName, args)
		request, _, err := harness.runtime.Approvals.CreateRequestWithTitle(challengeID(t, first), "caller-a", harness.workspaceID, "Update port")
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		results := make(chan error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := harness.runtime.Approvals.Approve(request.ID, "reviewer-a", "")
			results <- err
		}()
		go func() {
			defer wg.Done()
			_, err := harness.runtime.Approvals.Deny(request.ID, "reviewer-b", "")
			results <- err
		}()
		wg.Wait()
		close(results)
		successes := 0
		for err := range results {
			if err == nil {
				successes++
			}
		}
		if successes != 1 || harness.applied.Load() != 0 {
			t.Fatalf("parallel reviews successes=%d applied=%d", successes, harness.applied.Load())
		}
	})

	t.Run("reviewer unavailable", func(t *testing.T) {
		harness := newConfigApprovalHarness(t)
		harness.runtime.Approvals = nil
		result, err := harness.runtime.Call(configApprovalContext("caller-a", "request-a"), mcpconfigwire.SetToolName,
			configSetArgs(harness.workspaceID, mcpconfigwire.Change{Key: "server.port", Value: "41001"}))
		if err != nil || !result.IsError || harness.applied.Load() != 0 {
			t.Fatalf("unavailable reviewer=%#v err=%v applied=%d", result, err, harness.applied.Load())
		}
	})
}

func TestConfigSetMalformedNestedValueNeverLeaksThroughActivityOrRequestEnvelope(t *testing.T) {
	harness := newConfigApprovalHarness(t)
	secret := "nested-credential-like-private-marker"
	args := map[string]any{
		"workspace_id": harness.workspaceID,
		"changes": []any{
			map[string]any{
				"key": "server.port",
				"value": map[string]any{
					"authorization": "Bearer " + secret,
					"nested":        []any{map[string]any{"password": secret}},
				},
			},
		},
	}
	params := map[string]any{
		"name":      mcpconfigwire.SetToolName,
		"arguments": args,
		"_meta":     map[string]any{"safe": "metadata"},
	}
	request := map[string]any{"jsonrpc": "2.0", "id": "nested-call", "method": "tools/call", "params": params}
	ctx := configApprovalContext("nested-caller", "nested-request")
	ctx = tools.WithCallDetails(ctx, "tools/call", params)
	ctx = tools.WithCallRequest(ctx, request)
	observed := make([]tools.CallObservation, 0, 2)
	harness.runtime.SetCallObserver(func(value tools.CallObservation) { observed = append(observed, value) })

	result, err := harness.runtime.Call(ctx, mcpconfigwire.SetToolName, args)
	if err != nil || !result.IsError || harness.applied.Load() != 0 {
		t.Fatalf("nested malformed result=%#v err=%v applied=%d", result, err, harness.applied.Load())
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(resultJSON), string(mcpconfigwire.ErrorInvalidRequest)) || strings.Contains(string(resultJSON), secret) {
		t.Fatalf("nested malformed result leaked or lost safe code: %s", resultJSON)
	}
	if requests := harness.runtime.Approvals.List(approval.Filter{}); len(requests) != 0 {
		t.Fatalf("malformed nested value created approval state: %#v", requests)
	}
	observedJSON, err := json.Marshal(observed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(observedJSON), secret) || strings.Contains(string(observedJSON), "Bearer "+secret) {
		t.Fatalf("nested malformed value leaked through activity/request envelope: %s", observedJSON)
	}
	if !strings.Contains(string(observedJSON), "server.port") || !strings.Contains(string(observedJSON), "change_count") {
		t.Fatalf("activity lost safe config summary: %s", observedJSON)
	}
}

func assertConfigApprovalPublicValueFree(t *testing.T, text, root string, values ...string) {
	t.Helper()
	for _, forbidden := range append(values, root, "__codemcp_config_binding", "config_fingerprint") {
		if forbidden != "" && strings.Contains(text, forbidden) {
			t.Fatalf("public config approval surface leaked %q: %s", forbidden, text)
		}
	}
	for _, required := range []string{"tunnel.organization_id", "permissions.allow_dirs", "change_count"} {
		if !strings.Contains(text, required) {
			t.Fatalf("public config approval surface lost summary %q: %s", required, text)
		}
	}
}
