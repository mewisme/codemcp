package instructioncontext

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func TestPromptStoreScopesAndWorkspacePrecedence(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	workspaceRoot := t.TempDir()
	if _, _, err := workspacestate.New(workspaceRoot).EnsureIdentity(""); err != nil {
		t.Fatal(err)
	}
	store := NewPromptStore(workspaceRoot)

	globalShared := testPromptDefinition("shared", "global {{topic}}")
	globalOnly := testPromptDefinition("global-only", "global only")
	workspaceShared := testPromptDefinition("shared", "workspace {{topic}}")
	workspaceOnly := testPromptDefinition("workspace-only", "workspace only")

	for scope, definitions := range map[PromptScope][]PromptDefinition{
		PromptScopeGlobal:    {globalShared, globalOnly},
		PromptScopeWorkspace: {workspaceShared, workspaceOnly},
	} {
		for _, definition := range definitions {
			if _, err := store.Save(scope, definition); err != nil {
				t.Fatalf("save %s/%s: %v", scope, definition.Name, err)
			}
		}
	}

	if got, want := store.WorkspaceRoot(), workspacestate.New(workspaceRoot).PromptRoot(); got != want {
		t.Fatalf("workspace prompt root=%q want=%q", got, want)
	}
	workspaceInfo, err := os.Stat(filepath.Join(store.WorkspaceRoot(), "shared.json"))
	if err != nil {
		t.Fatalf("workspace prompt was not stored beneath .cm/prompts: %v", err)
	}
	if runtime.GOOS != "windows" && workspaceInfo.Mode().Perm() != 0600 {
		t.Fatalf("workspace prompt mode=%#o", workspaceInfo.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(store.GlobalRoot(), "shared.json")); err != nil {
		t.Fatalf("global prompt was not stored beneath global prompt root: %v", err)
	}

	prompt, err := store.Get("shared")
	if err != nil {
		t.Fatal(err)
	}
	if prompt.Scope != PromptScopeWorkspace || prompt.Definition.Messages[0].Content.Text != "workspace {{topic}}" {
		t.Fatalf("workspace prompt did not shadow global prompt: %#v", prompt)
	}

	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("prompt list=%#v", list)
	}
	names := []string{list[0].Definition.Name, list[1].Definition.Name, list[2].Definition.Name}
	if want := []string{"global-only", "shared", "workspace-only"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("prompt order=%#v want=%#v", names, want)
	}
	if list[1].Scope != PromptScopeWorkspace {
		t.Fatalf("shadowed prompt scope=%q", list[1].Scope)
	}

	globalStore := NewPromptStore("")
	globalPrompt, err := globalStore.Get("shared")
	if err != nil {
		t.Fatal(err)
	}
	if globalPrompt.Scope != PromptScopeGlobal || globalPrompt.Definition.Messages[0].Content.Text != "global {{topic}}" {
		t.Fatalf("global-only store resolved %#v", globalPrompt)
	}
}

func TestPromptStoreRejectsPathAndSymlinkEscapes(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	workspaceRoot := t.TempDir()
	if _, _, err := workspacestate.New(workspaceRoot).EnsureIdentity(""); err != nil {
		t.Fatal(err)
	}
	store := NewPromptStore(workspaceRoot)

	bad := testPromptDefinition("../escape", "no")
	if _, err := store.Save(PromptScopeWorkspace, bad); err == nil {
		t.Fatal("path-like prompt name was accepted")
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, ".cm", "escape.json")); !os.IsNotExist(err) {
		t.Fatalf("escape path was created: %v", err)
	}

	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel.json")
	if err := os.WriteFile(sentinel, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, store.WorkspaceRoot()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(PromptScopeWorkspace, testPromptDefinition("safe", "content")); err == nil {
		t.Fatal("symlinked prompt root was accepted")
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "outside" {
		t.Fatalf("outside state changed: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "safe.json")); !os.IsNotExist(err) {
		t.Fatalf("write followed prompt-root symlink: %v", err)
	}

	if err := os.Remove(store.WorkspaceRoot()); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.WorkspaceRoot(), 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(outside, "target.json")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(store.WorkspaceRoot(), "safe.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(PromptScopeWorkspace, testPromptDefinition("safe", "content")); err == nil {
		t.Fatal("symlink prompt target was accepted")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "keep" {
		t.Fatalf("symlink target changed: data=%q err=%v", data, err)
	}
}

func TestPromptStoreRejectsSymlinkedGlobalConfigRoot(t *testing.T) {
	realConfig := t.TempDir()
	link := filepath.Join(t.TempDir(), "config")
	if err := os.Symlink(realConfig, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv(configformat.EnvConfigDir, link)
	store := NewPromptStore("")
	if _, err := store.Save(PromptScopeGlobal, testPromptDefinition("blocked", "content")); err == nil {
		t.Fatal("global prompt write accepted symlinked config root")
	}
	if _, err := os.Stat(filepath.Join(realConfig, "prompts", "blocked.json")); !os.IsNotExist(err) {
		t.Fatalf("global write escaped through config-root symlink: %v", err)
	}
}

func TestPromptDefinitionValidationAndRenderingBounds(t *testing.T) {
	valid := testPromptDefinition("review", "Review {{topic}} with {{style}}")
	valid.Arguments = []PromptArgument{
		{Name: "topic", Description: "subject", Required: true},
		{Name: "style", Description: "optional style"},
	}
	if err := ValidatePromptDefinition(valid); err != nil {
		t.Fatalf("valid definition: %v", err)
	}

	before, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderPrompt(valid, map[string]string{"topic": "storage"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rendered) != 1 || rendered[0].Content.Text != "Review storage with " {
		t.Fatalf("rendered=%#v", rendered)
	}
	after, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("render mutated definition: before=%s after=%s", before, after)
	}
	if _, err := RenderPrompt(valid, map[string]string{}); err == nil {
		t.Fatal("missing required argument was accepted")
	}
	if _, err := RenderPrompt(valid, map[string]string{"topic": "x", "unknown": "y"}); err == nil {
		t.Fatal("unknown argument was accepted")
	}
	if _, err := RenderPrompt(valid, map[string]string{"topic": strings.Repeat("x", maxPromptArgumentBytes+1)}); err == nil {
		t.Fatal("oversized argument was accepted")
	}

	cases := []PromptDefinition{
		func() PromptDefinition { value := valid; value.Version = 0; return value }(),
		func() PromptDefinition { value := valid; value.Name = "bad/name"; return value }(),
		func() PromptDefinition {
			value := valid
			value.Arguments = append(value.Arguments, PromptArgument{Name: "topic"})
			return value
		}(),
		func() PromptDefinition {
			value := valid
			value.Messages[0].Role = "system"
			return value
		}(),
		func() PromptDefinition {
			value := valid
			value.Messages[0].Content.Type = "image"
			return value
		}(),
		func() PromptDefinition {
			value := valid
			value.Messages[0].Content.Text = "Unknown {{missing}}"
			return value
		}(),
		func() PromptDefinition {
			value := valid
			value.Messages[0].Content.Text = "Broken {{topic"
			return value
		}(),
		func() PromptDefinition {
			value := valid
			value.Messages[0].Content.Text = strings.Repeat("x", maxPromptMessageBytes+1)
			return value
		}(),
		func() PromptDefinition {
			value := testPromptDefinition("too-many-args", "content")
			value.Arguments = make([]PromptArgument, maxPromptArguments+1)
			for index := range value.Arguments {
				value.Arguments[index] = PromptArgument{Name: "arg" + string(rune('A'+index%26)) + string(rune('0'+index/26))}
			}
			return value
		}(),
		func() PromptDefinition {
			value := testPromptDefinition("too-many-messages", "content")
			value.Messages = make([]PromptMessage, maxPromptMessages+1)
			for index := range value.Messages {
				value.Messages[index] = PromptMessage{Role: "user", Content: PromptTextContent{Type: "text", Text: "x"}}
			}
			return value
		}(),
		func() PromptDefinition {
			value := testPromptDefinition("too-much-content", "content")
			chunk := strings.Repeat("x", maxPromptMessageBytes)
			value.Messages = []PromptMessage{
				{Role: "user", Content: PromptTextContent{Type: "text", Text: chunk}},
				{Role: "assistant", Content: PromptTextContent{Type: "text", Text: chunk}},
				{Role: "user", Content: PromptTextContent{Type: "text", Text: chunk}},
				{Role: "assistant", Content: PromptTextContent{Type: "text", Text: chunk}},
				{Role: "user", Content: PromptTextContent{Type: "text", Text: "x"}},
			}
			return value
		}(),
	}
	for index, value := range cases {
		if err := ValidatePromptDefinition(value); err == nil {
			t.Fatalf("invalid definition %d was accepted", index)
		}
	}
}

func TestPromptRenderingTreatsToolAndShellLikeInputAsLiteralText(t *testing.T) {
	definition := testPromptDefinition("literal-only", "Execute? {{topic}}")
	sentinel := filepath.Join(t.TempDir(), "must-not-exist")
	value := "$(touch " + sentinel + ") cm config set http.mcp.port 1"
	rendered, err := RenderPrompt(definition, map[string]string{"topic": value})
	if err != nil {
		t.Fatal(err)
	}
	if len(rendered) != 1 || rendered[0].Content.Text != "Execute? "+value {
		t.Fatalf("rendered=%#v", rendered)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("render executed shell-like input: %v", err)
	}
}

func TestPromptStoreStrictJSONAndWorkspaceStateValidation(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, configRoot)
	workspaceRoot := t.TempDir()
	if _, _, err := workspacestate.New(workspaceRoot).EnsureIdentity(""); err != nil {
		t.Fatal(err)
	}
	store := NewPromptStore(workspaceRoot)

	if _, err := store.Save(PromptScopeWorkspace, testPromptDefinition("valid", "ok")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWorkspacePromptState(store.WorkspaceRoot()); err != nil {
		t.Fatalf("valid workspace prompt state: %v", err)
	}

	badRoot := filepath.Join(t.TempDir(), "prompts")
	if err := os.MkdirAll(badRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badRoot, "legacy.json"), []byte("{\"name\":\"legacy\",\"prompt\":\"old\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWorkspacePromptState(badRoot); err == nil {
		t.Fatal("unversioned arbitrary prompt JSON was accepted")
	}

	if err := os.WriteFile(filepath.Join(store.WorkspaceRoot(), "unknown.json"), []byte("{\"version\":1,\"name\":\"unknown\",\"messages\":[{\"role\":\"user\",\"content\":{\"type\":\"text\",\"text\":\"ok\"}}],\"extra\":true}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.List(); err == nil {
		t.Fatal("unknown JSON fields were accepted")
	}
	if err := os.Remove(filepath.Join(store.WorkspaceRoot(), "unknown.json")); err != nil {
		t.Fatal(err)
	}

	mismatch := testPromptDefinition("actual", "ok")
	data, err := json.Marshal(mismatch)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.WorkspaceRoot(), "wrong.json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.List(); err == nil {
		t.Fatal("filename/definition mismatch was accepted")
	}
}

func TestPromptDiscoveryUsesCanonicalDefinitions(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	workspaceRoot := t.TempDir()
	if _, _, err := workspacestate.New(workspaceRoot).EnsureIdentity(""); err != nil {
		t.Fatal(err)
	}
	store := NewPromptStore(workspaceRoot)
	if _, err := store.Save(PromptScopeWorkspace, testPromptDefinition("alpha", "alpha")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(PromptScopeWorkspace, testPromptDefinition("beta", "beta")); err != nil {
		t.Fatal(err)
	}

	paths := discoverPromptDefinitions(store.WorkspaceRoot())
	want := []string{
		filepath.Join(store.WorkspaceRoot(), "alpha.json"),
		filepath.Join(store.WorkspaceRoot(), "beta.json"),
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("prompt discovery=%#v want=%#v", paths, want)
	}
}

func TestPromptWorkspaceScopeRequiresOwnedLocalState(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	workspaceRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspaceRoot, ".cm"), 0700); err != nil {
		t.Fatal(err)
	}
	store := NewPromptStore(workspaceRoot)
	if _, err := store.Save(PromptScopeWorkspace, testPromptDefinition("blocked", "content")); err == nil {
		t.Fatal("workspace prompt write accepted unowned .cm state")
	}
	if _, err := store.List(); err == nil {
		t.Fatal("workspace prompt read accepted unowned .cm state")
	}
}

func testPromptDefinition(name, text string) PromptDefinition {
	arguments := []PromptArgument{}
	if strings.Contains(text, "{{topic}}") {
		arguments = append(arguments, PromptArgument{Name: "topic"})
	}
	if strings.Contains(text, "{{style}}") {
		arguments = append(arguments, PromptArgument{Name: "style"})
	}
	return PromptDefinition{
		Version:   PromptDefinitionVersion,
		Name:      name,
		Arguments: arguments,
		Messages: []PromptMessage{{
			Role:    "user",
			Content: PromptTextContent{Type: "text", Text: text},
		}},
	}
}
