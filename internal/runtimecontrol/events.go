package runtimecontrol

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

	"go.mewis.me/codemcp/internal/runtimeevent"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

var ErrEventStreamGap = errors.New("runtime event stream gap")

type EventStream struct {
	response       *http.Response
	scanner        *bufio.Scanner
	latestSequence uint64
}

func OpenEvents(ctx context.Context) (*EventStream, State, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	span := tracepkg.Start(ctx, "CONTROL", "runtime.events.connect", "Connecting runtime event stream", tracepkg.String("state_file", Path()))
	state, err := LoadContext(ctx)
	if err != nil {
		span.FailMessage("Runtime event stream control lookup failed", err)
		return nil, State{}, err
	}
	endpoint := "http://" + state.Address + "/events"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		span.FailMessage("Runtime event stream request construction failed", err, tracepkg.Int("pid", state.PID), tracepkg.URL("endpoint", endpoint))
		return nil, state, err
	}
	request.Header.Set("Authorization", "Bearer "+state.Token)
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DialContext: dialer.DialContext, ResponseHeaderTimeout: 5 * time.Second}}
	response, err := client.Do(request)
	if err != nil {
		span.FailMessage("Runtime event stream connection failed", err, tracepkg.Int("pid", state.PID), tracepkg.URL("endpoint", endpoint))
		return nil, state, fmt.Errorf("running server control endpoint unavailable: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64*1024))
		_ = response.Body.Close()
		err := fmt.Errorf("runtime event stream failed with HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
		span.FailMessage("Runtime event stream connection failed", err, tracepkg.Int("pid", state.PID), tracepkg.URL("endpoint", endpoint), tracepkg.Int("status", response.StatusCode), tracepkg.Int64("response_bytes", int64(len(body))))
		return nil, state, err
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	latestSequence, err := readEventStreamReady(scanner)
	if err != nil {
		_ = response.Body.Close()
		span.FailMessage("Runtime event stream ready handshake failed", err, tracepkg.Int("pid", state.PID), tracepkg.URL("endpoint", endpoint), tracepkg.Int("status", response.StatusCode))
		return nil, state, err
	}
	span.EndMessage("Runtime event stream connected", tracepkg.Int("pid", state.PID), tracepkg.URL("endpoint", endpoint), tracepkg.Int("status", response.StatusCode), tracepkg.Uint64("latest_sequence", latestSequence))
	return &EventStream{response: response, scanner: scanner, latestSequence: latestSequence}, state, nil
}

func readEventStreamReady(scanner *bufio.Scanner) (uint64, error) {
	eventType := ""
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if eventType != "ready" {
				eventType = ""
				data.Reset()
				continue
			}
			var ready struct {
				LatestSequence uint64 `json:"latest_sequence"`
			}
			if strings.TrimSpace(data.String()) != "" {
				if err := json.Unmarshal([]byte(data.String()), &ready); err != nil {
					return 0, fmt.Errorf("decode runtime event stream ready frame: %w", err)
				}
			}
			return ready.LatestSequence, nil
		}
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, io.EOF
}

func (stream *EventStream) LatestSequence() uint64 {
	if stream == nil {
		return 0
	}
	return stream.latestSequence
}

func (stream *EventStream) Next() (runtimeevent.Event, error) {
	if stream == nil || stream.scanner == nil {
		return runtimeevent.Event{}, io.EOF
	}
	eventType := ""
	var data strings.Builder
	flush := func() (runtimeevent.Event, bool, error) {
		if eventType != "runtime" || strings.TrimSpace(data.String()) == "" {
			return runtimeevent.Event{}, false, nil
		}
		var event runtimeevent.Event
		if err := json.Unmarshal([]byte(data.String()), &event); err != nil {
			return runtimeevent.Event{}, false, fmt.Errorf("decode runtime event: %w", err)
		}
		return event, true, nil
	}
	for stream.scanner.Scan() {
		line := stream.scanner.Text()
		if line == "" {
			if eventType == "gap" {
				var gap struct {
					DroppedSequence uint64 `json:"dropped_sequence"`
					LatestSequence  uint64 `json:"latest_sequence"`
				}
				if err := json.Unmarshal([]byte(data.String()), &gap); err != nil {
					return runtimeevent.Event{}, fmt.Errorf("decode runtime event stream gap: %w", err)
				}
				return runtimeevent.Event{}, fmt.Errorf("%w: dropped sequence %d, latest sequence %d", ErrEventStreamGap, gap.DroppedSequence, gap.LatestSequence)
			}
			event, ok, err := flush()
			if err != nil || ok {
				return event, err
			}
			eventType = ""
			data.Reset()
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := stream.scanner.Err(); err != nil {
		return runtimeevent.Event{}, err
	}
	return runtimeevent.Event{}, io.EOF
}

func (stream *EventStream) Close() error {
	if stream == nil || stream.response == nil || stream.response.Body == nil {
		return nil
	}
	return stream.response.Body.Close()
}
