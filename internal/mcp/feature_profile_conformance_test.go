package mcp

import (
	"context"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/tools"
)

type conformanceSkillsListParams struct {
	sdkmcp.ParamsBase
	Cursor string `json:"cursor,omitempty"`
}

type conformanceSkillResource struct {
	URI    string `json:"uri"`
	Digest string `json:"digest"`
}

type conformanceSkillEntry struct {
	URI       string                     `json:"uri"`
	Name      string                     `json:"name"`
	Resources []conformanceSkillResource `json:"resources"`
}

type conformanceSkillsListResult struct {
	sdkmcp.ResultBase
	Skills     []conformanceSkillEntry `json:"skills"`
	NextCursor string                  `json:"nextCursor,omitempty"`
}

type conformanceSkillsGetParams struct {
	sdkmcp.ParamsBase
	URI string `json:"uri"`
}

type conformanceSkillsGetResult struct {
	sdkmcp.ResultBase
	URI       string                     `json:"uri"`
	Name      string                     `json:"name"`
	Resources []conformanceSkillResource `json:"resources"`
}

func TestResourcePromptSkillCapabilityMatrixIsProfileInvariant(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	runtime := tools.NewRuntime()
	registry := FeatureRegistryForRuntime(runtime)
	descriptor := DescribeProtocolWithFeatures(nil, registry)

	base := ProjectFeatures(BaseProfile(), descriptor)
	openai := ProjectFeatures(OpenAIProfile(), descriptor)
	if !reflect.DeepEqual(base.Resources, openai.Resources) ||
		!reflect.DeepEqual(base.ResourceTemplates, openai.ResourceTemplates) ||
		!reflect.DeepEqual(base.Prompts, openai.Prompts) ||
		!reflect.DeepEqual(base.Skills, openai.Skills) {
		t.Fatalf("profile changed canonical feature descriptors\nbase=%#v\nopenai=%#v", base, openai)
	}
	if descriptor.Capabilities.Resources == nil || !descriptor.Capabilities.Resources.Subscribe || !descriptor.Capabilities.Resources.ListChanged {
		t.Fatalf("resource listen capability missing: %#v", descriptor.Capabilities.Resources)
	}
	if descriptor.Capabilities.Prompts == nil {
		t.Fatal("prompt capability missing")
	}
	if _, ok := descriptor.Capabilities.Extensions[SkillsExtensionID]; !ok {
		t.Fatalf("skills extension missing: %#v", descriptor.Capabilities.Extensions)
	}

	wantMethods := []string{
		ResourcesListMethod, ResourceTemplatesListMethod, ResourcesReadMethod,
		PromptsListMethod, PromptsGetMethod, SkillsListMethod, SkillsGetMethod,
	}
	for _, profile := range []Profile{BaseProfile(), OpenAIProfile()} {
		executor := NewFeatureExecutor(registry, runtime, "", "conformance")
		executor.Profile = profile
		for _, method := range wantMethods {
			if !executor.SupportsMethod(method) {
				t.Fatalf("%s profile omitted method %q", profile.ID(), method)
			}
		}
	}
	for key := range descriptor.Capabilities.Extensions {
		lower := strings.ToLower(key)
		for _, forbidden := range []string{"app", "ui", "excalidraw", "plugin"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("excluded capability surfaced as %q", key)
			}
		}
	}
}

func TestResourcePromptSkillConformanceAcrossDirectProfilesAndTransports(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	runtime := tools.NewRuntime()
	workspaceRoot := t.TempDir()
	workspace, err := runtime.Workspaces.Register(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	prepareFeatureConformanceFixture(t, workspaceRoot)

	for _, tc := range []struct {
		name      string
		profile   Profile
		transport string
	}{
		{name: "base-stdio", profile: BaseProfile(), transport: "stdio"},
		{name: "base-streamable-http", profile: BaseProfile(), transport: "http"},
		{name: "openai-streamable-http", profile: OpenAIProfile(), transport: "http"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			transport, closeServer := conformanceTransport(t, ctx, cancel, runtime, workspace.ID, tc.profile, tc.transport)
			defer closeServer()

			client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "feature-conformance-" + tc.name, Version: "1.0.0"}, nil)
			if err := sdkmcp.AddSendingCustomMethod[*conformanceSkillsListParams, *conformanceSkillsListResult](client, SkillsListMethod); err != nil {
				t.Fatal(err)
			}
			if err := sdkmcp.AddSendingCustomMethod[*conformanceSkillsGetParams, *conformanceSkillsGetResult](client, SkillsGetMethod); err != nil {
				t.Fatal(err)
			}
			session, err := client.Connect(ctx, transport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			assertFeatureConformanceSession(t, ctx, session, workspace.ID, tc.profile)
		})
	}
}

func prepareFeatureConformanceFixture(t *testing.T, workspaceRoot string) {
	t.Helper()
	prompt := instructioncontext.PromptDefinition{
		Version: instructioncontext.PromptDefinitionVersion,
		Name:    "conformance-review", Description: "Cross-profile review",
		Arguments: []instructioncontext.PromptArgument{{Name: "topic", Required: true}},
		Messages: []instructioncontext.PromptMessage{{
			Role:    "user",
			Content: instructioncontext.PromptTextContent{Type: "text", Text: "Conformance {{topic}}"},
		}},
	}
	if _, err := instructioncontext.NewPromptStore(workspaceRoot).Save(instructioncontext.PromptScopeWorkspace, prompt); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(workspaceRoot, ".cm", "skills", "aaa-conformance")
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: aaa-conformance\ndescription: Cross-profile skill\n---\nconformance skill body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "references", "note.txt"), []byte("support"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func conformanceTransport(t *testing.T, ctx context.Context, cancel context.CancelFunc, runtime *tools.Runtime, workspaceID string, profile Profile, kind string) (sdkmcp.Transport, func()) {
	t.Helper()
	if kind == "stdio" {
		clientToServerReader, clientToServerWriter := io.Pipe()
		serverToClientReader, serverToClientWriter := io.Pipe()
		server, err := NewSDKServerWithProfile(runtime, "stdio", "", workspaceID, profile)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			done <- server.Server.Run(ctx, &sdkmcp.IOTransport{Reader: clientToServerReader, Writer: serverToClientWriter})
		}()
		return &sdkmcp.IOTransport{Reader: serverToClientReader, Writer: clientToServerWriter}, func() {
			server.Close()
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
			}
		}
	}
	handler, err := NewSDKHTTPHandlerWithProfile(runtime, workspaceID, false, profile)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	return &sdkmcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", DisableStandaloneSSE: true}, server.Close
}

func assertFeatureConformanceSession(t *testing.T, ctx context.Context, session *sdkmcp.ClientSession, workspaceID string, profile Profile) {
	t.Helper()
	initialized := session.InitializeResult()
	if initialized == nil || initialized.Capabilities == nil ||
		initialized.Capabilities.Resources == nil || initialized.Capabilities.Prompts == nil {
		t.Fatalf("%s capabilities=%#v", profile.ID(), initialized)
	}
	if !initialized.Capabilities.Resources.Subscribe || !initialized.Capabilities.Resources.ListChanged {
		t.Fatalf("%s resource subscription capability=%#v", profile.ID(), initialized.Capabilities.Resources)
	}
	if _, ok := initialized.Capabilities.Extensions[SkillsExtensionID]; !ok {
		t.Fatalf("%s skills extension missing: %#v", profile.ID(), initialized.Capabilities.Extensions)
	}

	projectURI, err := WorkspaceResourceURI(workspaceID, resourcePathProjectContext)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: projectURI})
	if err != nil {
		t.Fatal(err)
	}
	if len(resource.Contents) != 1 || resource.Contents[0].URI != projectURI || resource.CacheScope != ResourceCacheScopePrivate {
		t.Fatalf("%s resource=%#v", profile.ID(), resource)
	}

	prompt, err := session.GetPrompt(ctx, &sdkmcp.GetPromptParams{Name: "conformance-review", Arguments: map[string]string{"topic": "matrix"}})
	if err != nil || len(prompt.Messages) != 1 {
		t.Fatalf("%s prompt=%#v err=%v", profile.ID(), prompt, err)
	}
	text, ok := prompt.Messages[0].Content.(*sdkmcp.TextContent)
	if !ok || text.Text != "Conformance matrix" {
		t.Fatalf("%s prompt content=%#v", profile.ID(), prompt.Messages[0].Content)
	}

	skillsList, err := sdkmcp.CallCustomMethod[*conformanceSkillsListParams, *conformanceSkillsListResult](
		ctx, session, SkillsListMethod, &conformanceSkillsListParams{},
	)
	if err != nil {
		t.Fatal(err)
	}
	var skill conformanceSkillEntry
	for _, candidate := range skillsList.Skills {
		if candidate.Name == "aaa-conformance" {
			skill = candidate
			break
		}
	}
	if skill.Name == "" || len(skill.Resources) != 2 {
		t.Fatalf("%s skill list=%#v", profile.ID(), skillsList.Skills)
	}
	gotSkill, err := sdkmcp.CallCustomMethod[*conformanceSkillsGetParams, *conformanceSkillsGetResult](
		ctx, session, SkillsGetMethod, &conformanceSkillsGetParams{URI: skill.URI},
	)
	if err != nil || gotSkill.Name != skill.Name || gotSkill.URI != skill.URI || !reflect.DeepEqual(gotSkill.Resources, skill.Resources) {
		t.Fatalf("%s skills/get=%#v err=%v", profile.ID(), gotSkill, err)
	}
	manifest, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: skill.URI})
	if err != nil || len(manifest.Contents) != 1 || !strings.Contains(manifest.Contents[0].Text, "conformance skill body") {
		t.Fatalf("%s skill resource=%#v err=%v", profile.ID(), manifest, err)
	}

	toolsList, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(toolsList.Tools))
	for _, tool := range toolsList.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	for _, required := range []string{"list_skills", "load_skill"} {
		if index := sort.SearchStrings(names, required); index >= len(names) || names[index] != required {
			t.Fatalf("%s fallback tool %q missing from %v", profile.ID(), required, names)
		}
	}
	loaded, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "load_skill",
		Arguments: map[string]any{"workspace_id": workspaceID, "name": "aaa-conformance", "max_bytes": 500000},
	})
	if err != nil || loaded.IsError || len(loaded.Content) == 0 {
		t.Fatalf("%s fallback load=%#v err=%v", profile.ID(), loaded, err)
	}
	loadedText, ok := loaded.Content[0].(*sdkmcp.TextContent)
	if !ok || !strings.Contains(loadedText.Text, "conformance skill body") {
		t.Fatalf("%s fallback content=%#v", profile.ID(), loaded.Content)
	}
}
