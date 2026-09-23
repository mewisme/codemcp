package approval

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRequestAndChallengeJSONHidePrivateIdentity(t *testing.T) {
	manager, _ := testManager()
	input := testChallenge("raw-secret-session", "ws_x", "cm update")
	input.SessionHash = "session-fingerprint"
	challenge, _, err := manager.CreateChallenge(input)
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequest(challenge.ID, "raw-secret-session", "ws_x")
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]any{"challenge": challenge, "request": request} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if strings.Contains(text, "raw-secret-session") || strings.Contains(text, request.Digest) || strings.Contains(text, challenge.Digest) {
			t.Fatalf("%s leaked private identity: %s", name, text)
		}
		if !strings.Contains(text, `"arguments":{"command":"cm update","workspace_id":"ws_x"}`) {
			t.Fatalf("%s missing structured arguments: %s", name, text)
		}
	}
}
