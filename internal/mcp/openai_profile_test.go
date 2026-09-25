package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"go.mewis.me/codemcp/internal/tools"
)

func TestResolveProfile(t *testing.T) {
	for _, test := range []struct {
		value string
		want  ProfileID
	}{{"", BaseProfileID}, {" base ", BaseProfileID}, {"OPENAI", OpenAIProfileID}} {
		profile, err := ResolveProfile(test.value)
		if err != nil || profile.ID() != test.want {
			t.Fatalf("ResolveProfile(%q)=%v, %v want %q", test.value, profile, err, test.want)
		}
	}
	if _, err := ResolveProfile("unknown"); err == nil {
		t.Fatal("unknown profile was accepted")
	}
}

func TestOpenAIProfileProjectsCompatibleToolMetadataWithoutAppsUI(t *testing.T) {
	descriptor := DescribeTool(tools.Schema{
		Name:         "openai_probe",
		Title:        strings.Repeat("Long tool title ", 8),
		Description:  "Canonical description.",
		InputSchema:  json.RawMessage(`{"type":"object","additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		Annotations:  tools.ToolAnnotationsOpenWorld(tools.RiskDestructive),
	})
	base, err := ProjectTool(BaseProfile(), descriptor, ToolProjectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	openai, err := ProjectTool(OpenAIProfile(), descriptor, ToolProjectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if base.Name != openai.Name || base.Title != openai.Title || base.Description != openai.Description ||
		!reflect.DeepEqual(base.InputSchema, openai.InputSchema) || !reflect.DeepEqual(base.OutputSchema, openai.OutputSchema) ||
		!reflect.DeepEqual(base.Annotations, openai.Annotations) {
		t.Fatalf("OpenAI profile changed canonical tool truth:\nbase=%#v\nopenai=%#v", base, openai)
	}
	for _, key := range []string{"openai/toolInvocation/invoking", "openai/toolInvocation/invoked"} {
		value, ok := openai.Meta[key].(string)
		if !ok || value == "" || utf8.RuneCountInString(value) > 64 {
			t.Fatalf("%s=%#v", key, openai.Meta[key])
		}
	}
	for _, forbidden := range []string{"ui", "openai/outputTemplate", "openai/widgetAccessible", "openai/visibility", "openai/fileParams", "openai/profile"} {
		if _, exists := openai.Meta[forbidden]; exists {
			t.Fatalf("OpenAI profile exposed excluded Apps/UI metadata %q", forbidden)
		}
	}
}

func TestOpenAIProfilePrioritizesCanonicalWorkflowGuidance(t *testing.T) {
	base := ProjectServerInstructions(BaseProfile())
	openai := ProjectServerInstructions(OpenAIProfile())
	if !strings.HasPrefix(openai, openAIInstructionHeading+" ") || strings.TrimPrefix(openai, openAIInstructionHeading+" ") != base {
		t.Fatalf("OpenAI instructions changed canonical semantics: %q", openai)
	}
	first := []rune(openai)
	if len(first) > 512 {
		first = first[:512]
	}
	if !strings.Contains(string(first), "project_context") || !strings.Contains(string(first), "CodeMCP approval remains authoritative") {
		t.Fatalf("important OpenAI workflow guidance is not early: %q", string(first))
	}
}

func TestOpenAIRequestMetadataBecomesNeutralHintsWithoutSecurityAuthority(t *testing.T) {
	registry := tools.NewRegistry()
	var client tools.ClientHints
	var requestCorrelation tools.RequestCorrelationHints
	var approvalCorrelation tools.ApprovalCorrelation
	var workspace, session string
	registry.MustRegister("hint_probe", tools.Schema{
		Name:        "hint_probe",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		Annotations: tools.ToolAnnotations(tools.RiskRead),
	}, func(ctx context.Context, _ map[string]any) (tools.Result, error) {
		client = tools.ClientHintsFromContext(ctx)
		requestCorrelation = tools.RequestCorrelationHintsFromContext(ctx)
		approvalCorrelation = tools.ApprovalCorrelationFromContext(ctx)
		workspace = tools.BoundWorkspace(ctx)
		session = tools.MCPSessionID(ctx)
		return tools.JSONResult(map[string]any{"ok": true}), nil
	})

	ctx := tools.WithBoundWorkspace(context.Background(), "ws_authorized")
	ctx = tools.WithMCPSessionID(ctx, "mcp_authorized")
	ctx = tools.WithApprovalCorrelation(ctx, "caller_authorized", "request_authorized")
	params := map[string]any{
		"name":      "hint_probe",
		"arguments": map[string]any{},
		"_meta": map[string]any{
			"io.modelcontextprotocol/protocolVersion": SupportedProtocolVersion,
			openAILocaleMetaKey:                       " vi-VN ",
			openAIUserAgentMetaKey:                    " ChatGPT/Test ",
			openAIUserLocationMetaKey: map[string]any{
				"city": "Hanoi", "region": "HN", "country": "VN", "timezone": "Asia/Bangkok",
				"longitude": json.Number("105.84"), "latitude": json.Number("21.03"),
			},
			openAISubjectMetaKey:      "spoofed-subject",
			openAISessionMetaKey:      "spoofed-session",
			openAIOrganizationMetaKey: "spoofed-organization",
			"workspace_id":            "ws_spoofed",
			"approval_caller":         "caller_spoofed",
		},
	}
	if _, err := NewRuntimeWithProfile(&tools.Runtime{Registry: registry}, OpenAIProfile()).Handle(ctx, "tools/call", params); err != nil {
		t.Fatal(err)
	}
	if client.Locale != "vi-VN" || client.UserAgent != "ChatGPT/Test" || client.Location.City != "Hanoi" ||
		client.Location.Longitude == nil || *client.Location.Longitude != 105.84 || client.Location.Latitude == nil || *client.Location.Latitude != 21.03 {
		t.Fatalf("client hints=%#v", client)
	}
	if requestCorrelation != (tools.RequestCorrelationHints{SubjectID: "spoofed-subject", SessionID: "spoofed-session", OrganizationID: "spoofed-organization"}) {
		t.Fatalf("request correlation=%#v", requestCorrelation)
	}
	if workspace != "ws_authorized" || session != "mcp_authorized" || approvalCorrelation != (tools.ApprovalCorrelation{CallerID: "caller_authorized", RequestID: "request_authorized"}) {
		t.Fatalf("OpenAI hints changed security identity: workspace=%q session=%q approval=%#v", workspace, session, approvalCorrelation)
	}
}

func TestBaseProfileIgnoresOpenAIRequestMetadata(t *testing.T) {
	ctx := withProfileRequestMetadata(context.Background(), BaseProfile(), map[string]any{
		openAILocaleMetaKey:  "vi-VN",
		openAISessionMetaKey: "session",
	})
	if hints := tools.ClientHintsFromContext(ctx); hints != (tools.ClientHints{}) {
		t.Fatalf("base client hints=%#v", hints)
	}
	if hints := tools.RequestCorrelationHintsFromContext(ctx); hints != (tools.RequestCorrelationHints{}) {
		t.Fatalf("base correlation hints=%#v", hints)
	}
}
