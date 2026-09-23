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

	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
)

var (
	ErrExecutionFeedOverflow    = errors.New("execution feed overflowed")
	ErrExecutionFeedUnsupported = errors.New("execution feed unsupported by running server")
)

type ExecutionFeedStream struct {
	response *http.Response
	reader   *bufio.Reader
	snapshot shellruntime.ExecutionFeedSnapshot
}

func ListProcesses(ctx context.Context, workspaceID string) ([]shellruntime.ProcessInfo, error) {
	var result []shellruntime.ProcessInfo
	_, err := Request(ctx, http.MethodGet, "/api/workspaces/"+url.PathEscape(strings.TrimSpace(workspaceID))+"/processes", nil, &result)
	return result, err
}

func DeleteFinishedProcess(ctx context.Context, workspaceID, id string) error {
	var result map[string]bool
	_, err := Request(ctx, http.MethodDelete, "/api/workspaces/"+url.PathEscape(strings.TrimSpace(workspaceID))+"/processes/"+url.PathEscape(strings.TrimSpace(id)), nil, &result)
	return err
}

func OpenExecutionFeed(ctx context.Context) (*ExecutionFeedStream, State, error) {
	state, err := Load()
	if err != nil {
		return nil, State{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+state.Address+"/executions/stream", nil)
	if err != nil {
		return nil, state, err
	}
	request.Header.Set("Authorization", "Bearer "+state.Token)
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DialContext: dialer.DialContext, ResponseHeaderTimeout: 5 * time.Second}}
	response, err := client.Do(request)
	if err != nil {
		return nil, state, fmt.Errorf("running server execution endpoint unavailable: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64*1024))
		_ = response.Body.Close()
		if response.StatusCode == http.StatusNotFound {
			return nil, state, fmt.Errorf("%w: restart the running server to enable command execution streaming", ErrExecutionFeedUnsupported)
		}
		return nil, state, fmt.Errorf("runtime execution stream failed with HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	reader := bufio.NewReader(response.Body)
	snapshot, err := readExecutionFeedReady(reader)
	if err != nil {
		_ = response.Body.Close()
		return nil, state, err
	}
	return &ExecutionFeedStream{response: response, reader: reader, snapshot: snapshot}, state, nil
}

type executionFeedReady struct {
	Events         []shellruntime.ExecutionFeedEvent `json:"events,omitempty"`
	LatestSequence uint64                            `json:"latest_sequence"`
	ReplayCount    int                               `json:"replay_count,omitempty"`
}

func readExecutionFeedReady(reader *bufio.Reader) (shellruntime.ExecutionFeedSnapshot, error) {
	for {
		eventType, data, err := readSSEPacket(reader)
		if err != nil {
			return shellruntime.ExecutionFeedSnapshot{}, err
		}
		if eventType != "ready" {
			continue
		}
		var ready executionFeedReady
		if err := json.Unmarshal([]byte(data), &ready); err != nil {
			return shellruntime.ExecutionFeedSnapshot{}, fmt.Errorf("decode execution feed ready frame: %w", err)
		}
		snapshot := shellruntime.ExecutionFeedSnapshot{Events: append([]shellruntime.ExecutionFeedEvent(nil), ready.Events...), LatestSequence: ready.LatestSequence}
		if ready.ReplayCount < 0 {
			return shellruntime.ExecutionFeedSnapshot{}, fmt.Errorf("invalid execution feed replay count: %d", ready.ReplayCount)
		}
		for range ready.ReplayCount {
			event, err := readExecutionFeedEvent(reader)
			if err != nil {
				return shellruntime.ExecutionFeedSnapshot{}, fmt.Errorf("read execution feed replay: %w", err)
			}
			snapshot.Events = append(snapshot.Events, event)
		}
		return snapshot, nil
	}
}

func (stream *ExecutionFeedStream) Snapshot() shellruntime.ExecutionFeedSnapshot {
	if stream == nil {
		return shellruntime.ExecutionFeedSnapshot{Events: []shellruntime.ExecutionFeedEvent{}}
	}
	return stream.snapshot
}

func (stream *ExecutionFeedStream) Next() (shellruntime.ExecutionFeedEvent, error) {
	if stream == nil || stream.reader == nil {
		return shellruntime.ExecutionFeedEvent{}, io.EOF
	}
	return readExecutionFeedEvent(stream.reader)
}

func readExecutionFeedEvent(reader *bufio.Reader) (shellruntime.ExecutionFeedEvent, error) {
	for {
		eventType, data, err := readSSEPacket(reader)
		if err != nil {
			return shellruntime.ExecutionFeedEvent{}, err
		}
		if eventType == "overflow" {
			return shellruntime.ExecutionFeedEvent{}, ErrExecutionFeedOverflow
		}
		if eventType != shellruntime.ExecutionEventStarted && eventType != shellruntime.ExecutionEventOutput && eventType != shellruntime.ExecutionEventCompleted {
			continue
		}
		if strings.TrimSpace(data) == "" {
			continue
		}
		var event shellruntime.ExecutionFeedEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return shellruntime.ExecutionFeedEvent{}, fmt.Errorf("decode execution feed event: %w", err)
		}
		return event, nil
	}
}

func readSSEPacket(reader *bufio.Reader) (string, string, error) {
	eventType := ""
	var data strings.Builder
	for {
		line, err := readSSELine(reader)
		if err != nil && !errors.Is(err, io.EOF) {
			return "", "", err
		}
		if line == "" {
			if eventType != "" || data.Len() > 0 {
				return eventType, data.String(), nil
			}
		} else if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
		if errors.Is(err, io.EOF) {
			if eventType != "" || data.Len() > 0 {
				return eventType, data.String(), nil
			}
			return "", "", io.EOF
		}
	}
}

func readSSELine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	return line, err
}

func (stream *ExecutionFeedStream) Close() error {
	if stream == nil || stream.response == nil || stream.response.Body == nil {
		return nil
	}
	return stream.response.Body.Close()
}
