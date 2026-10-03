package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestManagedAgentIDIsOpaqueAndDistinctFromCompletionAgentID(t *testing.T) {
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateID(id); err != nil {
		t.Fatalf("generated id %q is invalid: %v", id, err)
	}
	if !strings.HasPrefix(string(id), "agent_") || len(id) != len("agent_")+16 {
		t.Fatalf("unexpected managed agent id %q", id)
	}
	for _, invalid := range []ID{"", "0123456789abcdef", "agent_0123", "agent_0123456789ABCDEf"} {
		if ValidateID(invalid) == nil {
			t.Fatalf("invalid id accepted: %q", invalid)
		}
	}
}

func TestStateTransitionsRejectBackwardAndTerminalChanges(t *testing.T) {
	valid := [][2]State{
		{StateStarting, StateWorking},
		{StateWorking, StateIdle},
		{StateIdle, StateWorking},
		{StateWorking, StateCompletionPending},
		{StateCompletionPending, StateCompleted},
		{StateCompleted, StateCompleted},
	}
	for _, transition := range valid {
		if err := ValidateTransition(transition[0], transition[1]); err != nil {
			t.Fatalf("valid transition %q -> %q rejected: %v", transition[0], transition[1], err)
		}
	}
	invalid := [][2]State{
		{StateIdle, StateStarting},
		{StateCompleted, StateWorking},
		{StateFailed, StateCancelled},
		{StateWorking, StateCompleted},
		{"unknown", StateWorking},
	}
	for _, transition := range invalid {
		if err := ValidateTransition(transition[0], transition[1]); err == nil {
			t.Fatalf("invalid transition accepted: %q -> %q", transition[0], transition[1])
		}
	}
	for _, terminal := range []State{StateCompleted, StatePartial, StateBlocked, StateCancelled, StateFailed, StateExpired} {
		if !terminal.Terminal() {
			t.Fatalf("state %q should be terminal", terminal)
		}
	}
}

func TestNormalizeSpawnInputBoundsAndCanonicalizesOnlyMetadata(t *testing.T) {
	input, err := NormalizeSpawnInput(SpawnInput{
		WorkspaceID:     " ws_test ",
		Prompt:          "  preserve task whitespace  ",
		Backend:         " ChatGPT-Web ",
		Model:           " model-x ",
		ReasoningEffort: " high ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if input.WorkspaceID != "ws_test" || input.Backend != "chatgpt-web" || input.Model != "model-x" || input.ReasoningEffort != "high" {
		t.Fatalf("normalized input=%#v", input)
	}
	if input.Prompt != "  preserve task whitespace  " {
		t.Fatalf("prompt was rewritten: %q", input.Prompt)
	}
	for _, value := range []SpawnInput{
		{WorkspaceID: "", Prompt: "task"},
		{WorkspaceID: "wsc_container", Prompt: "task"},
		{WorkspaceID: "ws_test", Prompt: "   "},
		{WorkspaceID: "ws_test", Prompt: strings.Repeat("x", MaxPromptBytes+1)},
		{WorkspaceID: "ws_test", Prompt: "task", Model: strings.Repeat("m", MaxModelBytes+1)},
	} {
		if _, err := NormalizeSpawnInput(value); err == nil {
			t.Fatalf("invalid spawn input accepted: %#v", value)
		}
	}
}

func TestSnapshotBoundsResultAndOmitsOwner(t *testing.T) {
	owner, err := NewMCPController("session-secret")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := Record{
		ID: "agent_0123456789abcdef", Backend: "chatgpt-web", WorkspaceID: "ws_test",
		Depth: 1, State: StateIdle, CreatedAt: now, UpdatedAt: now, Turn: 1,
		Result: strings.Repeat("界", MaxResultBytes), Error: strings.Repeat("e", MaxErrorBytes+20),
		Owner: owner,
	}
	snapshot := record.Snapshot()
	if len(snapshot.Result) > MaxResultBytes || len(snapshot.Error) > MaxErrorBytes || !utf8.ValidString(snapshot.Result) {
		t.Fatalf("snapshot bounds invalid: result=%d error=%d valid=%v", len(snapshot.Result), len(snapshot.Error), utf8.ValidString(snapshot.Result))
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "session-secret") {
		t.Fatalf("trusted owner leaked through JSON: %s", data)
	}
}

func TestInitialDelegationDepthRejectsManagedGrandchildren(t *testing.T) {
	if err := ValidateDelegation("", 1); err != nil {
		t.Fatalf("root child rejected: %v", err)
	}
	if err := ValidateDelegation("agent_0123456789abcdef", 2); err == nil {
		t.Fatal("managed grandchild depth unexpectedly accepted")
	}
	if err := ValidateDelegation("agent_0123456789abcdef", 1); err == nil {
		t.Fatal("managed parent at root depth unexpectedly accepted")
	}
}
