package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/backgrounddelivery"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"go.mewis.me/codemcp/internal/idgen"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
)

const (
	TasksExtensionID   = "io.modelcontextprotocol/tasks"
	taskDefaultTTL     = time.Hour
	taskPollInterval   = 5 * time.Second
	taskRegistryLimit  = 256
	taskRecentLimit    = 256
	taskRecentTerminal = 10 * time.Minute
)

type TaskStatus string

const (
	TaskStatusWorking   TaskStatus = "working"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusCancelled TaskStatus = "cancelled"
)

type Task struct {
	TaskID         string     `json:"taskId"`
	Status         TaskStatus `json:"status"`
	StatusMessage  string     `json:"statusMessage,omitempty"`
	CreatedAt      string     `json:"createdAt"`
	LastUpdatedAt  string     `json:"lastUpdatedAt"`
	TTLMS          *int64     `json:"ttlMs"`
	PollIntervalMS int64      `json:"pollIntervalMs,omitempty"`
	Result         any        `json:"result,omitempty"`
}

type CreateTaskResult struct {
	sdkmcp.ResultBase
	ResultType string `json:"resultType"`
	Task
}

type GetTaskParams struct {
	sdkmcp.ParamsBase
	TaskID string `json:"taskId"`
}

type GetTaskResult struct {
	sdkmcp.ResultBase
	ResultType string `json:"resultType"`
	Task
}

type UpdateTaskParams struct {
	sdkmcp.ParamsBase
	TaskID         string         `json:"taskId"`
	InputResponses map[string]any `json:"inputResponses"`
}

type CancelTaskParams struct {
	sdkmcp.ParamsBase
	TaskID string `json:"taskId"`
}

type EmptyTaskResult struct {
	sdkmcp.ResultBase
	ResultType string `json:"resultType"`
}

type taskRecord struct {
	task      Task
	processID string
	workspace string
	execution string
	final     *sdkmcp.CallToolResult
	expiresAt time.Time
}

type recentTerminal struct {
	event    shellruntime.BackgroundWorkTerminalEvent
	received time.Time
}

type TaskRegistry struct {
	processes   *shellruntime.ProcessManager
	broker      *backgrounddelivery.Broker
	sub         *shellruntime.BackgroundWorkTerminalSubscription
	mu          sync.Mutex
	tasks       map[string]*taskRecord
	byProcess   map[string]string
	order       []string
	recent      map[string]recentTerminal
	recentOrder []string
	closed      chan struct{}
	closeOnce   sync.Once
	wg          sync.WaitGroup
}

func NewTaskRegistry(processes *shellruntime.ProcessManager, brokers ...*backgrounddelivery.Broker) *TaskRegistry {
	var broker *backgrounddelivery.Broker
	if len(brokers) > 0 {
		broker = brokers[0]
	}
	r := &TaskRegistry{
		processes: processes,
		broker:    broker,
		tasks:     map[string]*taskRecord{},
		byProcess: map[string]string{},
		recent:    map[string]recentTerminal{},
		closed:    make(chan struct{}),
	}
	if processes != nil {
		r.sub = processes.SubscribeTerminal()
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.consume()
		}()
	}
	return r
}

func (r *TaskRegistry) Close() {
	if r == nil {
		return
	}
	r.closeOnce.Do(func() {
		close(r.closed)
		if r.processes != nil && r.sub != nil {
			r.processes.UnsubscribeTerminal(r.sub)
		}
		r.wg.Wait()
	})
}

func (r *TaskRegistry) consume() {
	if r == nil || r.sub == nil {
		return
	}
	for {
		select {
		case event, ok := <-r.sub.Events:
			if !ok {
				return
			}
			r.ApplyTerminal(event)
		case <-r.closed:
			return
		}
	}
}

func (r *TaskRegistry) Create(start shellruntime.StartResult, final *sdkmcp.CallToolResult) (*CreateTaskResult, error) {
	if r == nil {
		return nil, errors.New("task registry is unavailable")
	}
	processID := strings.TrimSpace(start.ID)
	if processID == "" {
		return nil, errors.New("background process id is required for task projection")
	}
	now := time.Now().UTC()
	ttl := taskDefaultTTL.Milliseconds()
	record := &taskRecord{
		task: Task{
			TaskID: idgen.Must("task", 12), Status: TaskStatusWorking,
			StatusMessage: "Background process is running.",
			CreatedAt:     now.Format(time.RFC3339Nano), LastUpdatedAt: now.Format(time.RFC3339Nano),
			TTLMS: &ttl, PollIntervalMS: taskPollInterval.Milliseconds(),
		},
		processID: processID,
		execution: strings.TrimSpace(start.ExecutionID),
		final:     cloneSDKCallToolResult(final),
		expiresAt: now.Add(taskDefaultTTL),
	}
	r.mu.Lock()
	r.pruneLocked(now)
	if existingID := r.byProcess[processID]; existingID != "" {
		existing := r.tasks[existingID]
		result := createTaskResult(existing.task)
		r.mu.Unlock()
		return result, nil
	}
	r.tasks[record.task.TaskID] = record
	r.byProcess[processID] = record.task.TaskID
	if r.broker != nil {
		r.broker.AttachTask(processID, record.task.TaskID)
	}
	r.order = append(r.order, record.task.TaskID)
	if recent, ok := r.recent[processID]; ok {
		r.applyTerminalLocked(record, recent.event, now)
		delete(r.recent, processID)
	}
	result := createTaskResult(record.task)
	r.mu.Unlock()
	return result, nil
}

func (r *TaskRegistry) Get(taskID string) (Task, bool) {
	if r == nil {
		return Task{}, false
	}
	now := time.Now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(now)
	record := r.tasks[strings.TrimSpace(taskID)]
	if record == nil {
		return Task{}, false
	}
	return cloneTask(record.task), true
}

func (r *TaskRegistry) ApplyTerminal(event shellruntime.BackgroundWorkTerminalEvent) {
	if r == nil || strings.TrimSpace(event.ProcessID) == "" {
		return
	}
	now := time.Now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(now)
	if taskID := r.byProcess[event.ProcessID]; taskID != "" {
		if record := r.tasks[taskID]; record != nil {
			r.applyTerminalLocked(record, event, now)
			return
		}
	}
	r.recent[event.ProcessID] = recentTerminal{event: event, received: now}
	r.recentOrder = append(r.recentOrder, event.ProcessID)
	r.pruneRecentLocked(now)
}

func (r *TaskRegistry) applyTerminalLocked(record *taskRecord, event shellruntime.BackgroundWorkTerminalEvent, now time.Time) {
	if record == nil || record.task.Status != TaskStatusWorking {
		return
	}
	record.workspace = strings.TrimSpace(event.WorkspaceID)
	if record.execution == "" {
		record.execution = strings.TrimSpace(event.ExecutionID)
	}
	record.task.LastUpdatedAt = now.Format(time.RFC3339Nano)
	switch event.Reason {
	case shellruntime.BackgroundTerminalStopped, shellruntime.BackgroundTerminalShutdown:
		record.task.Status = TaskStatusCancelled
		record.task.StatusMessage = "Background process was cancelled."
		record.task.Result = nil
	default:
		record.task.Status = TaskStatusCompleted
		record.task.StatusMessage = terminalTaskMessage(event)
		final := cloneSDKCallToolResult(record.final)
		if final != nil && event.Reason != shellruntime.BackgroundTerminalExit {
			final.IsError = true
			final.Content = []sdkmcp.Content{&sdkmcp.TextContent{Text: record.task.StatusMessage}}
		}
		record.task.Result = final
	}
}

func (r *TaskRegistry) pruneLocked(now time.Time) {
	for len(r.order) > 0 {
		id := r.order[0]
		record := r.tasks[id]
		if record != nil && len(r.tasks) <= taskRegistryLimit && now.Before(record.expiresAt) {
			break
		}
		r.order = r.order[1:]
		if record != nil {
			delete(r.byProcess, record.processID)
			delete(r.tasks, id)
		}
	}
	r.pruneRecentLocked(now)
}

func (r *TaskRegistry) pruneRecentLocked(now time.Time) {
	for len(r.recentOrder) > 0 {
		processID := r.recentOrder[0]
		recent, exists := r.recent[processID]
		if exists && len(r.recent) <= taskRecentLimit && now.Sub(recent.received) < taskRecentTerminal {
			break
		}
		r.recentOrder = r.recentOrder[1:]
		delete(r.recent, processID)
	}
}

func terminalTaskMessage(event shellruntime.BackgroundWorkTerminalEvent) string {
	switch event.Reason {
	case shellruntime.BackgroundTerminalExit:
		return "Background process completed."
	case shellruntime.BackgroundTerminalTimeout:
		return "Background process timed out."
	case shellruntime.BackgroundTerminalSignal:
		return "Background process exited after a signal."
	case shellruntime.BackgroundTerminalFailure:
		return "Background process failed."
	default:
		return "Background process finished."
	}
}

func createTaskResult(task Task) *CreateTaskResult {
	return &CreateTaskResult{ResultType: "task", Task: cloneTask(task)}
}

func cloneTask(task Task) Task {
	if task.TTLMS != nil {
		value := *task.TTLMS
		task.TTLMS = &value
	}
	if result, ok := task.Result.(*sdkmcp.CallToolResult); ok {
		task.Result = cloneSDKCallToolResult(result)
	}
	return task
}

func cloneSDKCallToolResult(value *sdkmcp.CallToolResult) *sdkmcp.CallToolResult {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var result sdkmcp.CallToolResult
	if json.Unmarshal(data, &result) != nil {
		return nil
	}
	return &result
}

func taskExtensionNegotiated(profile Profile, meta map[string]any) bool {
	if !ProfileBackgroundCapabilities(profile).TaskObservation {
		return false
	}
	raw := meta[sdkmcp.MetaKeyClientCapabilities]
	if raw == nil {
		return false
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return false
	}
	var capabilities struct {
		Extensions map[string]any `json:"extensions"`
	}
	if json.Unmarshal(data, &capabilities) != nil {
		return false
	}
	_, ok := capabilities.Extensions[TasksExtensionID]
	return ok
}

func InstallTaskProjection(server *sdkmcp.Server, registry *TaskRegistry, profiles ...Profile) error {
	if server == nil || registry == nil {
		return nil
	}
	profile := Profile(BaseProfile())
	if len(profiles) > 0 && profiles[0] != nil {
		profile = profiles[0]
	}
	if !ProfileBackgroundCapabilities(profile).TaskObservation {
		return nil
	}
	server.AddReceivingMiddleware(func(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
		return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil || method != "tools/call" || req == nil || !taskExtensionNegotiated(profile, req.GetParams().GetMeta()) {
				return result, err
			}
			params, ok := req.GetParams().(*sdkmcp.CallToolParamsRaw)
			if !ok || params == nil || params.Name != "start_process" {
				return result, nil
			}
			callResult, ok := result.(*sdkmcp.CallToolResult)
			if !ok || callResult == nil || callResult.IsError {
				return result, nil
			}
			var start shellruntime.StartResult
			data, marshalErr := json.Marshal(callResult.StructuredContent)
			if marshalErr != nil || json.Unmarshal(data, &start) != nil || strings.TrimSpace(start.ID) == "" {
				return result, nil
			}
			return registry.Create(start, callResult)
		}
	})
	if err := sdkmcp.AddReceivingCustomMethod(server, "tasks/get", func(ctx context.Context, session *sdkmcp.ServerSession, params *GetTaskParams) (*GetTaskResult, error) {
		if params == nil || !taskExtensionNegotiated(profile, params.GetMeta()) {
			return nil, &jsonrpc.Error{Code: -32021, Message: "MCP Tasks capability is required"}
		}
		task, ok := registry.Get(params.TaskID)
		if !ok {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "task not found or expired"}
		}
		return &GetTaskResult{ResultType: "complete", Task: task}, nil
	}); err != nil {
		return err
	}
	if err := sdkmcp.AddReceivingCustomMethod(server, "tasks/update", func(ctx context.Context, session *sdkmcp.ServerSession, params *UpdateTaskParams) (*EmptyTaskResult, error) {
		if params == nil || !taskExtensionNegotiated(profile, params.GetMeta()) {
			return nil, &jsonrpc.Error{Code: -32021, Message: "MCP Tasks capability is required"}
		}
		if _, ok := registry.Get(params.TaskID); !ok {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "task not found or expired"}
		}
		return &EmptyTaskResult{ResultType: "complete"}, nil
	}); err != nil {
		return err
	}
	if err := sdkmcp.AddReceivingCustomMethod(server, "tasks/cancel", func(ctx context.Context, session *sdkmcp.ServerSession, params *CancelTaskParams) (*EmptyTaskResult, error) {
		if params == nil || !taskExtensionNegotiated(profile, params.GetMeta()) {
			return nil, &jsonrpc.Error{Code: -32021, Message: "MCP Tasks capability is required"}
		}
		if _, ok := registry.Get(params.TaskID); !ok {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "task not found or expired"}
		}
		return &EmptyTaskResult{ResultType: "complete"}, nil
	}); err != nil {
		return err
	}
	return nil
}
