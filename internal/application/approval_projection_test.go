package application

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/approval"
	mcpconfigwire "go.mewis.me/codemcp/internal/mcpconfig/wire"
)

func TestApprovalReviewProjectionIsEphemeralAndStateDerived(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	pending := approval.Request{ID: "req_pending", Status: approval.StatusPending, ExpiresAt: now.Add(time.Minute)}
	projection := ProjectApprovalReview(pending, now)
	if !projection.Ephemeral || !projection.Approve || !projection.Deny || !projection.Actionable() {
		t.Fatalf("pending projection=%#v", projection)
	}

	resolved := pending
	resolved.Status = approval.StatusApproved
	projection = ProjectApprovalReview(resolved, now)
	if !projection.Ephemeral || projection.Approve || projection.Deny || projection.Actionable() {
		t.Fatalf("resolved projection=%#v", projection)
	}

	expired := pending
	expired.ExpiresAt = now
	projection = ProjectApprovalReview(expired, now)
	if projection.Approve || projection.Deny || projection.Actionable() {
		t.Fatalf("expired projection=%#v", projection)
	}
}

func TestApprovalReviewProjectionRedactsConfigSetValues(t *testing.T) {
	request := approval.Request{
		ID: "req_config", Status: approval.StatusPending, WorkspaceID: "ws_scope",
		TargetTool: mcpconfigwire.SetToolName,
		Arguments:  json.RawMessage(`{"changes":[{"key":"server.port","value":"4000"},{"key":"permissions.allow_dirs","value":"/private/value"}]}`),
		Command:    "must-not-be-public",
		ExpiresAt:  time.Now().Add(time.Minute),
	}
	projection := ProjectApprovalReview(request, time.Now())
	data, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{"4000", "/private/value", "must-not-be-public"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("approval review leaked %q: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, "server.port") || !strings.Contains(text, "change_count") {
		t.Fatalf("approval review lost safe summary: %s", text)
	}
}
