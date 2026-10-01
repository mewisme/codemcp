package telegram

import (
	"fmt"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/projectcontext"
	"go.mewis.me/codemcp/internal/rules"
	"go.mewis.me/codemcp/internal/skills"
	"go.mewis.me/codemcp/internal/tools"
)

func TestSystemScreenActivatesCanonicalAdministration(t *testing.T) {
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{
		capability.VersionAbout: application.AboutInfo{Version: "v1.2.3", Commit: "abcdef", RuntimeRunning: true},
	}}
	ui, owner := newDomainTestInterface(t, dispatcher)
	screen, err := ui.systemScreen(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	labels := keyboardLabels(screen.Keyboard)
	for _, want := range []string{"Doctor", "Tools", "Check update", "Restart", "Stop"} {
		if !strings.Contains(labels, want) {
			t.Fatalf("system control %q missing: %s", want, labels)
		}
	}
}

func TestToolInventoryUsesVisiblePaginatorInsteadOfRichListTruncation(t *testing.T) {
	ui, owner := newDomainTestInterface(t, &domainTestDispatcher{})
	schemas := make([]tools.Schema, 76)
	for index := range schemas {
		schemas[index] = tools.Schema{Name: fmt.Sprintf("tool_%02d", index+1), Title: fmt.Sprintf("Tool %02d", index+1)}
	}
	state := ActionState{Route: RouteOperation, Back: RouteSystem, Operation: capability.ToolInventoryRead}
	screen, handled, err := ui.systemOperationResultScreen(t.Context(), owner, state, schemas)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || screen.Rich == nil {
		t.Fatalf("tool inventory handled=%t rich=%#v", handled, screen.Rich)
	}
	fallback := RichFallback(screen.Rich)
	if !strings.Contains(fallback.Text, "76 tool(s) · Page 1/7") {
		t.Fatalf("tool inventory heading=%q", fallback.Text)
	}
	for _, expected := range []string{"tool_01", "tool_12"} {
		if !strings.Contains(fallback.Text, expected) {
			t.Fatalf("tool inventory first page missing %q: %q", expected, fallback.Text)
		}
	}
	if strings.Contains(fallback.Text, "tool_13") {
		t.Fatalf("tool inventory leaked next-page item: %q", fallback.Text)
	}
	if len(screen.Keyboard) != 2 || len(screen.Keyboard[0]) != 7 {
		t.Fatalf("tool inventory paginator=%#v", screen.Keyboard)
	}
	if screen.Keyboard[0][0].Text != "( 1 )" || !screen.Keyboard[0][0].Disabled || screen.Keyboard[0][6].Text != "7" {
		t.Fatalf("tool inventory page row=%#v", screen.Keyboard[0])
	}
	if got := keyboardLabels([][]Button{screen.Keyboard[1]}); !strings.Contains(got, "Back") || !strings.Contains(got, "Home") || !strings.Contains(got, "Refresh") {
		t.Fatalf("tool inventory navigation=%s", got)
	}
}

func TestPaginatorWindowKeepsFirstLastAndFiveNearbyPages(t *testing.T) {
	pages := PaginatorPages(6, 13)
	want := []int{0, 4, 5, 6, 7, 8, 12}
	if len(pages) != len(want) {
		t.Fatalf("pages=%v want=%v", pages, want)
	}
	for index := range want {
		if pages[index] != want[index] {
			t.Fatalf("pages=%v want=%v", pages, want)
		}
	}
}

func TestInstructionsExposeProviderSourcesReadOnly(t *testing.T) {
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{
		capability.InstructionSettingsRead: application.InstructionSettings{
			Version:         1,
			DetectedSources: []instructioncontext.SourceSnapshot{{Provider: "claude", Kind: "rules", Scope: "user", Count: 3, Enabled: true, Loaded: true}},
		},
	}}
	ui, owner := newDomainTestInterface(t, dispatcher)
	screen, err := ui.instructionsScreen(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	text := RichFallback(screen.Rich).Text
	if !strings.Contains(text, "claude/rules") || !strings.Contains(text, "read-only here") {
		t.Fatalf("instruction provenance missing: %q", text)
	}
	labels := keyboardLabels(screen.Keyboard)
	if !strings.Contains(labels, "Author rule") || !strings.Contains(labels, "Author skill") {
		t.Fatalf("unsafe authoring is not explicitly unavailable: %s", labels)
	}
}

func TestProjectContextPresentationDoesNotRenderRuleContent(t *testing.T) {
	ui, owner := newDomainTestInterface(t, &domainTestDispatcher{})
	const sentinel = "DO_NOT_RENDER_RULE_BODY"
	result := projectcontext.Result{
		WorkspaceID: "ws_test", Root: "/workspace",
		Summary: projectcontext.Summary{Rules: 1, Skills: 1, InstructionBytes: 50},
		InstructionContext: instructioncontext.InstructionContext{
			Rules:                  []rules.Rule{{Path: "/workspace/.cm/rules/test.md", Source: "native", Content: sentinel}},
			Skills:                 []skills.Skill{{Name: "test-skill", Source: "native", Path: "/workspace/.cm/skills/test-skill"}},
			IntegrationDiagnostics: []instructioncontext.IntegrationDiagnostic{{ID: "codegraph", State: "ready", Message: "available"}},
		},
	}
	screen, handled, err := ui.systemOperationResultScreen(t.Context(), owner, ActionState{Back: RouteInstructions}, result)
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	text := RichFallback(screen.Rich).Text
	if strings.Contains(text, sentinel) {
		t.Fatalf("rule body leaked into Telegram project-context presentation: %q", text)
	}
	for _, want := range []string{"test.md", "test-skill", "codegraph"} {
		if !strings.Contains(text, want) {
			t.Fatalf("project-context inventory missing %q: %q", want, text)
		}
	}
}

func TestPromptUpdateInputFlowKeepsBoundNameAndScope(t *testing.T) {
	state := ActionState{InputKind: inputPromptUpdate, ResourceID: "selected", ExpectedVersion: "ws_test"}
	prompt := instructioncontext.ScopedPrompt{
		Scope: instructioncontext.PromptScopeWorkspace,
		Definition: instructioncontext.PromptDefinition{
			Version: instructioncontext.PromptDefinitionVersion,
			Name:    "selected",
			Messages: []instructioncontext.PromptMessage{{
				Role: "user", Content: instructioncontext.PromptTextContent{Type: "text", Text: "hello"},
			}},
		},
	}
	descriptor := promptUpdateInputFlow(state, prompt)
	flow := newInputFlowState(descriptor)
	value, err := descriptor.Build(inputFlowDataFor(descriptor, flow))
	if err != nil {
		t.Fatal(err)
	}
	request := value.(application.PromptWriteRequest)
	if request.Definition.Name != "selected" || request.Scope != instructioncontext.PromptScopeWorkspace || request.WorkspaceID != "ws_test" {
		t.Fatalf("prompt update retargeted immutable identity: %#v", request)
	}
}
