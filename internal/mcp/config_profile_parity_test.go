package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	mcpconfigwire "go.mewis.me/codemcp/internal/mcpconfig/wire"
	"go.mewis.me/codemcp/internal/tools"
)

type openAIConfigProjectionProbe struct{}

type profileConfigSetProvider struct {
	applies atomic.Int32
}

func (provider *profileConfigSetProvider) BindSetApproval(_ context.Context, arguments map[string]any) (mcpconfigwire.SetApprovalBinding, mcpconfigwire.ErrorCode) {
	changes, _, err := mcpconfigwire.CanonicalSetArguments(arguments)
	if err != nil {
		return mcpconfigwire.SetApprovalBinding{}, mcpconfigwire.ErrorInvalidRequest
	}
	return mcpconfigwire.SetApprovalBinding{Changes: changes, ConfigRoot: "/profile-test", ConfigFingerprint: "profile-fingerprint"}, ""
}

func (provider *profileConfigSetProvider) ApplySet(context.Context, map[string]any, mcpconfigwire.SetApprovalBinding) (mcpconfigwire.MutationResult, *mcpconfigwire.MutationError) {
	provider.applies.Add(1)
	return mcpconfigwire.MutationResult{
		State: mcpconfigwire.MutationRuntimeSynced,
		Keys:  []string{"server.port", "admin.port"},
		Outcomes: []mcpconfigwire.MutationOutcome{
			{Key: "server.port", Changed: true},
			{Key: "admin.port", Changed: true},
		},
		ChangeCount: 2, Changed: true, RuntimeReloaded: true, RuntimeSync: mcpconfigwire.RuntimeSyncCurrent,
	}, nil
}

func (openAIConfigProjectionProbe) ID() ProfileID { return "openai-config-probe" }

func (openAIConfigProjectionProbe) ToolRepresentation(tool ToolDescriptor) ToolRepresentation {
	return ToolRepresentation{
		Title:       "OpenAI " + tool.Title,
		Description: "OpenAI-compatible presentation for " + tool.Name,
		Meta:        map[string]any{"profile": "openai"},
	}
}

func TestConfigToolsKeepCanonicalSchemasEffectsAndSecurityAcrossProfiles(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := tools.NewRuntime()
	for _, name := range []string{mcpconfigwire.ListToolName, mcpconfigwire.GetToolName, mcpconfigwire.SetToolName} {
		schema, ok := runtime.Registry.Schema(name)
		if !ok {
			t.Fatalf("missing config tool %q", name)
		}
		descriptor := DescribeTool(schema)
		if descriptor.Security.ApprovalAuthority != runtimeApprovalAuthority {
			t.Fatalf("%s approval authority=%q", name, descriptor.Security.ApprovalAuthority)
		}
		for _, boundWorkspace := range []bool{false, true} {
			options := ToolProjectionOptions{BoundWorkspace: boundWorkspace}
			base, err := ProjectTool(BaseProfile(), descriptor, options)
			if err != nil {
				t.Fatal(err)
			}
			openai, err := ProjectTool(openAIConfigProjectionProbe{}, descriptor, options)
			if err != nil {
				t.Fatal(err)
			}
			if base.Name != openai.Name ||
				!jsonSemanticEqual(base.InputSchema, openai.InputSchema) ||
				!jsonSemanticEqual(base.OutputSchema, openai.OutputSchema) ||
				!reflect.DeepEqual(base.Annotations, openai.Annotations) {
				t.Fatalf("%s profile changed canonical contract bound=%t\nbase=%#v\nopenai=%#v", name, boundWorkspace, base, openai)
			}
			if reflect.DeepEqual(base.Meta, openai.Meta) || base.Title == openai.Title {
				t.Fatalf("%s probe did not exercise presentation-only profile differences", name)
			}
		}
	}
}

func TestConfigToolEffectsRemainTruthfulAcrossProfiles(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := tools.NewRuntime()
	want := map[string]ToolEffects{
		mcpconfigwire.ListToolName: {ReadOnly: true, Idempotent: true},
		mcpconfigwire.GetToolName:  {ReadOnly: true, Idempotent: true},
		mcpconfigwire.SetToolName:  {ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: false},
	}
	for name, expected := range want {
		schema, ok := runtime.Registry.Schema(name)
		if !ok {
			t.Fatalf("missing config tool %q", name)
		}
		descriptor := DescribeTool(schema)
		if descriptor.Effects != expected {
			t.Fatalf("%s effects=%#v want=%#v", name, descriptor.Effects, expected)
		}
		base, err := ProjectTool(BaseProfile(), descriptor, ToolProjectionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		openai, err := ProjectTool(openAIConfigProjectionProbe{}, descriptor, ToolProjectionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(base.Annotations, expected.annotations()) || !reflect.DeepEqual(openai.Annotations, expected.annotations()) {
			t.Fatalf("%s projected effects drifted base=%#v openai=%#v", name, base.Annotations, openai.Annotations)
		}
	}
}

func TestConfigSetHostConfirmationCannotBypassCodeMCPApprovalAcrossProfiles(t *testing.T) {
	for name, profile := range map[string]Profile{
		"base":                    BaseProfile(),
		"openai-compatible-probe": openAIConfigProjectionProbe{},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CM_CONFIG_DIR", t.TempDir())
			toolRuntime := tools.NewRuntime()
			workspace, err := toolRuntime.Workspaces.Register(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			provider := &profileConfigSetProvider{}
			toolRuntime.SetConfigSetApprovalProvider(provider)
			toolRuntime.SetConfigSetApplyProvider(provider)
			ctx := tools.WithCallSource(context.Background(), "http")
			ctx = tools.WithApprovalCorrelation(ctx, "profile-caller", "profile-request")
			ctx = tools.WithInputRound(ctx, "host-confirmed", map[string]any{
				"confirm": map[string]any{"accepted": true},
			})
			result, err := NewRuntimeWithProfile(toolRuntime, profile).Handle(ctx, "tools/call", map[string]any{
				"name": mcpconfigwire.SetToolName,
				"arguments": map[string]any{
					"workspace_id": workspace.ID,
					"changes":      []any{map[string]any{"key": "server.port", "value": "41001"}},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			toolResult, ok := result.(tools.Result)
			if !ok || !toolResult.IsError || provider.applies.Load() != 0 {
				t.Fatalf("host confirmation bypassed CodeMCP approval: result=%#v applies=%d", result, provider.applies.Load())
			}
			data, err := json.Marshal(toolResult)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "approval_required") {
				t.Fatalf("approval challenge missing: %s", data)
			}
		})
	}
}

func TestConfigSetApprovedExecutionIsCanonicalAcrossProfiles(t *testing.T) {
	var canonicalJSON string
	for name, profile := range map[string]Profile{
		"base":                    BaseProfile(),
		"openai-compatible-probe": openAIConfigProjectionProbe{},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CM_CONFIG_DIR", t.TempDir())
			toolRuntime := tools.NewRuntime()
			workspace, err := toolRuntime.Workspaces.Register(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			provider := &profileConfigSetProvider{}
			toolRuntime.SetConfigSetApprovalProvider(provider)
			toolRuntime.SetConfigSetApplyProvider(provider)
			args := map[string]any{
				"workspace_id": workspace.ID,
				"changes": []any{
					map[string]any{"key": "server.port", "value": "41001"},
					map[string]any{"key": "admin.port", "value": "41002"},
				},
			}
			server := NewRuntimeWithProfile(toolRuntime, profile)
			initialCtx := tools.WithCallSource(context.Background(), "http")
			initialCtx = tools.WithApprovalCorrelation(initialCtx, "profile-approved-caller", "profile-approved-initial")
			first, err := server.Handle(initialCtx, "tools/call", map[string]any{"name": mcpconfigwire.SetToolName, "arguments": args})
			if err != nil {
				t.Fatal(err)
			}
			firstResult, ok := first.(tools.Result)
			if !ok || !firstResult.IsError || provider.applies.Load() != 0 {
				t.Fatalf("pre-approval result=%#v applies=%d", first, provider.applies.Load())
			}
			bodyJSON, err := json.Marshal(firstResult.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			if err := json.Unmarshal(bodyJSON, &body); err != nil {
				t.Fatal(err)
			}
			if body["code"] != "approval_required" {
				t.Fatalf("approval challenge=%#v", firstResult.StructuredContent)
			}
			challengeID, _ := body["challenge_id"].(string)
			request, created, err := toolRuntime.Approvals.CreateRequestWithTitle(challengeID, "profile-approved-caller", workspace.ID, "Update CodeMCP settings")
			if err != nil || !created {
				t.Fatalf("request=%#v created=%t err=%v", request, created, err)
			}
			if _, err := toolRuntime.Approvals.Approve(request.ID, "reviewer", ""); err != nil {
				t.Fatal(err)
			}
			retryCtx := tools.WithCallSource(context.Background(), "http")
			retryCtx = tools.WithApprovalCorrelation(retryCtx, "profile-approved-caller", "profile-approved-retry")
			retry, err := server.Handle(retryCtx, "tools/call", map[string]any{"name": mcpconfigwire.SetToolName, "arguments": args})
			if err != nil {
				t.Fatal(err)
			}
			retryResult, ok := retry.(tools.Result)
			if !ok || retryResult.IsError || provider.applies.Load() != 1 {
				t.Fatalf("approved retry=%#v applies=%d", retry, provider.applies.Load())
			}
			mutation, ok := retryResult.StructuredContent.(mcpconfigwire.MutationResult)
			if !ok || mutation.State != mcpconfigwire.MutationRuntimeSynced || mutation.RuntimeSync != mcpconfigwire.RuntimeSyncCurrent ||
				!mutation.RuntimeReloaded || mutation.ChangeCount != 2 {
				t.Fatalf("mutation=%#v", retryResult.StructuredContent)
			}
			data, err := json.Marshal(mutation)
			if err != nil {
				t.Fatal(err)
			}
			if canonicalJSON == "" {
				canonicalJSON = string(data)
			} else if canonicalJSON != string(data) {
				t.Fatalf("profile changed approved execution: first=%s current=%s", canonicalJSON, data)
			}
		})
	}
}

func jsonSemanticEqual(left, right json.RawMessage) bool {
	if len(left) == 0 || len(right) == 0 {
		return len(left) == len(right)
	}
	var a, b any
	if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}
