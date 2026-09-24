package caveman

import (
	"strings"
	"testing"
)

func TestRequestedMode(t *testing.T) {
	cases := map[string]Mode{
		"/caveman":                          Full,
		"/caveman lite":                     Lite,
		"@caveman ultra":                    Ultra,
		"$caveman wenyan":                   WenyanFull,
		"/caveman wenyan-lite":              WenyanLite,
		"/caveman wenyan-full":              WenyanFull,
		"/caveman wenyan-ultra":             WenyanUltra,
		"/caveman \"ultra\"":                Ultra,
		"/caveman off":                      Off,
		"stop caveman":                      Off,
		"please switch back to normal mode": Off,
		"turn on caveman mode":              Full,
		"talk like caveman":                 Full,
		"be brief":                          Full,
	}
	for prompt, expected := range cases {
		value, ok := RequestedMode(prompt, Full)
		if !ok || value != expected {
			t.Fatalf("%q => %q %v, want %q", prompt, value, ok, expected)
		}
	}
	if value, ok := RequestedMode("/caveman", WenyanUltra); !ok || value != WenyanUltra {
		t.Fatalf("configured default => %q %v", value, ok)
	}
}

func TestRequestedModeAvoidsFalseTriggers(t *testing.T) {
	for _, prompt := range []string{
		"normal user prompt",
		"how do I exit vim normal mode?",
		"what is caveman mode?",
		"does caveman lite drop articles?",
		"the docs say \"stop caveman\" to disable it",
		"be brief in the summary",
		"/other stop caveman",
		"/caveman review",
		"/caveman ?",
	} {
		if value, ok := RequestedMode(prompt, Full); ok {
			t.Fatalf("%q unexpectedly requested %q", prompt, value)
		}
	}
}

func TestNormalizeModes(t *testing.T) {
	for _, mode := range []Mode{Lite, Full, Ultra, WenyanLite, WenyanFull, WenyanUltra} {
		if value, ok := NormalizeRuntimeMode(" " + strings.ToUpper(string(mode)) + " "); !ok || value != mode {
			t.Fatalf("runtime mode %q => %q %v", mode, value, ok)
		}
	}
	for _, mode := range []Mode{Off, Lite, Full, Ultra, WenyanLite, WenyanFull, WenyanUltra} {
		if value, ok := NormalizeMode(string(mode)); !ok || value != mode {
			t.Fatalf("mode %q => %q %v", mode, value, ok)
		}
	}
	if value, ok := NormalizeMode("wenyan"); !ok || value != WenyanFull {
		t.Fatalf("wenyan alias => %q %v", value, ok)
	}
	if _, ok := NormalizeRuntimeMode("wenyan"); ok {
		t.Fatal("wenyan alias must not be persisted as configured runtime mode")
	}
}

func TestInstructionsFilterIntensity(t *testing.T) {
	modes := []Mode{Lite, Full, Ultra, WenyanLite, WenyanFull, WenyanUltra}
	for _, mode := range modes {
		value := Instructions(mode)
		if !strings.HasPrefix(value, "CAVEMAN MODE ACTIVE — level: "+string(mode)) || !strings.Contains(value, "## Rules") || !strings.Contains(value, "## Auto-Clarity") {
			t.Fatalf("%s instructions missing header/common rules", mode)
		}
		for _, candidate := range modes {
			row := "| **" + string(candidate) + "** |"
			example := "- " + string(candidate) + ": \""
			if candidate == mode {
				if !strings.Contains(value, row) {
					t.Fatalf("%s instructions missing own intensity row", mode)
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
	result, err = manager.Turn("ws", "/caveman wenyan-full", "turn")
	if err != nil || result.Mode != WenyanFull || !strings.Contains(result.ActiveInstructions, "level: wenyan-full") {
		t.Fatalf("wenyan result=%#v err=%v", result, err)
	}
	result, err = manager.Turn("ws", "stop caveman", "turn")
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
	activated, err := manager.Turn("ws", "/caveman", "turn")
	if err != nil || !activated.Active || activated.Mode != Ultra {
		t.Fatalf("explicit activate=%#v err=%v", activated, err)
	}
	manager.SetDefaults(true, WenyanLite)
	active, err := manager.Turn("ws", "continue", "turn")
	if err != nil || !active.Active || active.Mode != WenyanLite || !strings.Contains(active.ActiveInstructions, "level: wenyan-lite") {
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
