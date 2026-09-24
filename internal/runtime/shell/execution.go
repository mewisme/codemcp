package shell

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"go.mewis.me/codemcp/internal/idgen"
)

const (
	maxExecutionLogBytes      = 400_000
	MaxExecutionFeedEvents    = 1024
	maxExecutionEventBytes    = 8 << 10
	maxRecentExecutions       = 100
	executionSubscriberBuffer = 64
	executionFeedBuffer       = 128
	ExecutionStatusRunning    = "running"
	ExecutionStatusSuccess    = "success"
	ExecutionStatusFailed     = "failed"
	ExecutionStatusCancelled  = "cancelled"
	ExecutionStatusTimedOut   = "timed_out"
	ExecutionEventStarted     = "started"
	ExecutionEventOutput      = "output"
	ExecutionEventCompleted   = "completed"
)

var ErrExecutionNotFound = errors.New("execution not found")

type ExecutionInfo struct {
	ID                   string `json:"id"`
	WorkspaceID          string `json:"workspace_id"`
	Tool                 string `json:"tool"`
	Command              string `json:"command"`
	RequestedCommand     string `json:"requested_command,omitempty"`
	EffectiveCommand     string `json:"effective_command,omitempty"`
	SecurityCommand      string `json:"security_command,omitempty"`
	CWD                  string `json:"cwd"`
	Shell                string `json:"shell,omitempty"`
	Source               string `json:"source,omitempty"`
	CallID               string `json:"call_id,omitempty"`
	SessionHash          string `json:"session_hash,omitempty"`
	ReceivedByInstanceID string `json:"received_by_instance_id,omitempty"`
	ExecutedByInstanceID string `json:"executed_by_instance_id,omitempty"`
	StartedAt            string `json:"started_at"`
	FinishedAt           string `json:"finished_at,omitempty"`
	Status               string `json:"status"`
	ExitCode             *int   `json:"exit_code,omitempty"`
	TimedOut             bool   `json:"timed_out,omitempty"`
}

type ExecutionSnapshot struct {
	Execution      ExecutionInfo `json:"execution"`
	Stdout         string        `json:"stdout"`
	Stderr         string        `json:"stderr"`
	LatestSequence uint64        `json:"latest_sequence"`
}

type ExecutionEvent struct {
	Sequence    uint64 `json:"sequence"`
	Type        string `json:"type"`
	ExecutionID string `json:"execution_id"`
	Stream      string `json:"stream,omitempty"`
	Data        string `json:"data,omitempty"`
	Status      string `json:"status,omitempty"`
	ExitCode    *int   `json:"exit_code,omitempty"`
	TimedOut    bool   `json:"timed_out,omitempty"`
	Timestamp   string `json:"timestamp"`
}

type ExecutionFeedEvent struct {
	Sequence    uint64         `json:"sequence"`
	Type        string         `json:"type"`
	ExecutionID string         `json:"execution_id"`
	WorkspaceID string         `json:"workspace_id"`
	Execution   *ExecutionInfo `json:"execution,omitempty"`
	Stream      string         `json:"stream,omitempty"`
	Data        string         `json:"data,omitempty"`
	Status      string         `json:"status,omitempty"`
	ExitCode    *int           `json:"exit_code,omitempty"`
	TimedOut    bool           `json:"timed_out,omitempty"`
	Timestamp   string         `json:"timestamp"`
}

type ExecutionFeedSnapshot struct {
	Events         []ExecutionFeedEvent `json:"events"`
	LatestSequence uint64               `json:"latest_sequence"`
}

type ExecutionOverflow struct {
	DroppedSequence uint64 `json:"dropped_sequence"`
}

type ExecutionSubscription struct {
	Events   chan ExecutionEvent
	Overflow chan ExecutionOverflow
	record   *executionRecord
	overflow bool
	closed   bool
}

type ExecutionFeedSubscription struct {
	Events      chan ExecutionFeedEvent
	Overflow    chan ExecutionOverflow
	workspaceID string
	overflow    bool
	closed      bool
}

type ExecutionInput struct {
	WorkspaceID          string
	Tool                 string
	Command              string
	RequestedCommand     string
	EffectiveCommand     string
	SecurityCommand      string
	CWD                  string
	Shell                string
	Source               string
	CallID               string
	SessionHash          string
	ReceivedByInstanceID string
	ExecutedByInstanceID string
}

type ExecutionHub struct {
	mu           sync.RWMutex
	executions   map[string]*executionRecord
	order        []string
	maxRecent    int
	feedMu       sync.Mutex
	feed         []ExecutionFeedEvent
	feedSequence uint64
	feedSubs     map[*ExecutionFeedSubscription]struct{}
}

type executionRecord struct {
	mu       sync.Mutex
	info     ExecutionInfo
	stdout   []byte
	stderr   []byte
	sequence uint64
	subs     map[*ExecutionSubscription]struct{}
}

type ExecutionRun struct {
	hub    *ExecutionHub
	record *executionRecord
}

type executionWriter struct {
	run    *ExecutionRun
	stream string
}

type executionSourceKey struct{}
type executionMetadataKey struct{}

type ExecutionMetadata struct {
	Source               string
	CallID               string
	SessionHash          string
	ReceivedByInstanceID string
	ExecutedByInstanceID string
}

func NewExecutionHub() *ExecutionHub {
	return &ExecutionHub{executions: map[string]*executionRecord{}, maxRecent: maxRecentExecutions, feedSubs: map[*ExecutionFeedSubscription]struct{}{}}
}

func WithExecutionSource(ctx context.Context, source string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, executionSourceKey{}, strings.TrimSpace(source))
}

func WithExecutionMetadata(ctx context.Context, metadata ExecutionMetadata) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	metadata.Source = strings.TrimSpace(metadata.Source)
	metadata.CallID = strings.TrimSpace(metadata.CallID)
	metadata.SessionHash = strings.TrimSpace(metadata.SessionHash)
	metadata.ReceivedByInstanceID = strings.TrimSpace(metadata.ReceivedByInstanceID)
	metadata.ExecutedByInstanceID = strings.TrimSpace(metadata.ExecutedByInstanceID)
	return context.WithValue(ctx, executionMetadataKey{}, metadata)
}

func executionMetadata(ctx context.Context) ExecutionMetadata {
	if ctx == nil {
		return ExecutionMetadata{}
	}
	value, _ := ctx.Value(executionMetadataKey{}).(ExecutionMetadata)
	return value
}

func executionSource(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(executionSourceKey{}).(string)
	return strings.TrimSpace(value)
}

func (h *ExecutionHub) Begin(input ExecutionInput) *ExecutionRun {
	if h == nil {
		return nil
	}
	tool := strings.TrimSpace(input.Tool)
	if tool == "" {
		tool = "run_command"
	}
	h.mu.Lock()
	id := idgen.Must("exec", 8)
	record := &executionRecord{info: ExecutionInfo{
		ID: id, WorkspaceID: strings.TrimSpace(input.WorkspaceID), Tool: tool, Command: input.Command, RequestedCommand: input.RequestedCommand, EffectiveCommand: input.EffectiveCommand, SecurityCommand: input.SecurityCommand, CWD: input.CWD, Shell: strings.TrimSpace(input.Shell),
		Source: strings.TrimSpace(input.Source), CallID: strings.TrimSpace(input.CallID), SessionHash: strings.TrimSpace(input.SessionHash),
		ReceivedByInstanceID: strings.TrimSpace(input.ReceivedByInstanceID), ExecutedByInstanceID: strings.TrimSpace(input.ExecutedByInstanceID),
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Status: ExecutionStatusRunning,
	}, subs: map[*ExecutionSubscription]struct{}{}}
	h.executions[id] = record
	h.order = append(h.order, id)
	h.pruneLocked()
	h.mu.Unlock()
	h.publishFeed(ExecutionFeedEvent{Type: ExecutionEventStarted, ExecutionID: id, WorkspaceID: record.info.WorkspaceID, Execution: executionInfoPtr(record.info), Status: ExecutionStatusRunning, Timestamp: record.info.StartedAt})
	return &ExecutionRun{hub: h, record: record}
}

func (h *ExecutionHub) List(workspaceID string, limit int) []ExecutionInfo {
	if h == nil {
		return []ExecutionInfo{}
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if limit <= 0 || limit > h.maxRecent {
		limit = h.maxRecent
	}
	h.mu.RLock()
	result := make([]ExecutionInfo, 0, limit)
	for index := len(h.order) - 1; index >= 0 && len(result) < limit; index-- {
		record := h.executions[h.order[index]]
		if record == nil {
			continue
		}
		record.mu.Lock()
		info := cloneExecutionInfo(record.info)
		record.mu.Unlock()
		if workspaceID == "" || info.WorkspaceID == workspaceID {
			result = append(result, info)
		}
	}
	h.mu.RUnlock()
	return result
}

func (h *ExecutionHub) Get(workspaceID, id string) (ExecutionSnapshot, error) {
	record, err := h.record(workspaceID, id)
	if err != nil {
		return ExecutionSnapshot{}, err
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	return record.snapshotLocked(), nil
}

func (h *ExecutionHub) Subscribe(workspaceID, id string) (*ExecutionSubscription, ExecutionSnapshot, error) {
	record, err := h.record(workspaceID, id)
	if err != nil {
		return nil, ExecutionSnapshot{}, err
	}
	record.mu.Lock()
	sub := &ExecutionSubscription{Events: make(chan ExecutionEvent, executionSubscriberBuffer), Overflow: make(chan ExecutionOverflow, 1), record: record}
	record.subs[sub] = struct{}{}
	snapshot := record.snapshotLocked()
	record.mu.Unlock()
	return sub, snapshot, nil
}

func (h *ExecutionHub) Unsubscribe(sub *ExecutionSubscription) {
	if h == nil || sub == nil || sub.record == nil {
		return
	}
	record := sub.record
	record.mu.Lock()
	if !sub.closed {
		delete(record.subs, sub)
		close(sub.Events)
		close(sub.Overflow)
		sub.closed = true
	}
	record.mu.Unlock()
	h.mu.Lock()
	h.pruneLocked()
	h.mu.Unlock()
}

func (h *ExecutionHub) SubscribeFeed(workspaceID string) (*ExecutionFeedSubscription, ExecutionFeedSnapshot) {
	if h == nil {
		return nil, ExecutionFeedSnapshot{Events: []ExecutionFeedEvent{}}
	}
	workspaceID = strings.TrimSpace(workspaceID)
	h.feedMu.Lock()
	sub := &ExecutionFeedSubscription{Events: make(chan ExecutionFeedEvent, executionFeedBuffer), Overflow: make(chan ExecutionOverflow, 1), workspaceID: workspaceID}
	h.feedSubs[sub] = struct{}{}
	events := make([]ExecutionFeedEvent, 0, len(h.feed))
	for _, event := range h.feed {
		if workspaceID == "" || event.WorkspaceID == workspaceID {
			events = append(events, cloneExecutionFeedEvent(event))
		}
	}
	snapshot := ExecutionFeedSnapshot{Events: events, LatestSequence: h.feedSequence}
	h.feedMu.Unlock()
	return sub, snapshot
}

func (h *ExecutionHub) UnsubscribeFeed(sub *ExecutionFeedSubscription) {
	if h == nil || sub == nil {
		return
	}
	h.feedMu.Lock()
	if !sub.closed {
		delete(h.feedSubs, sub)
		close(sub.Events)
		close(sub.Overflow)
		sub.closed = true
	}
	h.feedMu.Unlock()
}

func (r *ExecutionRun) ID() string {
	if r == nil || r.record == nil {
		return ""
	}
	return r.record.info.ID
}

func (r *ExecutionRun) Writer(stream string) *executionWriter {
	return &executionWriter{run: r, stream: stream}
}

func (r *ExecutionRun) Finish(status string, exitCode *int, timedOut bool) {
	if r == nil || r.record == nil {
		return
	}
	record := r.record
	record.mu.Lock()
	if record.info.Status != ExecutionStatusRunning {
		record.mu.Unlock()
		return
	}
	record.info.Status = status
	record.info.ExitCode = cloneInt(exitCode)
	record.info.TimedOut = timedOut
	record.info.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	record.sequence++
	event := ExecutionEvent{
		Sequence: record.sequence, Type: ExecutionEventCompleted, ExecutionID: record.info.ID, Status: status,
		ExitCode: cloneInt(exitCode), TimedOut: timedOut, Timestamp: record.info.FinishedAt,
	}
	record.publishLocked(event)
	feedEvent := ExecutionFeedEvent{Type: ExecutionEventCompleted, ExecutionID: record.info.ID, WorkspaceID: record.info.WorkspaceID, Execution: executionInfoPtr(record.info), Status: status, ExitCode: cloneInt(exitCode), TimedOut: timedOut, Timestamp: record.info.FinishedAt}
	if r.hub != nil {
		r.hub.publishFeed(feedEvent)
	}
	record.mu.Unlock()
	if r.hub != nil {
		r.hub.mu.Lock()
		r.hub.pruneLocked()
		r.hub.mu.Unlock()
	}
}

func (w *executionWriter) Write(data []byte) (int, error) {
	if w == nil || w.run == nil || w.run.record == nil || len(data) == 0 {
		return len(data), nil
	}
	record := w.run.record
	record.mu.Lock()
	if w.stream == "stderr" {
		record.stderr = appendExecutionTail(record.stderr, data)
	} else {
		record.stdout = appendExecutionTail(record.stdout, data)
	}
	for _, chunk := range splitExecutionOutput(strings.ToValidUTF8(string(data), "�")) {
		record.sequence++
		event := ExecutionEvent{Sequence: record.sequence, Type: ExecutionEventOutput, ExecutionID: record.info.ID, Stream: w.stream, Data: chunk, Timestamp: time.Now().UTC().Format(time.RFC3339Nano)}
		record.publishLocked(event)
		if w.run.hub != nil {
			w.run.hub.publishFeed(ExecutionFeedEvent{Type: ExecutionEventOutput, ExecutionID: record.info.ID, WorkspaceID: record.info.WorkspaceID, Execution: executionInfoPtr(record.info), Stream: event.Stream, Data: event.Data, Timestamp: event.Timestamp})
		}
	}
	record.mu.Unlock()
	return len(data), nil
}

func (h *ExecutionHub) record(workspaceID, id string) (*executionRecord, error) {
	if h == nil {
		return nil, ErrExecutionNotFound
	}
	h.mu.RLock()
	record := h.executions[strings.TrimSpace(id)]
	h.mu.RUnlock()
	if record == nil {
		return nil, ErrExecutionNotFound
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID != "" {
		record.mu.Lock()
		matched := record.info.WorkspaceID == workspaceID
		record.mu.Unlock()
		if !matched {
			return nil, ErrExecutionNotFound
		}
	}
	return record, nil
}

func (h *ExecutionHub) pruneLocked() {
	if h == nil || len(h.order) <= h.maxRecent {
		return
	}
	kept := make([]string, 0, len(h.order))
	remove := len(h.order) - h.maxRecent
	for _, id := range h.order {
		record := h.executions[id]
		if remove > 0 && record != nil {
			record.mu.Lock()
			canRemove := record.info.Status != ExecutionStatusRunning && len(record.subs) == 0
			record.mu.Unlock()
			if canRemove {
				delete(h.executions, id)
				remove--
				continue
			}
		}
		kept = append(kept, id)
	}
	h.order = kept
}

func (r *executionRecord) snapshotLocked() ExecutionSnapshot {
	return ExecutionSnapshot{Execution: cloneExecutionInfo(r.info), Stdout: string(r.stdout), Stderr: string(r.stderr), LatestSequence: r.sequence}
}

func (r *executionRecord) publishLocked(event ExecutionEvent) {
	for sub := range r.subs {
		if sub.closed || sub.overflow {
			continue
		}
		select {
		case sub.Events <- event:
		default:
			sub.overflow = true
			sub.Overflow <- ExecutionOverflow{DroppedSequence: event.Sequence}
		}
	}
}

func (h *ExecutionHub) publishFeed(event ExecutionFeedEvent) {
	if h == nil {
		return
	}
	h.feedMu.Lock()
	h.feedSequence++
	event.Sequence = h.feedSequence
	event = cloneExecutionFeedEvent(event)
	h.feed = append(h.feed, event)
	h.pruneFeedLocked()
	for sub := range h.feedSubs {
		if sub.closed || sub.overflow || (sub.workspaceID != "" && sub.workspaceID != event.WorkspaceID) {
			continue
		}
		select {
		case sub.Events <- cloneExecutionFeedEvent(event):
		default:
			sub.overflow = true
			sub.Overflow <- ExecutionOverflow{DroppedSequence: event.Sequence}
		}
	}
	h.feedMu.Unlock()
}

func (h *ExecutionHub) pruneFeedLocked() {
	if len(h.feed) <= MaxExecutionFeedEvents {
		return
	}
	h.feed = append([]ExecutionFeedEvent(nil), h.feed[len(h.feed)-MaxExecutionFeedEvents:]...)
}

func splitExecutionOutput(value string) []string {
	if value == "" {
		return nil
	}
	chunks := make([]string, 0, (len(value)+maxExecutionEventBytes-1)/maxExecutionEventBytes)
	for len(value) > maxExecutionEventBytes {
		end := maxExecutionEventBytes
		for end > 0 && !utf8.RuneStart(value[end]) {
			end--
		}
		chunks = append(chunks, value[:end])
		value = value[end:]
	}
	if value != "" {
		chunks = append(chunks, value)
	}
	return chunks
}

func appendExecutionTail(existing, data []byte) []byte {
	existing = append(existing, data...)
	if len(existing) <= maxExecutionLogBytes {
		return existing
	}
	return append([]byte(nil), existing[len(existing)-maxExecutionLogBytes:]...)
}

func cloneExecutionInfo(value ExecutionInfo) ExecutionInfo {
	value.ExitCode = cloneInt(value.ExitCode)
	return value
}

func executionInfoPtr(value ExecutionInfo) *ExecutionInfo {
	cloned := cloneExecutionInfo(value)
	return &cloned
}

func cloneExecutionFeedEvent(value ExecutionFeedEvent) ExecutionFeedEvent {
	value.ExitCode = cloneInt(value.ExitCode)
	if value.Execution != nil {
		value.Execution = executionInfoPtr(*value.Execution)
	}
	return value
}
