package shell

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"go.mewis.me/codemcp/internal/idgen"
	"go.mewis.me/codemcp/internal/sequence"
	statepkg "go.mewis.me/codemcp/internal/state"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const (
	maxExecutionLogBytes       = 400_000
	MaxExecutionFeedEvents     = 1024
	MaxRecentExecutions        = 1024
	maxExecutionEventBytes     = 8 << 10
	executionSubscriberBuffer  = 64
	executionFeedBuffer        = 128
	ExecutionStatusRunning     = "running"
	ExecutionStatusSuccess     = "success"
	ExecutionStatusFailed      = "failed"
	ExecutionStatusCancelled   = "cancelled"
	ExecutionStatusTimedOut    = "timed_out"
	ExecutionStatusInterrupted = "interrupted"
	ExecutionEventStarted      = "started"
	ExecutionEventOutput       = "output"
	ExecutionEventCompleted    = "completed"
	executionStoreVersion      = 1
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
	Executions     []ExecutionInfo      `json:"executions,omitempty"`
	LatestSequence uint64               `json:"latest_sequence"`
}

type ExecutionOverflow = sequence.Overflow

type ExecutionDiagnostics struct {
	Running              int   `json:"running"`
	OldestRunningAgeMS   int64 `json:"oldest_running_age_ms,omitempty"`
	FeedSubscribers      int   `json:"feed_subscribers"`
	FeedOverflowed       int   `json:"feed_overflowed"`
	ExecutionSubscribers int   `json:"execution_subscribers"`
	ExecutionOverflowed  int   `json:"execution_overflowed"`
}

type ExecutionSubscription = sequence.Subscription[ExecutionEvent]
type ExecutionFeedSubscription = sequence.Subscription[ExecutionFeedEvent]

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
	mu         sync.RWMutex
	executions map[string]*executionRecord
	order      []string
	maxRecent  int
	feed       *sequence.Stream[ExecutionFeedEvent]
	storePath  string
	closeOnce  sync.Once
}

type executionStoreFile struct {
	Version    int                 `json:"version"`
	Executions []ExecutionSnapshot `json:"executions"`
}

type executionRecord struct {
	mu     sync.Mutex
	info   ExecutionInfo
	stdout []byte
	stderr []byte
	stream *sequence.Stream[ExecutionEvent]
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
	Source                string
	CallID                string
	SessionHash           string
	ReceivedByInstanceID  string
	ExecutedByInstanceID  string
	SuppressNotifications bool
}

func NewExecutionHub() *ExecutionHub {
	return &ExecutionHub{
		executions: map[string]*executionRecord{},
		maxRecent:  MaxRecentExecutions,
		feed: sequence.New[ExecutionFeedEvent](MaxExecutionFeedEvents, executionFeedBuffer, func(event *ExecutionFeedEvent, value uint64) {
			event.Sequence = value
		}),
	}
}

func NewPersistentExecutionHub(path string) (*ExecutionHub, error) {
	h := NewExecutionHub()
	h.storePath = strings.TrimSpace(path)
	if err := h.load(); err != nil {
		return nil, err
	}
	return h, nil
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

func ExecutionMetadataFromContext(ctx context.Context) ExecutionMetadata {
	return executionMetadata(ctx)
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
		ID: id, WorkspaceID: strings.TrimSpace(input.WorkspaceID), Tool: tool,
		Command: tracepkg.SanitizeCommand(input.Command), RequestedCommand: tracepkg.SanitizeCommand(input.RequestedCommand), EffectiveCommand: tracepkg.SanitizeCommand(input.EffectiveCommand), SecurityCommand: tracepkg.SanitizeCommand(input.SecurityCommand),
		CWD: input.CWD, Shell: strings.TrimSpace(input.Shell),
		Source: strings.TrimSpace(input.Source), CallID: strings.TrimSpace(input.CallID), SessionHash: strings.TrimSpace(input.SessionHash),
		ReceivedByInstanceID: strings.TrimSpace(input.ReceivedByInstanceID), ExecutedByInstanceID: strings.TrimSpace(input.ExecutedByInstanceID),
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Status: ExecutionStatusRunning,
	}, stream: sequence.New[ExecutionEvent](0, executionSubscriberBuffer, func(event *ExecutionEvent, value uint64) {
		event.Sequence = value
	})}
	h.executions[id] = record
	h.order = append(h.order, id)
	h.pruneLocked()
	h.mu.Unlock()
	_ = h.persist()
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
	sub, _ := record.stream.Subscribe(nil, 0)
	snapshot := record.snapshotLocked()
	record.mu.Unlock()
	return sub, snapshot, nil
}

func (h *ExecutionHub) Unsubscribe(sub *ExecutionSubscription) {
	if h == nil || sub == nil {
		return
	}
	sub.Close()
	h.mu.Lock()
	h.pruneLocked()
	h.mu.Unlock()
}

func (h *ExecutionHub) SubscribeFeed(workspaceID string) (*ExecutionFeedSubscription, ExecutionFeedSnapshot) {
	if h == nil {
		return nil, ExecutionFeedSnapshot{Events: []ExecutionFeedEvent{}}
	}
	workspaceID = strings.TrimSpace(workspaceID)
	var filter sequence.Predicate[ExecutionFeedEvent]
	if workspaceID != "" {
		filter = func(event ExecutionFeedEvent) bool { return event.WorkspaceID == workspaceID }
	}
	sub, feedSnapshot := h.feed.Subscribe(filter, MaxExecutionFeedEvents)
	events := make([]ExecutionFeedEvent, len(feedSnapshot.Events))
	for index := range feedSnapshot.Events {
		events[index] = cloneExecutionFeedEvent(feedSnapshot.Events[index])
	}
	snapshot := ExecutionFeedSnapshot{Events: events, Executions: h.List(workspaceID, MaxRecentExecutions), LatestSequence: feedSnapshot.LatestSequence}
	return sub, snapshot
}

func (h *ExecutionHub) UnsubscribeFeed(sub *ExecutionFeedSubscription) {
	if sub != nil {
		sub.Close()
	}
}

func (h *ExecutionHub) Diagnostics() ExecutionDiagnostics {
	if h == nil {
		return ExecutionDiagnostics{}
	}
	now := time.Now().UTC()
	h.mu.RLock()
	records := make([]*executionRecord, 0, len(h.executions))
	for _, record := range h.executions {
		records = append(records, record)
	}
	h.mu.RUnlock()
	result := ExecutionDiagnostics{}
	var oldest time.Time
	for _, record := range records {
		record.mu.Lock()
		if record.info.Status == ExecutionStatusRunning {
			result.Running++
			if started, err := time.Parse(time.RFC3339Nano, record.info.StartedAt); err == nil && (oldest.IsZero() || started.Before(oldest)) {
				oldest = started
			}
		}
		if record.stream != nil {
			result.ExecutionSubscribers += record.stream.SubscriberCount()
			result.ExecutionOverflowed += record.stream.OverflowedCount()
		}
		record.mu.Unlock()
	}
	if !oldest.IsZero() {
		result.OldestRunningAgeMS = max(0, now.Sub(oldest).Milliseconds())
	}
	if h.feed != nil {
		result.FeedSubscribers = h.feed.SubscriberCount()
		result.FeedOverflowed = h.feed.OverflowedCount()
	}
	return result
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
	record.stream.Publish(ExecutionEvent{
		Type: ExecutionEventCompleted, ExecutionID: record.info.ID, Status: status,
		ExitCode: cloneInt(exitCode), TimedOut: timedOut, Timestamp: record.info.FinishedAt,
	})
	feedEvent := ExecutionFeedEvent{Type: ExecutionEventCompleted, ExecutionID: record.info.ID, WorkspaceID: record.info.WorkspaceID, Execution: executionInfoPtr(record.info), Status: status, ExitCode: cloneInt(exitCode), TimedOut: timedOut, Timestamp: record.info.FinishedAt}
	if r.hub != nil {
		r.hub.publishFeed(feedEvent)
	}
	record.mu.Unlock()
	if r.hub != nil {
		r.hub.mu.Lock()
		r.hub.pruneLocked()
		r.hub.mu.Unlock()
		_ = r.hub.persist()
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
		event := record.stream.Publish(ExecutionEvent{Type: ExecutionEventOutput, ExecutionID: record.info.ID, Stream: w.stream, Data: chunk, Timestamp: time.Now().UTC().Format(time.RFC3339Nano)})
		if w.run.hub != nil {
			w.run.hub.publishFeed(ExecutionFeedEvent{Type: ExecutionEventOutput, ExecutionID: record.info.ID, WorkspaceID: record.info.WorkspaceID, Execution: executionInfoPtr(record.info), Stream: event.Stream, Data: event.Data, Timestamp: event.Timestamp})
		}
	}
	record.mu.Unlock()
	if w.run.hub != nil {
		_ = w.run.hub.persist()
	}
	return len(data), nil
}

func (h *ExecutionHub) Close() {
	if h == nil {
		return
	}
	h.closeOnce.Do(func() {
		h.mu.RLock()
		records := make([]*executionRecord, 0, len(h.executions))
		for _, record := range h.executions {
			records = append(records, record)
		}
		h.mu.RUnlock()
		for _, record := range records {
			record.mu.Lock()
			if record.stream != nil {
				record.stream.Close()
			}
			record.mu.Unlock()
		}
		if h.feed != nil {
			h.feed.Close()
		}
		_ = h.persist()
	})
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

func (h *ExecutionHub) load() error {
	if h == nil || h.storePath == "" {
		return nil
	}
	data, err := os.ReadFile(h.storePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var stored executionStoreFile
	if err := json.Unmarshal(data, &stored); err != nil {
		return err
	}
	if stored.Version != executionStoreVersion {
		return errors.New("unsupported execution store version")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	interrupted := false
	for _, snapshot := range stored.Executions {
		info := cloneExecutionInfo(snapshot.Execution)
		if strings.TrimSpace(info.ID) == "" || strings.TrimSpace(info.WorkspaceID) == "" {
			continue
		}
		if info.Status == ExecutionStatusRunning {
			info.Status = ExecutionStatusInterrupted
			info.FinishedAt = now
			info.ExitCode = nil
			info.TimedOut = false
			interrupted = true
		}
		record := &executionRecord{
			info: info, stdout: []byte(snapshot.Stdout), stderr: []byte(snapshot.Stderr),
			stream: sequence.New[ExecutionEvent](0, executionSubscriberBuffer, func(event *ExecutionEvent, value uint64) {
				event.Sequence = value
			}),
		}
		record.stream.EnsureSequence(snapshot.LatestSequence)
		h.executions[info.ID] = record
		h.order = append(h.order, info.ID)
	}
	h.pruneLocked()
	if interrupted {
		return h.persist()
	}
	return nil
}

func (h *ExecutionHub) persist() error {
	if h == nil || h.storePath == "" {
		return nil
	}
	h.mu.RLock()
	order := append([]string(nil), h.order...)
	records := make(map[string]*executionRecord, len(h.executions))
	for id, record := range h.executions {
		records[id] = record
	}
	h.mu.RUnlock()
	snapshots := make([]ExecutionSnapshot, 0, len(order))
	for _, id := range order {
		record := records[id]
		if record == nil {
			continue
		}
		record.mu.Lock()
		snapshots = append(snapshots, record.snapshotLocked())
		record.mu.Unlock()
	}
	data, err := json.MarshalIndent(executionStoreFile{Version: executionStoreVersion, Executions: snapshots}, "", "  ")
	if err != nil {
		return err
	}
	return statepkg.WriteFileAtomic(h.storePath, append(data, '\n'), 0600)
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
			canRemove := record.info.Status != ExecutionStatusRunning && (record.stream == nil || record.stream.SubscriberCount() == 0)
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
	latest := uint64(0)
	if r.stream != nil {
		latest = r.stream.LatestSequence()
	}
	return ExecutionSnapshot{Execution: cloneExecutionInfo(r.info), Stdout: string(r.stdout), Stderr: string(r.stderr), LatestSequence: latest}
}

func (h *ExecutionHub) publishFeed(event ExecutionFeedEvent) {
	if h == nil || h.feed == nil {
		return
	}
	h.feed.Publish(cloneExecutionFeedEvent(event))
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
