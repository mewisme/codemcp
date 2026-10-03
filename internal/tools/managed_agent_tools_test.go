package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestManagedAgentToolSchemasAreStrictAndBounded(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := NewRuntime()
	defer runtime.CompletionHooks.Stop()
	for _, name := range []string{AgentSpawnToolName, AgentListToolName, AgentWaitToolName, AgentSendToolName, AgentCancelToolName} {
		schema, ok := runtime.Registry.Schema(name)
		if !ok {
			t.Fatalf("%s schema missing", name)
		}
		if !strings.Contains(string(schema.InputSchema), `"additionalProperties":false`) {
			t.Fatalf("%s input schema is not strict: %s", name, schema.InputSchema)
		}
	}
	wait, _ := runtime.Registry.Schema(AgentWaitToolName)
	if !strings.Contains(string(wait.InputSchema), `"maximum":10000`) {
		t.Fatalf("agent_wait is not bounded to 10 seconds: %s", wait.InputSchema)
	}
	spawn, _ := runtime.Registry.Schema(AgentSpawnToolName)
	spawnSchema := string(spawn.InputSchema)
	for _, forbidden := range []string{"owner", "controller", "session_id", "parent_id", "depth"} {
		if strings.Contains(spawnSchema, forbidden) {
			t.Fatalf("agent_spawn exposes trusted orchestration field %q: %s", forbidden, spawnSchema)
		}
	}
}

func TestManagedAgentObservabilityRedactsPromptAndMessageBodies(t *testing.T) {
	prompt := "highly-sensitive-delegation-prompt"
	message := "highly-sensitive-follow-up"
	spawn := observableToolArguments(AgentSpawnToolName, map[string]any{
		"workspace_id": "ws_test", "backend": "test", "model": "m", "reasoning_effort": "high", "prompt": prompt,
	})
	send := observableToolArguments(AgentSendToolName, map[string]any{
		"agent_id": "agent_0123456789abcdef", "message": message,
	})
	data, err := json.Marshal([]any{spawn, send})
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, secret := range []string{prompt, message} {
		if strings.Contains(text, secret) {
			t.Fatalf("managed-agent observability leaked body %q: %s", secret, text)
		}
	}
	for _, expected := range []string{"prompt_bytes", "message_bytes", "workspace_id", "agent_id"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("managed-agent observability missing %q: %s", expected, text)
		}
	}
}
