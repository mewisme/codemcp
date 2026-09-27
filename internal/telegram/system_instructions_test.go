package telegram

import (
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/projectcontext"
	"go.mewis.me/codemcp/internal/rules"
	"go.mewis.me/codemcp/internal/skills"
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
	if !strings.Contains(text, "claude/rules") || !strings.Contains(text, "read-only provenance") {
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

func TestPromptUpdateInputCannotRetargetName(t *testing.T) {
	state := ActionState{InputKind: inputPromptUpdate, ResourceID: "selected", ExpectedVersion: "ws_test"}
	_, handled, err := systemActionInput(state, `{"version":1,"name":"other","messages":[{"role":"user","content":{"type":"text","text":"hello"}}]}`)
	if !handled || err == nil || !strings.Contains(err.Error(), "cannot change") {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
}
