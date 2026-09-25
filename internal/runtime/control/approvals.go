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
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/approval"
)

var (
	ErrApprovalFeedOverflow    = errors.New("approval feed overflowed")
	ErrApprovalFeedUnsupported = errors.New("approval feed unsupported by running server")
)

type ApprovalFeedSnapshot struct {
	Requests       []approval.Request `json:"requests"`
	LatestSequence uint64             `json:"latest_sequence"`
}

type ApprovalFeedStream struct {
	response *http.Response
	reader   *bufio.Reader
	snapshot ApprovalFeedSnapshot
}

func OpenApprovalFeed(ctx context.Context) (*ApprovalFeedStream, State, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	state, err := LoadContext(ctx)
	if err != nil {
		return nil, State{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+state.Address+"/requests/stream", nil)
	if err != nil {
		return nil, state, err
	}
	request.Header.Set("Authorization", "Bearer "+state.Token)
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DialContext: dialer.DialContext, ResponseHeaderTimeout: 5 * time.Second}}
	response, err := client.Do(request)
	if err != nil {
		return nil, state, fmt.Errorf("running server approval endpoint unavailable: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64*1024))
		_ = response.Body.Close()
		if response.StatusCode == http.StatusNotFound {
			return nil, state, fmt.Errorf("%w: restart the running server to enable approval streaming", ErrApprovalFeedUnsupported)
		}
		return nil, state, fmt.Errorf("runtime approval stream failed with HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	reader := bufio.NewReader(response.Body)
	snapshot, err := readApprovalFeedReady(reader)
	if err != nil {
		_ = response.Body.Close()
		return nil, state, err
	}
	return &ApprovalFeedStream{response: response, reader: reader, snapshot: snapshot}, state, nil
}

func readApprovalFeedReady(reader *bufio.Reader) (ApprovalFeedSnapshot, error) {
	for {
		eventType, data, err := readSSEPacket(reader)
		if err != nil {
			return ApprovalFeedSnapshot{}, err
		}
		if eventType != "ready" {
			continue
		}
		var snapshot ApprovalFeedSnapshot
		if err := json.Unmarshal([]byte(data), &snapshot); err != nil {
			return ApprovalFeedSnapshot{}, fmt.Errorf("decode approval feed ready frame: %w", err)
		}
		if snapshot.Requests == nil {
			snapshot.Requests = []approval.Request{}
		}
		return snapshot, nil
	}
}

func (stream *ApprovalFeedStream) Snapshot() ApprovalFeedSnapshot {
	if stream == nil {
		return ApprovalFeedSnapshot{Requests: []approval.Request{}}
	}
	result := stream.snapshot
	result.Requests = append([]approval.Request(nil), stream.snapshot.Requests...)
	return result
}

func (stream *ApprovalFeedStream) Next() (approval.Event, error) {
	if stream == nil || stream.reader == nil {
		return approval.Event{}, io.EOF
	}
	for {
		eventType, data, err := readSSEPacket(stream.reader)
		if err != nil {
			return approval.Event{}, err
		}
		if eventType == "overflow" {
			return approval.Event{}, ErrApprovalFeedOverflow
		}
		if !strings.HasPrefix(eventType, "approval.") || strings.TrimSpace(data) == "" {
			continue
		}
		var event approval.Event
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return approval.Event{}, fmt.Errorf("decode approval feed event: %w", err)
		}
		return event, nil
	}
}

func (stream *ApprovalFeedStream) Close() error {
	if stream == nil || stream.response == nil || stream.response.Body == nil {
		return nil
	}
	return stream.response.Body.Close()
}
