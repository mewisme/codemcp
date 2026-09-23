package application

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/instructionpolicy"
)

func TestInstructionSettingsServiceRoundTripAndDetectSources(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "CLAUDE.md"), []byte("USER CONTEXT"), 0644); err != nil {
		t.Fatal(err)
	}
	store := &instructionpolicy.Store{Path: filepath.Join(t.TempDir(), "global.json")}
	service := NewInstructionSettingsService(store)
	service.UserHomeDir = func() (string, error) { return home, nil }

	initial, err := service.Load()
	if err != nil {
		t.Fatal(err)
	}
	if initial.Version != instructionpolicy.Version || initial.Context != "" || len(initial.Rules) != 0 || len(initial.DetectedSources) != 1 {
		t.Fatalf("initial=%#v", initial)
	}

	contextValue := "MANAGED GLOBAL CONTEXT"
	rules := []instructionpolicy.GlobalRule{{ID: "rule_global", Name: "Global", Enabled: true, Content: "MANAGED GLOBAL RULE"}}
	disabled := false
	saved, err := service.Save(InstructionSettingsPatch{
		Context: &contextValue, Rules: &rules,
		SourcePolicy: map[string]instructionpolicy.SourcePolicy{".claude": {Context: &disabled}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Context != contextValue || len(saved.Rules) != 1 || saved.Rules[0].ID != "rule_global" {
		t.Fatalf("saved=%#v", saved)
	}
	if len(saved.DetectedSources) != 1 || saved.DetectedSources[0].Provider != "claude" || saved.DetectedSources[0].Enabled {
		t.Fatalf("sources=%#v", saved.DetectedSources)
	}
	if policy, ok := saved.SourcePolicy["claude"]; !ok || policy.Context == nil || *policy.Context {
		t.Fatalf("policy=%#v", saved.SourcePolicy)
	}

	reloaded, err := service.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Context != saved.Context || len(reloaded.Rules) != 1 || reloaded.Rules[0] != saved.Rules[0] {
		t.Fatalf("reloaded=%#v saved=%#v", reloaded, saved)
	}
}

func TestInstructionSettingsServiceMergesSourcePolicy(t *testing.T) {
	store := &instructionpolicy.Store{Path: filepath.Join(t.TempDir(), "global.json")}
	value := instructionpolicy.DefaultConfig()
	disabled := false
	value.Sources["cursor"] = instructionpolicy.SourcePolicy{Skills: &disabled}
	if err := store.Save(value); err != nil {
		t.Fatal(err)
	}
	service := NewInstructionSettingsService(store)
	service.UserHomeDir = func() (string, error) { return t.TempDir(), nil }
	enabled := true
	result, err := service.Save(InstructionSettingsPatch{SourcePolicy: map[string]instructionpolicy.SourcePolicy{"claude": {Context: &enabled}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.SourcePolicy["cursor"].Skills == nil || result.SourcePolicy["claude"].Context == nil {
		t.Fatalf("source policy was replaced instead of merged: %#v", result.SourcePolicy)
	}
}

func TestNewInstructionRuleID(t *testing.T) {
	first, err := NewInstructionRuleID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewInstructionRuleID()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first, "rule_") || len(first) != len("rule_")+32 || first == second {
		t.Fatalf("ids first=%q second=%q", first, second)
	}
}
