package tunnel

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/instructioncontext"
	localmcp "go.mewis.me/codemcp/internal/mcp"
	"go.mewis.me/codemcp/internal/tools"
)

type tunnelSkillsListParams struct {
	sdkmcp.ParamsBase
	Cursor string `json:"cursor,omitempty"`
}

type tunnelSkillResource struct {
	URI    string `json:"uri"`
	Digest string `json:"digest"`
}

type tunnelSkillEntry struct {
	URI       string                `json:"uri"`
	Name      string                `json:"name"`
	Resources []tunnelSkillResource `json:"resources"`
}

type tunnelSkillsListResult struct {
	sdkmcp.ResultBase
	Skills []tunnelSkillEntry `json:"skills"`
}

type tunnelSkillsGetParams struct {
	sdkmcp.ParamsBase
	URI string `json:"uri"`
}

type tunnelSkillsGetResult struct {
	sdkmcp.ResultBase
	URI       string                `json:"uri"`
	Name      string                `json:"name"`
	Resources []tunnelSkillResource `json:"resources"`
}

func TestSecureTunnelOpenAIProfilePreservesResourcePromptSkillTruth(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, configRoot)
	prepareTunnelFeatureFixture(t, configRoot)
	runtime := tools.NewRuntime()
	bridge, err := newSDKBridge(runtime)
	if err != nil {
		t.Fatal(err)
	}

	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- bridge.Run(ctx, serverTransport) }()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "tunnel-feature-conformance", Version: "1.0.0"}, nil)
	if err := sdkmcp.AddSendingCustomMethod[*tunnelSkillsListParams, *tunnelSkillsListResult](client, localmcp.SkillsListMethod); err != nil {
		t.Fatal(err)
	}
	if err := sdkmcp.AddSendingCustomMethod[*tunnelSkillsGetParams, *tunnelSkillsGetResult](client, localmcp.SkillsGetMethod); err != nil {
		t.Fatal(err)
	}
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	initialized := session.InitializeResult()
	if initialized == nil || initialized.Capabilities == nil ||
		initialized.Capabilities.Resources == nil || initialized.Capabilities.Prompts == nil {
		t.Fatalf("tunnel capabilities=%#v", initialized)
	}
	if _, ok := initialized.Capabilities.Extensions[localmcp.SkillsExtensionID]; !ok {
		t.Fatalf("tunnel skills extension missing: %#v", initialized.Capabilities.Extensions)
	}
	for key := range initialized.Capabilities.Extensions {
		lower := strings.ToLower(key)
		for _, forbidden := range []string{"app", "ui", "excalidraw", "plugin"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("excluded tunnel capability surfaced as %q", key)
			}
		}
	}

	status, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: "cm://global/status"})
	if err != nil || len(status.Contents) != 1 || status.Contents[0].URI != "cm://global/status" {
		t.Fatalf("tunnel status=%#v err=%v", status, err)
	}

	prompt, err := session.GetPrompt(ctx, &sdkmcp.GetPromptParams{
		Name: "tunnel-conformance", Arguments: map[string]string{"topic": "matrix"},
	})
	if err != nil || len(prompt.Messages) != 1 {
		t.Fatalf("tunnel prompt=%#v err=%v", prompt, err)
	}
	promptText, ok := prompt.Messages[0].Content.(*sdkmcp.TextContent)
	if !ok || promptText.Text != "Tunnel matrix" {
		t.Fatalf("tunnel prompt content=%#v", prompt.Messages[0].Content)
	}

	listed, err := sdkmcp.CallCustomMethod[*tunnelSkillsListParams, *tunnelSkillsListResult](
		ctx, session, localmcp.SkillsListMethod, &tunnelSkillsListParams{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Skills) > 5 {
		t.Fatalf("OpenAI tunnel exceeded documented skill count: %d", len(listed.Skills))
	}
	var skill tunnelSkillEntry
	for _, candidate := range listed.Skills {
		if candidate.Name == "aaa-tunnel-conformance" {
			skill = candidate
			break
		}
	}
	if skill.Name == "" || len(skill.Resources) != 2 {
		t.Fatalf("tunnel skill list=%#v", listed.Skills)
	}
	got, err := sdkmcp.CallCustomMethod[*tunnelSkillsGetParams, *tunnelSkillsGetResult](
		ctx, session, localmcp.SkillsGetMethod, &tunnelSkillsGetParams{URI: skill.URI},
	)
	if err != nil || got.Name != skill.Name || got.URI != skill.URI || !reflect.DeepEqual(got.Resources, skill.Resources) {
		t.Fatalf("tunnel skills/get=%#v err=%v", got, err)
	}
	manifest, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: skill.URI})
	if err != nil || len(manifest.Contents) != 1 || !strings.Contains(manifest.Contents[0].Text, "tunnel skill body") {
		t.Fatalf("tunnel skill resource=%#v err=%v", manifest, err)
	}

	toolsList, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	fallbacks := map[string]bool{"list_skills": false, "load_skill": false}
	for _, tool := range toolsList.Tools {
		if _, ok := fallbacks[tool.Name]; ok {
			fallbacks[tool.Name] = true
		}
	}
	for name, present := range fallbacks {
		if !present {
			t.Fatalf("tunnel fallback tool %q missing", name)
		}
	}

	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil && err != context.Canceled {
			t.Fatalf("tunnel bridge: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("tunnel bridge did not stop")
	}
}

func prepareTunnelFeatureFixture(t *testing.T, configRoot string) {
	t.Helper()
	prompt := instructioncontext.PromptDefinition{
		Version: instructioncontext.PromptDefinitionVersion,
		Name:    "tunnel-conformance", Description: "Tunnel conformance prompt",
		Arguments: []instructioncontext.PromptArgument{{Name: "topic", Required: true}},
		Messages: []instructioncontext.PromptMessage{{
			Role:    "user",
			Content: instructioncontext.PromptTextContent{Type: "text", Text: "Tunnel {{topic}}"},
		}},
	}
	if _, err := instructioncontext.NewPromptStore("").Save(instructioncontext.PromptScopeGlobal, prompt); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(configRoot, "skills", "aaa-tunnel-conformance")
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: aaa-tunnel-conformance\ndescription: Tunnel conformance skill\n---\ntunnel skill body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "references", "note.txt"), []byte("support"), 0o644); err != nil {
		t.Fatal(err)
	}
}
