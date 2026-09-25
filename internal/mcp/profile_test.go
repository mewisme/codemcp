package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"go.mewis.me/codemcp/internal/tools"
)

type presentationOnlyProfile struct{}

func (presentationOnlyProfile) ID() ProfileID { return "presentation-test" }

func (presentationOnlyProfile) ToolRepresentation(tool ToolDescriptor) ToolRepresentation {
	return ToolRepresentation{
		Title:       "Profile: " + tool.Title,
		Description: "profile presentation",
		Meta:        map[string]any{"profile": "test"},
	}
}

func TestCanonicalDescriptorsSeparateEffectsFromRuntimeSecurityAuthority(t *testing.T) {
	schema := tools.Schema{
		Name:         "danger_probe",
		Title:        "Danger Probe",
		Description:  "Probe canonical descriptors.",
		InputSchema:  json.RawMessage(`{"type":"object","additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		Annotations:  tools.ToolAnnotationsOpenWorld(tools.RiskDestructive),
	}
	descriptor := DescribeProtocol([]tools.Schema{schema})
	if descriptor.Server.Name != "codemcp" || !descriptor.Capabilities.Tools.ListChanged || len(descriptor.Tools) != 1 {
		t.Fatalf("protocol descriptor=%#v", descriptor)
	}
	tool := descriptor.Tools[0]
	if tool.Security.ApprovalAuthority != runtimeApprovalAuthority || len(tool.Security.AuthRequirements) != 0 {
		t.Fatalf("tool security=%#v", tool.Security)
	}
	if tool.Effects.ReadOnly || !tool.Effects.Destructive || tool.Effects.Idempotent || !tool.Effects.OpenWorld {
		t.Fatalf("tool effects=%#v", tool.Effects)
	}
	if descriptor.Resources != nil || descriptor.Prompts != nil || descriptor.Skills != nil || len(descriptor.AuthRequirements) != 0 {
		t.Fatalf("unsupported descriptor surfaces should remain empty: %#v", descriptor)
	}
	if !descriptor.Results.SupportsStructuredContent || !descriptor.Results.SupportsInputRequired || !descriptor.Errors.JSONRPC || !descriptor.Errors.Tool {
		t.Fatalf("result/error descriptors=%#v/%#v", descriptor.Results, descriptor.Errors)
	}
}

func TestProfileCanChangePresentationButNotOperationTruth(t *testing.T) {
	descriptor := DescribeTool(tools.Schema{
		Name:         "profile_probe",
		Title:        "Profile Probe",
		Description:  "Canonical description.",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"workspace_id":{"type":"string"}},"required":["workspace_id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		Annotations:  tools.ToolAnnotations(tools.RiskRead),
	})
	base, err := ProjectTool(BaseProfile(), descriptor, ToolProjectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	custom, err := ProjectTool(presentationOnlyProfile{}, descriptor, ToolProjectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if custom.Title == base.Title || custom.Description == base.Description || custom.Meta["profile"] != "test" {
		t.Fatalf("profile presentation was not applied: base=%#v custom=%#v", base, custom)
	}
	if custom.Name != base.Name || string(custom.InputSchema) != string(base.InputSchema) || string(custom.OutputSchema) != string(base.OutputSchema) || !reflect.DeepEqual(custom.Annotations, base.Annotations) {
		t.Fatalf("profile changed operation truth: base=%#v custom=%#v", base, custom)
	}
	if descriptor.Security.ApprovalAuthority != runtimeApprovalAuthority || !descriptor.Effects.ReadOnly || !descriptor.Effects.Idempotent {
		t.Fatalf("canonical descriptor mutated=%#v", descriptor)
	}
}

func TestBaseSDKProjectionMatchesPortableProjection(t *testing.T) {
	descriptor := DescribeTool(tools.Schema{
		Name:         "parity_probe",
		Title:        "Parity Probe",
		Description:  "Compare base projection shapes.",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`),
		Annotations:  tools.ToolAnnotationsOpenWorld(tools.RiskRead),
	})
	portable, err := ProjectTool(BaseProfile(), descriptor, ToolProjectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sdk, err := ProjectSDKTool(BaseProfile(), descriptor, ToolProjectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var portableValue, sdkValue map[string]any
	portableJSON, _ := json.Marshal(portable)
	sdkJSON, _ := json.Marshal(sdk)
	if err := json.Unmarshal(portableJSON, &portableValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(sdkJSON, &sdkValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(portableValue, sdkValue) {
		t.Fatalf("base projections differ:\nportable=%s\nsdk=%s", portableJSON, sdkJSON)
	}
}

func TestProfileDoesNotAffectToolExecutionContextOrResult(t *testing.T) {
	registry := tools.NewRegistry()
	registry.MustRegister("execution_probe", tools.Schema{
		Name:        "execution_probe",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		Annotations: tools.ToolAnnotations(tools.RiskRead),
	}, func(ctx context.Context, _ map[string]any) (tools.Result, error) {
		correlation := tools.ApprovalCorrelationFromContext(ctx)
		return tools.JSONResult(map[string]any{
			"bound_workspace": tools.BoundWorkspace(ctx),
			"caller_id":       correlation.CallerID,
			"request_id":      correlation.RequestID,
		}), nil
	})
	toolRuntime := &tools.Runtime{Registry: registry}
	base := NewRuntimeWithProfile(toolRuntime, BaseProfile())
	custom := NewRuntimeWithProfile(toolRuntime, presentationOnlyProfile{})
	ctx := tools.WithApprovalCorrelation(tools.WithBoundWorkspace(context.Background(), "wsc_bound"), "apc_same", "apr_same")
	params := map[string]any{"name": "execution_probe", "arguments": map[string]any{}}
	baseResult, err := base.Handle(ctx, "tools/call", params)
	if err != nil {
		t.Fatal(err)
	}
	customResult, err := custom.Handle(ctx, "tools/call", params)
	if err != nil {
		t.Fatal(err)
	}
	baseJSON, _ := json.Marshal(baseResult)
	customJSON, _ := json.Marshal(customResult)
	if string(baseJSON) != string(customJSON) {
		t.Fatalf("profile changed execution result: base=%s custom=%s", baseJSON, customJSON)
	}
}
