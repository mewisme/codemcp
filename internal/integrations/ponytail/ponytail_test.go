package ponytail

import (
	"strings"
	"testing"
)

func TestRequestedMode(t *testing.T) {
	cases := map[string]Mode{
		"/ponytail lite":                   Lite,
		"@ponytail full":                   Full,
		"$ponytail ultra":                  Ultra,
		"/ponytail-review":                 Review,
		"/ponytail:ponytail-review target": Review,
		"/ponytail off":                    Off,
		"stop ponytail":                    Off,
		"normal mode!":                     Off,
	}
	for prompt, expected := range cases {
		value, ok := RequestedMode(prompt)
		if !ok || value != expected {
			t.Fatalf("%q => %q %v, want %q", prompt, value, ok, expected)
		}
	}
	for _, prompt := range []string{"normal user prompt", "add a normal mode toggle", "explain how to stop ponytail later", "/ponytail", "/ponytail review"} {
		if value, ok := RequestedMode(prompt); ok {
			t.Fatalf("%q unexpectedly requested %q", prompt, value)
		}
	}
}

func TestNormalizeModes(t *testing.T) {
	for _, mode := range []Mode{Lite, Full, Ultra} {
		if value, ok := NormalizeRuntimeMode(" " + strings.ToUpper(string(mode)) + " "); !ok || value != mode {
			t.Fatalf("runtime mode %q => %q %v", mode, value, ok)
		}
	}
	for _, mode := range []Mode{Off, Lite, Full, Ultra, Review} {
		if value, ok := NormalizeMode(string(mode)); !ok || value != mode {
			t.Fatalf("mode %q => %q %v", mode, value, ok)
		}
	}
	if _, ok := NormalizeRuntimeMode("review"); ok {
		t.Fatal("review must remain session-only")
	}
}

func TestInstructionsFilterIntensity(t *testing.T) {
	for _, mode := range []Mode{Lite, Full, Ultra} {
		value := Instructions(mode)
		if !strings.HasPrefix(value, "PONYTAIL MODE ACTIVE — level: "+string(mode)) || !strings.Contains(value, "## The ladder") {
			t.Fatalf("%s instructions missing header/common rules", mode)
		}
		for _, candidate := range []Mode{Lite, Full, Ultra} {
			row := "| **" + string(candidate) + "** |"
			example := "- " + string(candidate) + ": \""
			if candidate == mode {
				if !strings.Contains(value, row) || !strings.Contains(value, example) {
					t.Fatalf("%s instructions missing own intensity content", mode)
				}
			} else if strings.Contains(value, row) || strings.Contains(value, example) {
				t.Fatalf("%s instructions retained %s-specific content", mode, candidate)
			}
		}
	}
	if Instructions(Off) != "" {
		t.Fatal("off instructions must be empty")
	}
}

func TestReviewInstructionsAreBuiltIn(t *testing.T) {
	value := Instructions(Review)
	for _, expected := range []string{"PONYTAIL MODE ACTIVE — level: review", "## Format", "`delete:`", "net: -<N> lines possible"} {
		if !strings.Contains(value, expected) {
			t.Fatalf("review instructions missing %q", expected)
		}
	}
}

func TestManagerTurnLifecycle(t *testing.T) {
	manager := NewManager(true, Ultra)
	result, err := manager.Turn("ws", "continue", "turn")
	if err != nil || !result.Available || !result.Active || result.Mode != Ultra || !strings.Contains(result.ActiveInstructions, "level: ultra") {
		t.Fatalf("first result=%#v err=%v", result, err)
	}
	result, err = manager.Turn("ws", "continue", "turn")
	if err != nil || result.ActiveInstructions != "" || result.RefreshHint == "" {
		t.Fatalf("second result=%#v err=%v", result, err)
	}
	result, err = manager.Turn("ws", "/ponytail lite", "turn")
	if err != nil || result.Mode != Lite || !strings.Contains(result.ActiveInstructions, "level: lite") {
		t.Fatalf("lite result=%#v err=%v", result, err)
	}
	result, err = manager.Turn("ws", "/ponytail-review src", "turn")
	if err != nil || result.Mode != Review || !strings.Contains(result.ActiveInstructions, "## Format") {
		t.Fatalf("review result=%#v err=%v", result, err)
	}
	result, err = manager.Turn("ws", "stop ponytail", "turn")
	if err != nil || result.Mode != Off || result.Active || result.ActiveInstructions != "" {
		t.Fatalf("off result=%#v err=%v", result, err)
	}
}

func TestManagerUsesConfiguredDefaultsAndResetsWorkspaceState(t *testing.T) {
	manager := NewManager(false, Ultra)
	inactive, err := manager.Turn("ws", "continue", "turn")
	if err != nil || inactive.Active || inactive.Mode != Off {
		t.Fatalf("inactive=%#v err=%v", inactive, err)
	}
	manager.SetDefaults(true, Lite)
	active, err := manager.Turn("ws", "continue", "turn")
	if err != nil || !active.Active || active.Mode != Lite || !strings.Contains(active.ActiveInstructions, "level: lite") {
		t.Fatalf("active=%#v err=%v", active, err)
	}
}

func TestManagerValidation(t *testing.T) {
	manager := NewManager(true, Full)
	if _, err := manager.Turn("", "x", "turn"); err == nil {
		t.Fatal("empty workspace accepted")
	}
	if _, err := manager.Turn("ws", "", "turn"); err == nil {
		t.Fatal("empty prompt accepted")
	}
	if _, err := manager.Turn("ws", "x", "bad"); err == nil {
		t.Fatal("invalid action accepted")
	}
}
