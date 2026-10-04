package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/projectcontext"
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

func (presentationOnlyProfile) InstructionPresentation() InstructionPresentation {
	return InstructionPresentation{Heading: "Profile presentation."}
}

type filteredToolProfile struct{}

func (filteredToolProfile) ID() ProfileID { return "filtered-test" }

func (filteredToolProfile) ToolRepresentation(tool ToolDescriptor) ToolRepresentation {
	return ToolRepresentation{Title: tool.Title, Description: tool.Description}
}

func (filteredToolProfile) IncludeTool(tool ToolDescriptor) bool {
	return tool.Name != "git_push"
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

func TestApprovalCapableProjectionAddsReservedEnvelopeAcrossProfiles(t *testing.T) {
	descriptor := DescribeTool(tools.Schema{
		Name:        "approval_projection_probe",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"workspace_id":{"type":"string"},"command":{"type":"string"}},"required":["workspace_id","command"],"additionalProperties":false}`),
		Approval:    &tools.ApprovalMetadata{Inline: true},
	})
	for _, profile := range []Profile{BaseProfile(), OpenAIProfile(), presentationOnlyProfile{}} {
		projected, err := ProjectTool(profile, descriptor, ToolProjectionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		assertInlineApprovalProjection(t, projected.InputSchema, true)

		bound, err := ProjectTool(profile, descriptor, ToolProjectionOptions{BoundWorkspace: true})
		if err != nil {
			t.Fatal(err)
		}
		assertInlineApprovalProjection(t, bound.InputSchema, false)
	}
}

func TestProjectionDoesNotAddApprovalEnvelopeToOrdinaryTool(t *testing.T) {
	descriptor := DescribeTool(tools.Schema{
		Name:        "ordinary_projection_probe",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
	})
	projected, err := ProjectTool(BaseProfile(), descriptor, ToolProjectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(projected.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	properties, _ := schema["properties"].(map[string]any)
	if _, ok := properties[tools.InlineApprovalArgumentKey]; ok {
		t.Fatalf("ordinary tool gained inline approval envelope: %s", projected.InputSchema)
	}
	if _, ok := schema["properties"]; ok {
		t.Fatalf("ordinary tool projection changed schema shape: %s", projected.InputSchema)
	}
}

func TestProjectionRejectsReservedApprovalInputCollision(t *testing.T) {
	descriptor := DescribeTool(tools.Schema{
		Name:        "approval_collision_probe",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"_approval":{"type":"string"}},"additionalProperties":false}`),
		Approval:    &tools.ApprovalMetadata{Inline: true},
	})
	if _, err := ProjectTool(BaseProfile(), descriptor, ToolProjectionOptions{}); err == nil || !strings.Contains(err.Error(), "reserved for runtime approval metadata") {
		t.Fatalf("reserved approval collision error=%v", err)
	}
}

func assertInlineApprovalProjection(t *testing.T, raw json.RawMessage, expectWorkspace bool) {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	properties, _ := schema["properties"].(map[string]any)
	_, hasWorkspace := properties["workspace_id"]
	if hasWorkspace != expectWorkspace {
		t.Fatalf("workspace projection=%t want=%t schema=%s", hasWorkspace, expectWorkspace, raw)
	}
	approval, ok := properties[tools.InlineApprovalArgumentKey].(map[string]any)
	if !ok {
		t.Fatalf("inline approval schema missing: %s", raw)
	}
	if approval["type"] != "object" || approval["additionalProperties"] != false {
		t.Fatalf("inline approval object=%#v", approval)
	}
	nested, _ := approval["properties"].(map[string]any)
	challenge, _ := nested[tools.InlineApprovalChallengeID].(map[string]any)
	title, _ := nested[tools.InlineApprovalTitle].(map[string]any)
	if challenge["type"] != "string" || challenge["minLength"] != float64(1) {
		t.Fatalf("challenge schema=%#v", challenge)
	}
	if title["type"] != "string" || title["minLength"] != float64(1) || title["maxLength"] != float64(tools.InlineApprovalTitleMaxLen) {
		t.Fatalf("title schema=%#v", title)
	}
	required, _ := approval["required"].([]any)
	if len(required) != 2 || required[0] != tools.InlineApprovalChallengeID || required[1] != tools.InlineApprovalTitle {
		t.Fatalf("approval required=%#v", required)
	}
	topRequired, _ := schema["required"].([]any)
	for _, item := range topRequired {
		if item == tools.InlineApprovalArgumentKey {
			t.Fatalf("inline approval envelope became top-level required: %#v", topRequired)
		}
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

func TestFilteredProfileProjectsSameEffectiveInventoryIntoProjectContext(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	toolRuntime := tools.NewRuntime()
	defer toolRuntime.CompletionHooks.Stop()
	item, err := toolRuntime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile := filteredToolProfile{}
	server := NewRuntimeWithProfile(toolRuntime, profile)
	effective := EffectiveToolSchemas(profile, toolRuntime.List())
	if len(effective) == len(toolRuntime.List()) {
		t.Fatal("filtered profile did not remove a tool")
	}
	for _, schema := range effective {
		if schema.Name == "git_push" {
			t.Fatal("git_push remained in effective schema snapshot")
		}
	}

	listed, err := server.Handle(context.Background(), "tools/list", nil)
	if err != nil {
		t.Fatal(err)
	}
	listedJSON, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(listedJSON), `"name":"git_push"`) {
		t.Fatalf("filtered tools/list advertised git_push: %s", listedJSON)
	}
	if !strings.Contains(string(listedJSON), `"name":"run_command"`) {
		t.Fatalf("filtered tools/list lost run_command: %s", listedJSON)
	}

	value, err := server.Handle(context.Background(), "tools/call", map[string]any{
		"name": "project_context",
		"arguments": map[string]any{
			"workspace_id": item.ID, "include_git": false, "include_memory": false, "include_skills": false,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := value.(tools.Result)
	if !ok || result.IsError {
		t.Fatalf("project_context result=%#v", value)
	}
	project, ok := result.StructuredContent.(projectcontext.Result)
	if !ok {
		t.Fatalf("project_context structured content=%T", result.StructuredContent)
	}
	if project.InstructionContext.ToolProfile.Name != "filtered-test" || project.InstructionContext.ToolProfile.Count != len(effective) {
		t.Fatalf("profile/count=%#v effective=%d", project.InstructionContext.ToolProfile, len(effective))
	}
	capabilities := project.InstructionContext.ToolCapabilities
	if capabilities == nil || capabilities.TotalTools != len(effective) || capabilities.IncludedTools != len(effective) {
		t.Fatalf("capability counts=%#v effective=%d", capabilities, len(effective))
	}
	for _, group := range capabilities.Groups {
		for _, name := range group.Tools {
			if name == "git_push" {
				t.Fatalf("project_context advertised filtered tool: %#v", capabilities)
			}
		}
	}
	if !strings.Contains(project.InstructionContext.InstructionsText, "## Tool capabilities") || strings.Contains(project.InstructionContext.InstructionsText, "git_push") || !strings.Contains(project.InstructionContext.InstructionsText, "run_command") {
		t.Fatalf("filtered capability instructions=%s", project.InstructionContext.InstructionsText)
	}
	if _, err := server.Handle(context.Background(), "tools/call", map[string]any{"name": "git_push", "arguments": map[string]any{"workspace_id": item.ID}}); err == nil {
		t.Fatal("filtered tool remained callable through profile runtime")
	}
}

func TestRuntimeProjectsConfiguredAuthRequirementsWithoutChangingToolTruth(t *testing.T) {
	registry := tools.NewRegistry()
	registry.MustRegister("auth_runtime_probe", tools.Schema{
		Name:        "auth_runtime_probe",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		Annotations: tools.ToolAnnotations(tools.RiskRead),
	}, func(context.Context, map[string]any) (tools.Result, error) {
		return tools.JSONResult(map[string]any{"ok": true}), nil
	})
	server := NewRuntimeWithProfile(&tools.Runtime{Registry: registry}, OpenAIProfile())
	server.SetAuthRequirements(BearerAuthRequirement("mcp:tools"))
	value, err := server.Handle(context.Background(), "tools/list", nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"securitySchemes":[{"scopes":["mcp:tools"],"type":"oauth2"}]`) {
		t.Fatalf("tools/list auth metadata missing: %s", data)
	}
}

func TestProfileInstructionPresentationPreservesCanonicalSemantics(t *testing.T) {
	base := ProjectServerInstructions(BaseProfile())
	custom := ProjectServerInstructions(presentationOnlyProfile{})
	if custom == base || !strings.HasPrefix(custom, "Profile presentation. ") {
		t.Fatalf("custom instructions=%q", custom)
	}
	if strings.TrimPrefix(custom, "Profile presentation. ") != base {
		t.Fatalf("profile changed canonical instruction semantics")
	}
}

func TestBaseAndOpenAIProfilesShareManagedAgentDelegationSemantics(t *testing.T) {
	base := ProjectServerInstructions(BaseProfile())
	openai := ProjectServerInstructions(OpenAIProfile())
	for _, instructions := range []string{base, openai} {
		for _, expected := range []string{
			"fanout_turn",
			"exact current user prompt",
			"automatically delegate safe, meaningful independent workstreams",
			"do not wait for the user to request fanout or choose a job count",
		} {
			if !strings.Contains(instructions, expected) {
				t.Fatalf("profile instructions missing Fanout bootstrap %q: %s", expected, instructions)
			}
		}
		for _, detailed := range []string{
			"Parallel mutation requires explicit disjoint ownership",
			"bounded `agent_wait`",
			"claimed children cannot use `agent_spawn`",
			"Deduplicate overlapping findings",
		} {
			if strings.Contains(instructions, detailed) {
				t.Fatalf("profile instructions duplicated detailed Fanout policy %q: %s", detailed, instructions)
			}
		}
	}
	if strings.Contains(base, "/agent") || strings.Contains(openai, "/agent") {
		t.Fatalf("managed-agent delegation unexpectedly exposed slash directive: base=%q openai=%q", base, openai)
	}
}
