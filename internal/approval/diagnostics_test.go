package approval

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDiagnosticsDoesNotPurgeOrExposeApprovalPayloads(t *testing.T) {
	manager, now := testManager()
	challenge, _, err := manager.CreateChallenge(testChallenge("raw-secret-session", "ws_x", "cm update --token raw-secret-value"))
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequest(challenge.ID, "raw-secret-session", "ws_x")
	if err != nil {
		t.Fatal(err)
	}
	*now = request.ExpiresAt.Add(time.Second)

	beforeChallenges := len(manager.challenges)
	beforeRequests := len(manager.requests)
	diagnostics := manager.Diagnostics()
	if !diagnostics.Available || diagnostics.Challenges != 1 || diagnostics.Requests != 1 ||
		diagnostics.Pending != 1 || diagnostics.Stale != 1 {
		t.Fatalf("diagnostics=%#v", diagnostics)
	}
	if len(manager.challenges) != beforeChallenges || len(manager.requests) != beforeRequests {
		t.Fatalf("diagnostics mutated manager: challenges=%d/%d requests=%d/%d", len(manager.challenges), beforeChallenges, len(manager.requests), beforeRequests)
	}
	if record := manager.requests[request.ID]; record == nil || record.value.Status != StatusPending {
		t.Fatalf("expired pending request was purged or mutated: %#v", record)
	}
	data, err := json.Marshal(diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"raw-secret-value", "raw-secret-session", "cm update"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("approval diagnostics leaked payload %q: %s", forbidden, data)
		}
	}
}
