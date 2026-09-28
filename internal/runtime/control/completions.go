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
	"strconv"
	"strings"
	"time"

	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
)

var (
	ErrCompletionFeedOverflow    = errors.New("completion feed overflowed")
	ErrCompletionFeedUnsupported = errors.New("completion feed unsupported by running server")
)

type CompletionFeedStream struct {
	response *http.Response
	reader   *bufio.Reader
	snapshot agentcompletion.Snapshot
}

func OpenCompletionFeed(ctx context.Context, workspaceID string, limit int) (*CompletionFeedStream, State, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	state, err := LoadContext(ctx)
	if err != nil {
		return nil, State{}, err
	}
	query := url.Values{}
	if workspaceID = strings.TrimSpace(workspaceID); workspaceID != "" {
		query.Set("workspace_id", workspaceID)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	endpoint := "http://" + state.Address + "/completions/stream"
	if encoded := query.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, state, err
	}
	request.Header.Set("Authorization", "Bearer "+state.Token)
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DialContext: dialer.DialContext, ResponseHeaderTimeout: 5 * time.Second}}
	response, err := client.Do(request)
	if err != nil {
		return nil, state, fmt.Errorf("running server completion endpoint unavailable: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64*1024))
		_ = response.Body.Close()
		if response.StatusCode == http.StatusNotFound {
			return nil, state, fmt.Errorf("%w: restart the running server to enable completion streaming", ErrCompletionFeedUnsupported)
		}
		return nil, state, fmt.Errorf("runtime completion stream failed with HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	reader := bufio.NewReader(response.Body)
	snapshot, err := readCompletionFeedReady(reader)
	if err != nil {
		_ = response.Body.Close()
		return nil, state, err
	}
	return &CompletionFeedStream{response: response, reader: reader, snapshot: snapshot}, state, nil
}

func readCompletionFeedReady(reader *bufio.Reader) (agentcompletion.Snapshot, error) {
	for {
		eventType, data, err := readSSEPacket(reader)
		if err != nil {
			return agentcompletion.Snapshot{}, err
		}
		if eventType != "ready" {
			continue
		}
		var snapshot agentcompletion.Snapshot
		if err := json.Unmarshal([]byte(data), &snapshot); err != nil {
			return agentcompletion.Snapshot{}, fmt.Errorf("decode completion feed ready frame: %w", err)
		}
		if snapshot.Records == nil {
			snapshot.Records = []agentcompletion.Record{}
		}
		return snapshot, nil
	}
}

func (stream *CompletionFeedStream) Snapshot() agentcompletion.Snapshot {
	if stream == nil {
		return agentcompletion.Snapshot{Records: []agentcompletion.Record{}}
	}
	result := stream.snapshot
	result.Records = append([]agentcompletion.Record(nil), stream.snapshot.Records...)
	return result
}

func (stream *CompletionFeedStream) Next() (agentcompletion.Event, error) {
	if stream == nil || stream.reader == nil {
		return agentcompletion.Event{}, io.EOF
	}
	for {
		eventType, data, err := readSSEPacket(stream.reader)
		if err != nil {
			return agentcompletion.Event{}, err
		}
		if eventType == "overflow" {
			return agentcompletion.Event{}, decodeFeedOverflow(ErrCompletionFeedOverflow, data)
		}
		if !strings.HasPrefix(eventType, "completion.") || strings.TrimSpace(data) == "" {
			continue
		}
		var event agentcompletion.Event
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return agentcompletion.Event{}, fmt.Errorf("decode completion feed event: %w", err)
		}
		return event, nil
	}
}

func (stream *CompletionFeedStream) Close() error {
	if stream == nil || stream.response == nil || stream.response.Body == nil {
		return nil
	}
	return stream.response.Body.Close()
}
