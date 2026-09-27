package product

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	DefaultQueueEvents    = 400
	DefaultQueueBytes     = 256 * 1024
	DefaultFlushInterval  = 5 * time.Second
	DefaultRequestTimeout = 2 * time.Second
)

type DiagnosticCode string

const (
	DiagnosticInvalidEvent    DiagnosticCode = "invalid_event"
	DiagnosticQueueOverflow   DiagnosticCode = "queue_overflow"
	DiagnosticSerialization   DiagnosticCode = "serialization_failed"
	DiagnosticTimeout         DiagnosticCode = "timeout"
	DiagnosticTransport       DiagnosticCode = "transport_failed"
	DiagnosticRejected        DiagnosticCode = "rejected"
	DiagnosticInvalidResponse DiagnosticCode = "invalid_response"
)

type Diagnostic struct {
	Code       DiagnosticCode
	EventCount int
	HTTPStatus int
}

type DiagnosticObserver func(Diagnostic)

type ClientOptions struct {
	Endpoint       string
	HTTPClient     *http.Client
	Enabled        bool
	QueueMaxEvents int
	QueueMaxBytes  int
	FlushInterval  time.Duration
	RequestTimeout time.Duration
	Observe        DiagnosticObserver
}

type queuedEvent struct {
	event Event
	bytes int
}

type flushRequest struct {
	ctx  context.Context
	done chan struct{}
	stop bool
}

type Client struct {
	endpoint       string
	httpClient     *http.Client
	queueMaxEvents int
	queueMaxBytes  int
	requestTimeout time.Duration
	observe        DiagnosticObserver

	mu         sync.Mutex
	enabled    bool
	closed     bool
	queue      []queuedEvent
	queueBytes int
	sendCancel context.CancelFunc

	wake     chan struct{}
	requests chan flushRequest
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

func NewClient(options ClientOptions) (*Client, error) {
	meta, err := ParseEndpoint(options.Endpoint)
	if err != nil {
		return nil, err
	}
	client := &Client{
		endpoint:       options.Endpoint,
		httpClient:     options.HTTPClient,
		enabled:        options.Enabled,
		queueMaxEvents: options.QueueMaxEvents,
		queueMaxBytes:  options.QueueMaxBytes,
		requestTimeout: options.RequestTimeout,
		observe:        options.Observe,
	}
	if client.httpClient == nil {
		client.httpClient = http.DefaultClient
	}
	if client.queueMaxEvents <= 0 {
		client.queueMaxEvents = DefaultQueueEvents
	}
	if client.queueMaxBytes <= 0 {
		client.queueMaxBytes = DefaultQueueBytes
	}
	if client.requestTimeout <= 0 {
		client.requestTimeout = DefaultRequestTimeout
	}
	if !meta.Available {
		return client, nil
	}
	interval := options.FlushInterval
	if interval <= 0 {
		interval = DefaultFlushInterval
	}
	client.wake = make(chan struct{}, 1)
	client.requests = make(chan flushRequest, 1)
	client.stop = make(chan struct{})
	client.done = make(chan struct{})
	go client.run(interval)
	return client, nil
}

func (client *Client) Active() bool {
	if client == nil {
		return false
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.enabled && !client.closed && client.done != nil
}

func (client *Client) Enqueue(event Event) bool {
	if client == nil {
		return false
	}
	if err := event.Validate(); err != nil {
		client.emit(Diagnostic{Code: DiagnosticInvalidEvent, EventCount: 1})
		return false
	}
	data, err := json.Marshal(event)
	if err != nil {
		client.emit(Diagnostic{Code: DiagnosticSerialization, EventCount: 1})
		return false
	}
	client.mu.Lock()
	if !client.enabled || client.closed || client.done == nil {
		client.mu.Unlock()
		return false
	}
	if len(client.queue) >= client.queueMaxEvents || client.queueBytes+len(data) > client.queueMaxBytes {
		client.mu.Unlock()
		client.emit(Diagnostic{Code: DiagnosticQueueOverflow, EventCount: 1})
		return false
	}
	client.queue = append(client.queue, queuedEvent{event: event, bytes: len(data)})
	client.queueBytes += len(data)
	shouldWake := len(client.queue) >= MaxBatchEvents || client.queueBytes >= MaxBodyBytes
	client.mu.Unlock()
	if shouldWake {
		client.signalWake()
	}
	return true
}

func (client *Client) SetEnabled(enabled bool) {
	if client == nil {
		return
	}
	client.mu.Lock()
	client.enabled = enabled
	if !enabled {
		client.queue = nil
		client.queueBytes = 0
		if client.sendCancel != nil {
			client.sendCancel()
		}
	}
	client.mu.Unlock()
}

func (client *Client) Flush(ctx context.Context) {
	if client == nil || client.done == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	request := flushRequest{ctx: ctx, done: make(chan struct{})}
	select {
	case client.requests <- request:
	case <-ctx.Done():
		return
	case <-client.done:
		return
	}
	select {
	case <-request.done:
	case <-ctx.Done():
	case <-client.done:
	}
}

func (client *Client) Close(ctx context.Context) {
	if client == nil || client.done == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	client.mu.Lock()
	if client.closed {
		done := client.done
		client.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			client.forceStop()
		}
		return
	}
	client.closed = true
	client.mu.Unlock()

	request := flushRequest{ctx: ctx, done: make(chan struct{}), stop: true}
	select {
	case client.requests <- request:
	case <-ctx.Done():
		client.forceStop()
		return
	case <-client.done:
		return
	}
	select {
	case <-request.done:
	case <-ctx.Done():
		client.forceStop()
	case <-client.done:
	}
}

func (client *Client) Pending() int {
	if client == nil {
		return 0
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	return len(client.queue)
}

func (client *Client) run(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer close(client.done)
	for {
		select {
		case <-ticker.C:
			client.flush(context.Background())
		case <-client.wake:
			client.flush(context.Background())
		case request := <-client.requests:
			client.flush(request.ctx)
			close(request.done)
			if request.stop {
				return
			}
		case <-client.stop:
			return
		}
	}
}

func (client *Client) flush(ctx context.Context) {
	for {
		events := client.takeBatch()
		if len(events) == 0 {
			return
		}
		client.send(ctx, events)
		if ctx != nil && ctx.Err() != nil {
			return
		}
	}
}

func (client *Client) takeBatch() []Event {
	client.mu.Lock()
	defer client.mu.Unlock()
	if !client.enabled || len(client.queue) == 0 {
		return nil
	}
	count := min(len(client.queue), MaxBatchEvents)
	for count > 0 {
		events := make([]Event, count)
		for i := range count {
			events[i] = client.queue[i].event
		}
		if _, err := NewBatch(events); err == nil {
			removedBytes := 0
			for _, item := range client.queue[:count] {
				removedBytes += item.bytes
			}
			client.queue = append([]queuedEvent(nil), client.queue[count:]...)
			client.queueBytes -= removedBytes
			return events
		}
		count--
	}
	droppedBytes := client.queue[0].bytes
	client.queue = client.queue[1:]
	client.queueBytes -= droppedBytes
	return nil
}

func (client *Client) send(parent context.Context, events []Event) {
	batch, err := NewBatch(events)
	if err != nil {
		client.emit(Diagnostic{Code: DiagnosticSerialization, EventCount: len(events)})
		return
	}
	body, err := json.Marshal(batch)
	if err != nil || len(body) > MaxBodyBytes {
		client.emit(Diagnostic{Code: DiagnosticSerialization, EventCount: len(events)})
		return
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, client.requestTimeout)
	client.mu.Lock()
	if !client.enabled {
		client.mu.Unlock()
		cancel()
		return
	}
	client.sendCancel = cancel
	client.mu.Unlock()
	defer func() {
		client.mu.Lock()
		client.sendCancel = nil
		client.mu.Unlock()
		cancel()
	}()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint, bytes.NewReader(body))
	if err != nil {
		client.emit(Diagnostic{Code: DiagnosticTransport, EventCount: len(events)})
		return
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			client.emit(Diagnostic{Code: DiagnosticTimeout, EventCount: len(events)})
		} else {
			client.emit(Diagnostic{Code: DiagnosticTransport, EventCount: len(events)})
		}
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
		client.emit(Diagnostic{Code: DiagnosticRejected, EventCount: len(events), HTTPStatus: response.StatusCode})
		return
	}
	var accepted struct {
		Accepted int `json:"accepted"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1024))
	if decoder.Decode(&accepted) != nil || accepted.Accepted != len(events) {
		client.emit(Diagnostic{Code: DiagnosticInvalidResponse, EventCount: len(events), HTTPStatus: response.StatusCode})
	}
}

func (client *Client) signalWake() {
	if client == nil || client.wake == nil {
		return
	}
	select {
	case client.wake <- struct{}{}:
	default:
	}
}

func (client *Client) forceStop() {
	if client == nil || client.stop == nil {
		return
	}
	client.mu.Lock()
	if client.sendCancel != nil {
		client.sendCancel()
	}
	client.mu.Unlock()
	client.stopOnce.Do(func() { close(client.stop) })
}

func (client *Client) emit(diagnostic Diagnostic) {
	if client == nil || client.observe == nil {
		return
	}
	client.observe(diagnostic)
}
