package application

import (
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/approval"
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
