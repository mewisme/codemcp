package approval

import (
	"math"
	"testing"

	"go.mewis.me/codemcp/internal/controlguard"
)

func TestCanonicalTargetDigestIsStableAndExact(t *testing.T) {
	base := Target{SessionID: "session-a", WorkspaceID: "ws_x", Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"workspace_id": "ws_x", "command": "cm update"}, GuardCode: controlguard.CodeControlPlaneMutation}
	first, firstArgs, err := CanonicalTargetDigest("instance-a", base)
	if err != nil {
		t.Fatal(err)
	}
	reordered := base
	reordered.Arguments = map[string]any{"command": "cm update", "workspace_id": "ws_x"}
	second, secondArgs, err := CanonicalTargetDigest("instance-a", reordered)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || string(firstArgs) != string(secondArgs) {
		t.Fatalf("canonical digest changed with map order: %s/%s %s/%s", first, second, firstArgs, secondArgs)
	}
	variants := []Target{
		{SessionID: "session-b", WorkspaceID: base.WorkspaceID, Source: base.Source, TargetTool: base.TargetTool, Arguments: base.Arguments, GuardCode: base.GuardCode},
		{SessionID: base.SessionID, WorkspaceID: "ws_y", Source: base.Source, TargetTool: base.TargetTool, Arguments: base.Arguments, GuardCode: base.GuardCode},
		{SessionID: base.SessionID, WorkspaceID: base.WorkspaceID, Source: "direct", TargetTool: base.TargetTool, Arguments: base.Arguments, GuardCode: base.GuardCode},
		{SessionID: base.SessionID, WorkspaceID: base.WorkspaceID, Source: base.Source, TargetTool: "start_process", Arguments: base.Arguments, GuardCode: base.GuardCode},
		{SessionID: base.SessionID, WorkspaceID: base.WorkspaceID, Source: base.Source, TargetTool: base.TargetTool, Arguments: map[string]any{"workspace_id": "ws_x", "command": "cm update "}, GuardCode: base.GuardCode},
		{SessionID: base.SessionID, WorkspaceID: base.WorkspaceID, Source: base.Source, TargetTool: base.TargetTool, Arguments: map[string]any{"workspace_id": "ws_x", "command": "cm update", "force": false}, GuardCode: base.GuardCode},
		{SessionID: base.SessionID, WorkspaceID: base.WorkspaceID, Source: base.Source, TargetTool: base.TargetTool, Arguments: base.Arguments, GuardCode: controlguard.CodeProtectedState},
	}
	for index, variant := range variants {
		digest, _, err := CanonicalTargetDigest("instance-a", variant)
		if err != nil {
			t.Fatalf("variant %d: %v", index, err)
		}
		if digest == first {
			t.Fatalf("variant %d unexpectedly matched base digest", index)
		}
	}
	otherInstance, _, err := CanonicalTargetDigest("instance-b", base)
	if err != nil || otherInstance == first {
		t.Fatalf("instance binding = %s err=%v", otherInstance, err)
	}
}

func TestCanonicalTargetDigestValidatesIdentityAndArguments(t *testing.T) {
	valid := Target{SessionID: "session", WorkspaceID: "ws", TargetTool: "run_command", Arguments: map[string]any{}, GuardCode: controlguard.CodeControlPlaneMutation}
	for name, mutate := range map[string]func(*Target){
		"session":   func(value *Target) { value.SessionID = "" },
		"workspace": func(value *Target) { value.WorkspaceID = "" },
		"tool":      func(value *Target) { value.TargetTool = "" },
		"guard":     func(value *Target) { value.GuardCode = "" },
	} {
		t.Run(name, func(t *testing.T) {
			value := valid
			mutate(&value)
			if _, _, err := CanonicalTargetDigest("instance", value); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	if _, _, err := CanonicalTargetDigest("", valid); err == nil {
		t.Fatal("missing instance id was accepted")
	}
	valid.Arguments = map[string]any{"invalid": math.NaN()}
	if _, _, err := CanonicalTargetDigest("instance", valid); err == nil {
		t.Fatal("non-JSON arguments were accepted")
	}
}
