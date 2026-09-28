package shell

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPublicExecutionProjectionPreservesSafeIdentityAndDropsPrivateFields(t *testing.T) {
	const secret = "projection-secret-marker"
	raw := ExecutionInfo{
		ID: "exec_1", WorkspaceID: "ws_1", Tool: "run_command",
		Command:          `curl -H "Authorization: Bearer ` + secret + `" example.test`,
		RequestedCommand: "curl --token=" + secret,
		EffectiveCommand: "curl --api-key=" + secret,
		SecurityCommand:  secret,
		CWD:              "/workspace", Shell: "sh", Source: "tunnel",
		CallID: "call_private", SessionHash: secret,
		ReceivedByInstanceID: "receiver_private", ExecutedByInstanceID: "executor_private",
		StartedAt: "2026-09-28T08:00:00Z", FinishedAt: "2026-09-28T08:00:01Z",
		Status: ExecutionStatusSuccess,
	}

	public := PublicExecutionInfo(raw)
	data, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(data)
	for _, forbidden := range []string{secret, "security_command", "call_id", "session_hash", "received_by_instance_id", "executed_by_instance_id"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("public execution leaked %q: %s", forbidden, encoded)
		}
	}
	for _, want := range []string{"exec_1", "ws_1", "run_command", ExecutionStatusSuccess, "2026-09-28T08:00:00Z", "2026-09-28T08:00:01Z"} {
		if !strings.Contains(encoded, want) {
			t.Fatalf("public execution lost %q: %s", want, encoded)
		}
	}
	if raw.SecurityCommand == "" || raw.SessionHash == "" || raw.ReceivedByInstanceID == "" || raw.ExecutedByInstanceID == "" {
		t.Fatalf("public projection mutated runtime truth: %#v", raw)
	}
}

func TestPublicExecutionSnapshotSanitizesOutput(t *testing.T) {
	const secret = "output-secret-marker"
	public := PublicExecutionSnapshot(ExecutionSnapshot{
		Execution:      ExecutionInfo{ID: "exec_1", WorkspaceID: "ws_1", Tool: "run_command", Command: "echo ok", Status: ExecutionStatusSuccess},
		Stdout:         "Authorization: Bearer " + secret,
		Stderr:         "token=" + secret,
		LatestSequence: 7,
	})
	data, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || public.LatestSequence != 7 || public.Execution.ID != "exec_1" {
		t.Fatalf("public execution snapshot=%s", data)
	}
}
