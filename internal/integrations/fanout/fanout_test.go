package fanout

import (
	"strings"
	"sync"
	"testing"
)

func TestNormalizeModes(t *testing.T) {
	for _, mode := range []Mode{Auto, Conservative, Aggressive} {
		if value, ok := NormalizeRuntimeMode(" " + strings.ToUpper(string(mode)) + " "); !ok || value != mode {
			t.Fatalf("runtime mode %q => %q %v", mode, value, ok)
		}
		if value, ok := NormalizeMode(string(mode)); !ok || value != mode {
			t.Fatalf("mode %q => %q %v", mode, value, ok)
		}
	}
	if value, ok := NormalizeMode(" OFF "); !ok || value != Off {
		t.Fatalf("off => %q %v", value, ok)
	}
	for _, invalid := range []string{"", "full", "parallel", "max"} {
		if _, ok := NormalizeRuntimeMode(invalid); ok {
			t.Fatalf("runtime mode %q accepted", invalid)
		}
	}
	if _, ok := NormalizeRuntimeMode("off"); ok {
		t.Fatal("off must remain transient runtime state")
	}
}

func TestManagerIsolatesControllerAndWorkspaceState(t *testing.T) {
	manager := NewManager(true, Auto)
	first, err := manager.Turn("session-a", "ws-a", "/fanout aggressive", "turn")
	if err != nil || first.Mode != Aggressive || !first.Active {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	otherSession, err := manager.Turn("session-b", "ws-a", "continue", "turn")
	if err != nil || otherSession.Mode != Auto {
		t.Fatalf("other session=%#v err=%v", otherSession, err)
	}
	otherWorkspace, err := manager.Turn("session-a", "ws-b", "/fanout conservative", "turn")
	if err != nil || otherWorkspace.Mode != Conservative {
		t.Fatalf("other workspace=%#v err=%v", otherWorkspace, err)
	}
	firstAgain, err := manager.Turn("session-a", "ws-a", "continue", "status")
	if err != nil || firstAgain.Mode != Aggressive {
		t.Fatalf("first again=%#v err=%v", firstAgain, err)
	}
}

func TestManagerLifecycleDefaultsAndReload(t *testing.T) {
	manager := NewManager(false, Aggressive)
	inactive, err := manager.Turn("session", "ws", "continue", "turn")
	if err != nil || inactive.Active || inactive.Mode != Off || inactive.ActiveInstructions != "" {
		t.Fatalf("inactive=%#v err=%v", inactive, err)
	}
	manager.SetDefaults(true, Conservative)
	active, err := manager.Turn("session", "ws", "continue", "turn")
	if err != nil || !active.Active || active.Mode != Conservative || active.ActiveInstructions == "" {
		t.Fatalf("active=%#v err=%v", active, err)
	}
	status, err := manager.Turn("session", "ws", "continue", "status")
	if err != nil || status.ActiveInstructions != "" || status.RefreshHint == "" {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	refresh, err := manager.Turn("session", "ws", "continue", "refresh")
	if err != nil || refresh.ActiveInstructions == "" || refresh.RefreshHint != "" {
		t.Fatalf("refresh=%#v err=%v", refresh, err)
	}
}

func TestRequestedModeIsDeterministic(t *testing.T) {
	for _, test := range []struct {
		prompt string
		want   Mode
		found  bool
	}{
		{"/fanout", Auto, true},
		{"/fanout conservative", Conservative, true},
		{"please /fanout aggressive now", Aggressive, true},
		{"/fanout off", Off, true},
		{"continue", "", false},
	} {
		got, found, err := RequestedMode(test.prompt, Auto)
		if err != nil || got != test.want || found != test.found {
			t.Fatalf("%q => %q %t err=%v", test.prompt, got, found, err)
		}
	}
	if _, _, err := RequestedMode("/fanout conservative then /fanout aggressive", Auto); err == nil {
		t.Fatal("conflicting selections accepted")
	}
	if mode, found, err := RequestedMode("/fanout aggressive /plan implement it", Auto); err != nil || found || mode != "" {
		t.Fatalf("plan did not dominate Fanout: mode=%q found=%t err=%v", mode, found, err)
	}
	if mode, found, err := RequestedMode("/fanout AGGRESSIVE", Conservative); err != nil || !found || mode != Conservative {
		t.Fatalf("invalid explicit case must preserve configured default: mode=%q found=%t err=%v", mode, found, err)
	}
}

func TestManagerRestartClearsTransientModeToConfiguredDefault(t *testing.T) {
	manager := NewManager(true, Conservative)
	changed, err := manager.Turn("session", "ws", "/fanout aggressive", "turn")
	if err != nil || changed.Mode != Aggressive {
		t.Fatalf("changed=%#v err=%v", changed, err)
	}
	restarted := NewManager(true, Conservative)
	value, err := restarted.Turn("session", "ws", "continue", "turn")
	if err != nil || value.Mode != Conservative {
		t.Fatalf("restarted=%#v err=%v", value, err)
	}
}

func TestManagerConcurrentSessionsRemainIsolated(t *testing.T) {
	manager := NewManager(true, Auto)
	type target struct {
		session string
		mode    Mode
	}
	targets := []target{{session: "session-aggressive", mode: Aggressive}, {session: "session-conservative", mode: Conservative}}
	var wg sync.WaitGroup
	for _, target := range targets {
		target := target
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if _, err := manager.Turn(target.session, "ws-shared", "/fanout "+string(target.mode), "turn"); err != nil {
					t.Errorf("%s turn: %v", target.session, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	for _, target := range targets {
		value, err := manager.Turn(target.session, "ws-shared", "continue", "status")
		if err != nil || value.Mode != target.mode {
			t.Fatalf("%s mode=%q want=%q err=%v", target.session, value.Mode, target.mode, err)
		}
	}
}

func TestInstructionsCoverCanonicalFanoutWorkflowScenarios(t *testing.T) {
	instructions := Instructions(Auto)
	for scenario, clauses := range map[string][]string{
		"read-only audit":         {"Prefer read-only fanout for broad audits and research."},
		"disjoint implementation": {"Parallel mutation requires explicit disjoint ownership."},
		"dependent work":          {"immediate sequential dependencies"},
		"conflicting mutation":    {"Never let workers race on the same files or shared mutable state."},
		"aggregation":             {"Aggregate child results at the parent.", "Deduplicate overlapping findings", "resolve contradictions", "verify material conclusions"},
		"idle follow-up":          {"Use `agent_send` only for useful follow-up to a live idle child."},
		"cancellation":            {"Use `agent_cancel` when child output is no longer needed."},
		"capacity rejection":      {"Current runtime/backend readiness and capacity remain authoritative.", "Never invent or raise concurrency limits."},
	} {
		for _, clause := range clauses {
			if !strings.Contains(instructions, clause) {
				t.Fatalf("%s policy missing %q", scenario, clause)
			}
		}
	}
}

func TestInstructionsProjectModeSpecificStrategy(t *testing.T) {
	tests := []struct {
		mode Mode
		want string
	}{
		{Auto, "Use balanced delegation."},
		{Conservative, "Use a high delegation threshold."},
		{Aggressive, "Proactively decompose substantial work"},
	}
	for _, test := range tests {
		value := Instructions(test.mode)
		if !strings.HasPrefix(value, "FANOUT MODE ACTIVE — level: "+string(test.mode)) || !strings.Contains(value, test.want) || !strings.Contains(value, "# Fanout") {
			t.Fatalf("%s instructions missing expected content: %q", test.mode, value)
		}
		for _, forbidden := range []string{"agent.max_parallel =", "max_depth =", "claim grants", "claim bypass"} {
			if strings.Contains(value, forbidden) {
				t.Fatalf("%s instructions redefine authority with %q", test.mode, forbidden)
			}
		}
	}
	if Instructions(Off) != "" {
		t.Fatal("off instructions must be empty")
	}

	canonical := Instructions(Auto)
	for _, expected := range []string{
		"positive payoff",
		"read-only fanout",
		"explicit disjoint ownership",
		"duplicate workers",
		"parent-only goal",
		"Exact workspace binding",
		"`agent_claim`",
		"`project_context` with memory enabled",
		"bounded `agent_wait`",
		"`agent_send` only",
		"`agent_cancel`",
		"Depth is one",
		"cannot use `agent_spawn`",
		"Aggregate child results at the parent",
		"Deduplicate overlapping findings",
		"resolve contradictions",
		"verify material conclusions",
		"Tool availability never means delegation is required",
	} {
		if !strings.Contains(canonical, expected) {
			t.Fatalf("canonical Fanout policy missing %q: %s", expected, canonical)
		}
	}
}
