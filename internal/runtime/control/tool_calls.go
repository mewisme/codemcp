package control

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/runtime/activity"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
)

var (
	ErrToolCallFeedOverflow    = errors.New("tool call feed overflowed")
	ErrToolCallFeedUnsupported = errors.New("tool call feed unsupported by running server")
)

type ToolCallFeedSnapshot struct {
	Events         []activity.Event          `json:"events"`
	Records        []activity.ToolCallRecord `json:"records,omitempty"`
	LatestSequence uint64                    `json:"latest_sequence"`
}

type ToolCallFeedStream struct {
	response *http.Response
	reader   *bufio.Reader
	snapshot ToolCallFeedSnapshot
}

func ListExecutions(ctx context.Context) ([]shellruntime.ExecutionInfo, error) {
	var result []shellruntime.ExecutionInfo
	_, err := Request(ctx, http.MethodGet, "/executions", nil, &result)
	return result, err
}

func GetExecution(ctx context.Context, id string) (shellruntime.ExecutionSnapshot, error) {
	var result shellruntime.ExecutionSnapshot
	_, err := Request(ctx, http.MethodGet, "/executions/"+url.PathEscape(strings.TrimSpace(id)), nil, &result)
	return result, err
}

func OpenToolCallFeed(ctx context.Context) (*ToolCallFeedStream, State, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	state, err := LoadContext(ctx)
	if err != nil {
		return nil, State{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+state.Address+"/tool-calls/stream", nil)
	if err != nil {
		return nil, state, err
	}
	request.Header.Set("Authorization", "Bearer "+state.Token)
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DialContext: dialer.DialContext, ResponseHeaderTimeout: 5 * time.Second}}
	response, err := client.Do(request)
	if err != nil {
		return nil, state, fmt.Errorf("running server tool call endpoint unavailable: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64*1024))
		_ = response.Body.Close()
		if response.StatusCode == http.StatusNotFound {
			return nil, state, fmt.Errorf("%w: restart the running server to enable tool call streaming", ErrToolCallFeedUnsupported)
		}
		return nil, state, fmt.Errorf("runtime tool call stream failed with HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	reader := bufio.NewReader(response.Body)
	snapshot, err := readToolCallFeedReady(reader)
	if err != nil {
		_ = response.Body.Close()
		return nil, state, err
	}
	return &ToolCallFeedStream{response: response, reader: reader, snapshot: snapshot}, state, nil
}

func readToolCallFeedReady(reader *bufio.Reader) (ToolCallFeedSnapshot, error) {
	for {
		eventType, data, err := readSSEPacket(reader)
		if err != nil {
			return ToolCallFeedSnapshot{}, err
		}
		if eventType != "ready" {
			continue
		}
		var ready struct {
			LatestSequence uint64                    `json:"latest_sequence"`
			ReplayCount    int                       `json:"replay_count"`
			Records        []activity.ToolCallRecord `json:"records,omitempty"`
		}
		if err := json.Unmarshal([]byte(data), &ready); err != nil {
			return ToolCallFeedSnapshot{}, fmt.Errorf("decode tool call feed ready frame: %w", err)
		}
		if ready.ReplayCount < 0 {
			return ToolCallFeedSnapshot{}, fmt.Errorf("invalid tool call feed replay count: %d", ready.ReplayCount)
		}
		snapshot := ToolCallFeedSnapshot{Events: make([]activity.Event, 0, ready.ReplayCount), Records: append([]activity.ToolCallRecord(nil), ready.Records...), LatestSequence: ready.LatestSequence}
		for range ready.ReplayCount {
			event, err := readToolCallFeedEvent(reader)
			if err != nil {
				return ToolCallFeedSnapshot{}, fmt.Errorf("read tool call feed replay: %w", err)
			}
			snapshot.Events = append(snapshot.Events, event)
		}
		return snapshot, nil
	}
}

func (stream *ToolCallFeedStream) Snapshot() ToolCallFeedSnapshot {
	if stream == nil {
		return ToolCallFeedSnapshot{Events: []activity.Event{}}
	}
	return stream.snapshot
}

func (stream *ToolCallFeedStream) Next() (activity.Event, error) {
	if stream == nil || stream.reader == nil {
		return activity.Event{}, io.EOF
	}
	return readToolCallFeedEvent(stream.reader)
}

func readToolCallFeedEvent(reader *bufio.Reader) (activity.Event, error) {
	for {
		eventType, data, err := readSSEPacket(reader)
		if err != nil {
			return activity.Event{}, err
		}
		if eventType == "overflow" {
			return activity.Event{}, decodeFeedOverflow(ErrToolCallFeedOverflow, data)
		}
		if eventType != "tool_call" || strings.TrimSpace(data) == "" {
			continue
		}
		var event activity.Event
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return activity.Event{}, fmt.Errorf("decode tool call feed event: %w", err)
		}
		return event, nil
	}
}

func (stream *ToolCallFeedStream) Close() error {
	if stream == nil || stream.response == nil || stream.response.Body == nil {
		return nil
	}
	return stream.response.Body.Close()
}
