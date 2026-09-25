package application

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"go.mewis.me/codemcp/internal/capability"
	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const defaultCompletionReadLimit = 50

type CompletionStateSnapshot struct {
	Records        []agentcompletion.Record
	LatestSequence uint64
}

type CompletionSubscription struct {
	stream   *runtimecontrol.CompletionFeedStream
	close    sync.Once
	closeErr error
}

func ListCompletions(ctx context.Context, workspaceID string, limit int) ([]agentcompletion.Record, error) {
	if limit <= 0 {
		limit = defaultCompletionReadLimit
	}
	workspaceID = strings.TrimSpace(workspaceID)
	fields := []tracepkg.Field{tracepkg.Int("limit", limit)}
	if workspaceID != "" {
		fields = append(fields, tracepkg.String("workspace_id", workspaceID))
	}
	result, err := runOperation(ctx, "COMPLETION", capability.CompletionList, "Listing agent completion history", fields, func() ([]agentcompletion.Record, error) {
		query := url.Values{}
		query.Set("limit", strconv.Itoa(limit))
		if workspaceID != "" {
			query.Set("workspace_id", workspaceID)
		}
		var records []agentcompletion.Record
		_, err := runtimecontrol.Request(ctx, http.MethodGet, "/completions?"+query.Encode(), nil, &records)
		if records == nil {
			records = []agentcompletion.Record{}
		}
		return records, err
	})
	return result.Value, err
}

func CurrentCompletion(ctx context.Context, workspaceID string) (agentcompletion.Record, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	result, err := runOperation(ctx, "COMPLETION", capability.CompletionCurrent, "Loading current agent completion", []tracepkg.Field{tracepkg.String("workspace_id", workspaceID)}, func() (agentcompletion.Record, error) {
		var record agentcompletion.Record
		_, err := runtimecontrol.Request(ctx, http.MethodGet, "/completions/current?workspace_id="+url.QueryEscape(workspaceID), nil, &record)
		return record, err
	})
	return result.Value, err
}

func ViewCompletion(ctx context.Context, id string) (agentcompletion.Record, error) {
	id = strings.TrimSpace(id)
	result, err := runOperation(ctx, "COMPLETION", capability.CompletionView, "Loading agent completion", []tracepkg.Field{tracepkg.String("completion_id", id)}, func() (agentcompletion.Record, error) {
		var record agentcompletion.Record
		_, err := runtimecontrol.Request(ctx, http.MethodGet, "/completions/view?id="+url.QueryEscape(id), nil, &record)
		return record, err
	})
	return result.Value, err
}

func SubscribeCompletions(ctx context.Context, workspaceID string, limit int) (*CompletionSubscription, CompletionStateSnapshot, error) {
	if limit <= 0 {
		limit = defaultCompletionReadLimit
	}
	stream, _, err := runtimecontrol.OpenCompletionFeed(ctx, strings.TrimSpace(workspaceID), limit)
	if err != nil {
		return nil, CompletionStateSnapshot{}, err
	}
	snapshot := stream.Snapshot()
	return &CompletionSubscription{stream: stream}, CompletionStateSnapshot{
		Records:        append([]agentcompletion.Record(nil), snapshot.Records...),
		LatestSequence: snapshot.LatestSequence,
	}, nil
}

func (subscription *CompletionSubscription) Next() (agentcompletion.Event, error) {
	if subscription == nil || subscription.stream == nil {
		return agentcompletion.Event{}, io.EOF
	}
	return subscription.stream.Next()
}

func (subscription *CompletionSubscription) Close() error {
	if subscription == nil || subscription.stream == nil {
		return nil
	}
	subscription.close.Do(func() {
		subscription.closeErr = subscription.stream.Close()
	})
	return subscription.closeErr
}
