package tools_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	mcpconfigwire "go.mewis.me/codemcp/internal/mcpconfig/wire"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	"go.mewis.me/codemcp/internal/tools"
)

type realConfigSetProvider struct {
	inner   *application.MCPConfigReadService
	applies atomic.Int32
}

func (provider *realConfigSetProvider) BindSetApproval(ctx context.Context, arguments map[string]any) (mcpconfigwire.SetApprovalBinding, mcpconfigwire.ErrorCode) {
	return provider.inner.BindSetApproval(ctx, arguments)
}

func (provider *realConfigSetProvider) ApplySet(ctx context.Context, arguments map[string]any, binding mcpconfigwire.SetApprovalBinding) (mcpconfigwire.MutationResult, *mcpconfigwire.MutationError) {
	provider.applies.Add(1)
	return provider.inner.ApplySet(ctx, arguments, binding)
}

type realConfigSetHarness struct {
	runtime     *tools.Runtime
	provider    *realConfigSetProvider
	root        string
	workspaceID string
}

func newRealConfigSetHarness(t *testing.T) realConfigSetHarness {
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
	provider := &realConfigSetProvider{inner: application.NewMCPConfigReadService()}
	runtime.SetConfigSetApprovalProvider(provider)
	runtime.SetConfigSetApplyProvider(provider)
	workspace, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return realConfigSetHarness{runtime: runtime, provider: provider, root: root, workspaceID: workspace.ID}
}

func approveAndRetryConfigSet(t *testing.T, harness realConfigSetHarness, caller string, args map[string]any) tools.Result {
	t.Helper()
	first, err := harness.runtime.Call(configApprovalContext(caller, caller+"-initial"), mcpconfigwire.SetToolName, args)
	if err != nil || !first.IsError {
		t.Fatalf("initial result=%#v err=%v", first, err)
	}
	if harness.provider.applies.Load() != 0 {
		t.Fatalf("config_set applied before approval: %d", harness.provider.applies.Load())
	}
	request, created, err := harness.runtime.Approvals.CreateRequestWithTitle(challengeID(t, first), caller, harness.workspaceID, "Update CodeMCP settings")
	if err != nil || !created {
		t.Fatalf("approval request=%#v created=%t err=%v", request, created, err)
	}
	if _, err := harness.runtime.Approvals.Approve(request.ID, "reviewer", "reviewed"); err != nil {
		t.Fatal(err)
	}
	retry, err := harness.runtime.Call(configApprovalContext(caller, caller+"-retry"), mcpconfigwire.SetToolName, args)
	if err != nil {
		t.Fatal(err)
	}
	return retry
}

func writeConfigSetRuntimeState(t *testing.T, root string, state runtimecontrol.State) {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, runtimecontrol.FileName), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestConfigSetApprovedStaticBatchAppliesOnceAndReloadsOnce(t *testing.T) {
	harness := newRealConfigSetHarness(t)
	var reloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/reload" || r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("reload request=%s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		reloads.Add(1)
		_ = json.NewEncoder(w).Encode(runtimecontrol.ReloadResult{PID: os.Getpid(), ServerEnabled: true, ServerPort: 40123, AdminEnabled: true, AdminPort: 40124})
	}))
	defer server.Close()
	writeConfigSetRuntimeState(t, harness.root, runtimecontrol.State{
		PID: os.Getpid(), Address: strings.TrimPrefix(server.URL, "http://"), Token: "token", ConfigRoot: harness.root,
	})

	args := configSetArgs(harness.workspaceID,
		mcpconfigwire.Change{Key: "server.port", Value: "40123"},
		mcpconfigwire.Change{Key: "admin.port", Value: "40124"},
	)
	before, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	first, err := harness.runtime.Call(configApprovalContext("static-batch", "static-initial"), mcpconfigwire.SetToolName, args)
	if err != nil || !first.IsError {
		t.Fatalf("pre-approval=%#v err=%v", first, err)
	}
	unchanged, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Server.Port != before.Server.Port || unchanged.Admin.Port != before.Admin.Port || reloads.Load() != 0 || harness.provider.applies.Load() != 0 {
		t.Fatalf("mutation happened before approval: cfg=%#v reloads=%d applies=%d", unchanged, reloads.Load(), harness.provider.applies.Load())
	}
	request, created, err := harness.runtime.Approvals.CreateRequestWithTitle(challengeID(t, first), "static-batch", harness.workspaceID, "Update ports")
	if err != nil || !created {
		t.Fatalf("request=%#v created=%t err=%v", request, created, err)
	}
	if _, err := harness.runtime.Approvals.Approve(request.ID, "reviewer", ""); err != nil {
		t.Fatal(err)
	}
	retry, err := harness.runtime.Call(configApprovalContext("static-batch", "static-retry"), mcpconfigwire.SetToolName, args)
	if err != nil || retry.IsError {
		t.Fatalf("approved retry=%#v err=%v", retry, err)
	}
	result, ok := retry.StructuredContent.(mcpconfigwire.MutationResult)
	if !ok {
		t.Fatalf("structured result=%T %#v", retry.StructuredContent, retry.StructuredContent)
	}
	if harness.provider.applies.Load() != 1 || reloads.Load() != 1 || result.State != mcpconfigwire.MutationRuntimeSynced ||
		result.RuntimeSync != mcpconfigwire.RuntimeSyncCurrent || !result.RuntimeReloaded || !result.Changed || result.ChangeCount != 2 {
		t.Fatalf("result=%#v applies=%d reloads=%d", result, harness.provider.applies.Load(), reloads.Load())
	}
	if !reflect.DeepEqual(result.Keys, []string{"server.port", "admin.port"}) ||
		len(result.Outcomes) != 2 || !result.Outcomes[0].Changed || !result.Outcomes[1].Changed {
		t.Fatalf("safe outcomes=%#v", result)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Server.Port != 40123 || loaded.Admin.Port != 40124 {
		t.Fatalf("approved values not persisted: %#v", loaded)
	}
	data, _ := json.Marshal(retry)
	if strings.Contains(string(data), "40123") || strings.Contains(string(data), "40124") {
		t.Fatalf("config_set result leaked values: %s", data)
	}
}

func TestConfigSetNoOpStillRequiresApprovalAndDoesNotReload(t *testing.T) {
	harness := newRealConfigSetHarness(t)
	var reloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reloads.Add(1)
		_ = json.NewEncoder(w).Encode(runtimecontrol.ReloadResult{PID: os.Getpid()})
	}))
	defer server.Close()
	writeConfigSetRuntimeState(t, harness.root, runtimecontrol.State{
		PID: os.Getpid(), Address: strings.TrimPrefix(server.URL, "http://"), Token: "token", ConfigRoot: harness.root,
	})
	args := configSetArgs(harness.workspaceID, mcpconfigwire.Change{Key: "notifications.approval.enabled", Value: "false"})
	result := approveAndRetryConfigSet(t, harness, "noop", args)
	if result.IsError {
		t.Fatalf("no-op result=%#v", result)
	}
	mutation, ok := result.StructuredContent.(mcpconfigwire.MutationResult)
	if !ok || mutation.State != mcpconfigwire.MutationUnchanged || mutation.RuntimeSync != mcpconfigwire.RuntimeSyncCurrent ||
		mutation.Changed || mutation.RuntimeReloaded || reloads.Load() != 0 || harness.provider.applies.Load() != 1 {
		t.Fatalf("no-op mutation=%#v applies=%d reloads=%d", mutation, harness.provider.applies.Load(), reloads.Load())
	}
	if len(mutation.Outcomes) != 1 || mutation.Outcomes[0].Changed {
		t.Fatalf("no-op outcome=%#v", mutation.Outcomes)
	}
}

func TestConfigSetStoppedRuntimePersistsWithoutReload(t *testing.T) {
	harness := newRealConfigSetHarness(t)
	args := configSetArgs(harness.workspaceID,
		mcpconfigwire.Change{Key: "server.port", Value: "40123"},
		mcpconfigwire.Change{Key: "admin.port", Value: "40124"},
	)
	result := approveAndRetryConfigSet(t, harness, "stopped", args)
	if result.IsError {
		t.Fatalf("stopped result=%#v", result)
	}
	mutation, ok := result.StructuredContent.(mcpconfigwire.MutationResult)
	if !ok || mutation.State != mcpconfigwire.MutationPersisted || mutation.RuntimeSync != mcpconfigwire.RuntimeSyncPersisted ||
		!mutation.Changed || mutation.RuntimeReloaded || harness.provider.applies.Load() != 1 {
		t.Fatalf("stopped mutation=%#v applies=%d", mutation, harness.provider.applies.Load())
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Server.Port != 40123 || loaded.Admin.Port != 40124 {
		t.Fatalf("stopped mutation not persisted: %#v", loaded)
	}
}

func TestConfigSetInvalidBatchIsRejectedBeforeApproval(t *testing.T) {
	harness := newRealConfigSetHarness(t)
	before, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range []map[string]any{
		configSetArgs(harness.workspaceID, mcpconfigwire.Change{Key: "server.port", Value: "not-a-port"}),
		configSetArgs(harness.workspaceID,
			mcpconfigwire.Change{Key: "server.port", Value: "40124"},
			mcpconfigwire.Change{Key: "admin.port", Value: "40124"},
		),
	} {
		result, err := harness.runtime.Call(configApprovalContext("invalid", "invalid-request"), mcpconfigwire.SetToolName, args)
		if err != nil || !result.IsError || harness.provider.applies.Load() != 0 {
			t.Fatalf("invalid result=%#v err=%v applies=%d", result, err, harness.provider.applies.Load())
		}
		data, _ := json.Marshal(result)
		if !strings.Contains(string(data), string(mcpconfigwire.ErrorInvalidRequest)) {
			t.Fatalf("invalid batch code missing: %s", data)
		}
		if requests := harness.runtime.Approvals.List(approval.Filter{}); len(requests) != 0 {
			t.Fatalf("invalid batch created approval state: %#v", requests)
		}
	}
	after, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if after.Server.Port != before.Server.Port || after.Admin.Port != before.Admin.Port {
		t.Fatalf("invalid batch changed config: before=%#v after=%#v", before, after)
	}
}

func TestConfigSetClaimedApprovalSurvivesDisablingHTTPListener(t *testing.T) {
	harness := newRealConfigSetHarness(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Tunnel.Enabled = true
	cfg.Tunnel.ID = "tunnel_test"
	cfg.Tunnel.APIKey = "runtime-secret"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	var reloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reloads.Add(1)
		_ = json.NewEncoder(w).Encode(runtimecontrol.ReloadResult{PID: os.Getpid(), ServerEnabled: false, AdminEnabled: true, AdminPort: 37422})
	}))
	defer server.Close()
	writeConfigSetRuntimeState(t, harness.root, runtimecontrol.State{
		PID: os.Getpid(), Address: strings.TrimPrefix(server.URL, "http://"), Token: "token", ConfigRoot: harness.root,
	})
	args := configSetArgs(harness.workspaceID, mcpconfigwire.Change{Key: "server.enabled", Value: "false"})
	result := approveAndRetryConfigSet(t, harness, "disable-listener", args)
	if result.IsError {
		t.Fatalf("disable listener result=%#v", result)
	}
	mutation, ok := result.StructuredContent.(mcpconfigwire.MutationResult)
	if !ok || mutation.State != mcpconfigwire.MutationRuntimeSynced || !mutation.RuntimeReloaded ||
		harness.provider.applies.Load() != 1 || reloads.Load() != 1 {
		t.Fatalf("disable listener mutation=%#v applies=%d reloads=%d", mutation, harness.provider.applies.Load(), reloads.Load())
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Server.Enabled {
		t.Fatalf("server listener remained enabled: %#v", loaded.Server)
	}
}

func TestConfigSetReloadFailureRollsBackWithSafeError(t *testing.T) {
	harness := newRealConfigSetHarness(t)
	before, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	var reloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reloads.Add(1)
		http.Error(w, "reload failed", http.StatusInternalServerError)
	}))
	defer server.Close()
	writeConfigSetRuntimeState(t, harness.root, runtimecontrol.State{
		PID: os.Getpid(), Address: strings.TrimPrefix(server.URL, "http://"), Token: "token", ConfigRoot: harness.root,
	})
	args := configSetArgs(harness.workspaceID,
		mcpconfigwire.Change{Key: "server.port", Value: "40123"},
		mcpconfigwire.Change{Key: "admin.port", Value: "40124"},
	)
	result := approveAndRetryConfigSet(t, harness, "rollback", args)
	if !result.IsError {
		t.Fatalf("reload failure unexpectedly succeeded: %#v", result)
	}
	mutation, ok := result.StructuredContent.(mcpconfigwire.MutationError)
	if !ok || mutation.Code != mcpconfigwire.ErrorApplyFailed || mutation.State != mcpconfigwire.MutationRolledBack ||
		mutation.RuntimeSync != mcpconfigwire.RuntimeSyncCurrent || mutation.RuntimeReloaded ||
		harness.provider.applies.Load() != 1 || reloads.Load() != 1 {
		t.Fatalf("rollback mutation=%#v applies=%d reloads=%d", mutation, harness.provider.applies.Load(), reloads.Load())
	}
	after, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if after.Server.Port != before.Server.Port || after.Admin.Port != before.Admin.Port {
		t.Fatalf("rollback did not restore config: before=%#v after=%#v", before, after)
	}
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), "40123") || strings.Contains(string(data), "40124") || strings.Contains(string(data), "reload failed") {
		t.Fatalf("reload failure leaked values/details: %s", data)
	}
}
