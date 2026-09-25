package application

import (
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/approval"
)

type ApprovalReviewProjection struct {
	Request   approval.Request
	Ephemeral bool
	Approve   bool
	Deny      bool
}

func ProjectApprovalReview(request approval.Request, now time.Time) ApprovalReviewProjection {
	if now.IsZero() {
		now = time.Now()
	}
	active := request.Status == approval.StatusPending && (request.ExpiresAt.IsZero() || now.Before(request.ExpiresAt))
	return ApprovalReviewProjection{
		Request:   approval.PublicRequest(request),
		Ephemeral: true,
		Approve:   active,
		Deny:      active,
	}
}

func (projection ApprovalReviewProjection) Actionable() bool {
	return projection.Ephemeral &&
		strings.TrimSpace(projection.Request.ID) != "" &&
		(projection.Approve || projection.Deny)
}
