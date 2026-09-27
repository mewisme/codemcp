package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/tools"
)

func TestPromptSDKProfilesShareScopedStoreAndRenderWithoutSideEffects(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	runtime := tools.NewRuntime()
	first, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	definition := instructioncontext.PromptDefinition{Version: instructioncontext.PromptDefinitionVersion, Name: "review", Description: "Review changes", Arguments: []instructioncontext.PromptArgument{{Name: "topic", Required: true}}, Messages: []instructioncontext.PromptMessage{{Role: "user", Content: instructioncontext.PromptTextContent{Type: "text", Text: "Review {{topic}}"}}}}
	store := instructioncontext.NewPromptStore(first.Path)
	if _, err := store.Save(instructioncontext.PromptScopeGlobal, definition); err != nil {
		t.Fatal(err)
	}
	definition.Messages[0].Content.Text = "Workspace {{topic}}"
	if _, err := store.Save(instructioncontext.PromptScopeWorkspace, definition); err != nil {
		t.Fatal(err)
	}
	for _, profile := range []Profile{BaseProfile(), OpenAIProfile()} {
		t.Run(string(profile.ID()), func(t *testing.T) {
			adapter, err := NewSDKServerWithProfile(runtime, "prompt-test", "", first.ID, profile)
			if err != nil {
				t.Fatal(err)
			}
			defer adapter.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
			done := make(chan error, 1)
			go func() { done <- adapter.Server.Run(ctx, serverTransport) }()
			client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "prompt-client", Version: "1.0.0"}, nil)
			session, err := client.Connect(ctx, clientTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			if session.InitializeResult().Capabilities.Prompts == nil {
				t.Fatal("prompt capability missing")
			}
			completed, err := adapter.Features.Complete(ctx, CompletionRequest{
				Ref:      CompletionReference{Type: "ref/prompt", Name: "review"},
				Argument: CompletionArgument{Name: "name", Value: "rev"},
			})
			if err != nil || len(completed.Values) != 1 || completed.Values[0] != "review" {
				t.Fatalf("prompt completion=%#v err=%v", completed, err)
			}
			free, err := adapter.Features.Complete(ctx, CompletionRequest{
				Ref:      CompletionReference{Type: "ref/prompt", Name: "review"},
				Argument: CompletionArgument{Name: "topic", Value: "s"},
			})
			if err != nil || len(free.Values) != 0 {
				t.Fatalf("free-text completion=%#v err=%v", free, err)
			}
			listed, err := session.ListPrompts(ctx, nil)
			if err != nil || len(listed.Prompts) != 1 || listed.Prompts[0].Name != "review" {
				t.Fatalf("list=%#v err=%v", listed, err)
			}
			got, err := session.GetPrompt(ctx, &sdkmcp.GetPromptParams{Name: "review", Arguments: map[string]string{"topic": "safety"}})
			if err != nil || len(got.Messages) != 1 {
				t.Fatalf("get=%#v err=%v", got, err)
			}
			body, ok := got.Messages[0].Content.(*sdkmcp.TextContent)
			if !ok || body.Text != "Workspace safety" {
				t.Fatalf("render=%#v", got.Messages[0].Content)
			}
			registryList, err := adapter.Features.Invoke(ctx, PromptsListMethod, nil)
			items, ok := registryList["prompts"].([]any)
			if err != nil || !ok || len(items) != 1 {
				t.Fatalf("registry prompt list=%#v err=%v", registryList, err)
			}
			registryGet, err := adapter.Features.Invoke(ctx, PromptsGetMethod, map[string]any{
				"name": "review", "arguments": map[string]any{"topic": "safety"},
			})
			if err != nil {
				t.Fatalf("registry prompt get=%#v err=%v", registryGet, err)
			}
			registryMessages, ok := registryGet["messages"].([]any)
			if !ok || len(registryMessages) != 1 {
				t.Fatalf("registry and SDK rendering differ: %#v %#v", registryGet, got)
			}
			registryMessage, ok := registryMessages[0].(map[string]any)
			if !ok || registryMessage["role"] != string(got.Messages[0].Role) {
				t.Fatalf("registry and SDK roles differ: %#v %#v", registryGet, got)
			}
			content, ok := registryMessage["content"].(map[string]any)
			if !ok || content["text"] != body.Text {
				t.Fatalf("registry and SDK rendering differ: %#v %#v", registryGet, got)
			}
			if _, err := session.GetPrompt(ctx, &sdkmcp.GetPromptParams{Name: "review"}); err == nil {
				t.Fatal("missing required prompt argument accepted")
			}
			if _, err := adapter.Features.Invoke(ctx, PromptsGetMethod, map[string]any{"workspace_id": second.ID, "name": "review"}); err == nil || !strings.Contains(err.Error(), "access") {
				t.Fatalf("cross-workspace prompt read=%v", err)
			}
			_ = session.Close()
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("SDK server did not stop")
			}
		})
	}
}
