package application

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"go.mewis.me/codemcp/internal/approval"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type ApprovalStateSnapshot struct {
	Requests       []approval.Request
	LatestSequence uint64
}

type ApprovalSubscription struct {
	stream   *runtimecontrol.ApprovalFeedStream
	close    sync.Once
	closeErr error
}

func SubscribeApprovalRequests(ctx context.Context) (*ApprovalSubscription, ApprovalStateSnapshot, error) {
	stream, _, err := runtimecontrol.OpenApprovalFeed(ctx)
	if err != nil {
		return nil, ApprovalStateSnapshot{}, err
	}
	snapshot := stream.Snapshot()
	return &ApprovalSubscription{stream: stream}, ApprovalStateSnapshot{
		Requests:       append([]approval.Request(nil), snapshot.Requests...),
		LatestSequence: snapshot.LatestSequence,
	}, nil
}

func (subscription *ApprovalSubscription) Next() (approval.Event, error) {
	if subscription == nil || subscription.stream == nil {
		return approval.Event{}, io.EOF
	}
	return subscription.stream.Next()
}

func (subscription *ApprovalSubscription) Close() error {
	if subscription == nil || subscription.stream == nil {
		return nil
	}
	subscription.close.Do(func() {
		subscription.closeErr = subscription.stream.Close()
	})
	return subscription.closeErr
}

func ListApprovalRequests(ctx context.Context) ([]approval.Request, error) {
	span := tracepkg.Start(ctx, "REQUEST", "request.list", "Listing control approval requests")
	var result []approval.Request
	_, err := runtimecontrol.Request(ctx, http.MethodGet, "/requests", nil, &result)
	if err != nil {
		span.FailMessage("Control approval request list failed", err)
		return result, err
	}
	pending := 0
	for _, request := range result {
		if request.Status == approval.StatusPending {
			pending++
		}
	}
	span.EndMessage("Control approval requests listed", tracepkg.Int("count", len(result)), tracepkg.Int("pending_count", pending))
	return result, nil
}

func GetApprovalRequest(ctx context.Context, id string) (approval.Request, error) {
	requested := strings.TrimSpace(id)
	span := tracepkg.Start(ctx, "REQUEST", "request.view", "Loading control approval request", tracepkg.String("request", requested))
	var result approval.Request
	_, err := runtimecontrol.Request(ctx, http.MethodGet, "/requests/view?id="+url.QueryEscape(requested), nil, &result)
	if err != nil {
		span.FailMessage("Control approval request load failed", err, tracepkg.String("request", requested))
		return result, err
	}
	span.EndMessage("Control approval request loaded", tracepkg.String("request", requested), tracepkg.String("request_id", result.ID), tracepkg.String("status", string(result.Status)), tracepkg.String("workspace_id", result.WorkspaceID), tracepkg.String("tool", result.TargetTool))
	return result, nil
}

func ResolveApprovalRequest(ctx context.Context, id string, approve bool, reason string) (approval.Request, error) {
	return ResolveApprovalRequestWithRuntimeGrant(ctx, id, approve, false, reason)
}

func ResolveApprovalRequestWithRuntimeGrant(ctx context.Context, id string, approve, allowSimilar bool, reason string) (approval.Request, error) {
	action := "deny"
	if approve {
		action = "approve"
	}
	requested := strings.TrimSpace(id)
	span := tracepkg.Start(ctx, "REQUEST", "request.resolve", "Resolving control approval request", tracepkg.String("request", requested), tracepkg.String("action", action), tracepkg.Bool("allow_similar", allowSimilar), tracepkg.Bool("reason_configured", strings.TrimSpace(reason) != ""))
	var result approval.Request
	_, err := runtimecontrol.Request(ctx, http.MethodPost, "/requests/"+action, map[string]any{"id": requested, "reason": strings.TrimSpace(reason), "allow_similar": allowSimilar}, &result)
	if err != nil {
		span.FailMessage("Control approval request resolution failed", err, tracepkg.String("request", requested), tracepkg.String("action", action), tracepkg.Bool("allow_similar", allowSimilar))
		return result, err
	}
	span.EndMessage("Control approval request resolved", tracepkg.String("request", requested), tracepkg.String("request_id", result.ID), tracepkg.String("action", action), tracepkg.String("status", string(result.Status)), tracepkg.String("resolved_by", result.ResolvedBy), tracepkg.Bool("allow_similar", allowSimilar))
	return result, nil
}

func CreateDummyApprovalRequest(ctx context.Context, workspaceID, title, command string) (approval.Request, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	span := tracepkg.Start(ctx, "REQUEST", "request.create-dummy", "Creating dummy control approval request", tracepkg.String("workspace_id", workspaceID), tracepkg.Bool("title_configured", strings.TrimSpace(title) != ""), tracepkg.Bool("command_configured", strings.TrimSpace(command) != ""))
	var result approval.Request
	_, err := runtimecontrol.Request(ctx, http.MethodPost, "/requests/create-dummy", map[string]string{
		"workspace_id": workspaceID, "title": strings.TrimSpace(title), "command": strings.TrimSpace(command),
	}, &result)
	if err != nil {
		span.FailMessage("Dummy control approval request creation failed", err, tracepkg.String("workspace_id", workspaceID))
		return result, err
	}
	span.EndMessage("Dummy control approval request created", tracepkg.String("request_id", result.ID), tracepkg.String("workspace_id", result.WorkspaceID), tracepkg.String("status", string(result.Status)))
	return result, nil
}

func RevokeRuntimeGrant(ctx context.Context, id string) (approval.Request, error) {
	requested := strings.TrimSpace(id)
	span := tracepkg.Start(ctx, "REQUEST", "request.revoke-grant", "Revoking runtime session grant", tracepkg.String("request", requested))
	var result approval.Request
	_, err := runtimecontrol.Request(ctx, http.MethodPost, "/requests/revoke-grant", map[string]any{"id": requested}, &result)
	if err != nil {
		span.FailMessage("Runtime session grant revoke failed", err, tracepkg.String("request", requested))
		return result, err
	}
	span.EndMessage("Runtime session grant revoked", tracepkg.String("request", requested), tracepkg.String("request_id", result.ID), tracepkg.String("status", string(result.Status)))
	return result, nil
}

func ListRuntimeGrants(ctx context.Context, workspaceID string) ([]approval.Request, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	span := tracepkg.Start(ctx, "REQUEST", "request.grants", "Listing runtime session grants", tracepkg.String("workspace_id", workspaceID))
	path := "/requests/grants"
	if workspaceID != "" {
		path += "?workspace_id=" + url.QueryEscape(workspaceID)
	}
	var result []approval.Request
	_, err := runtimecontrol.Request(ctx, http.MethodGet, path, nil, &result)
	if err != nil {
		span.FailMessage("Runtime session grant list failed", err)
		return result, err
	}
	span.EndMessage("Runtime session grants listed", tracepkg.Int("count", len(result)))
	return result, nil
}
