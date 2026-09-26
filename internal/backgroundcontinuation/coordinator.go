package backgroundcontinuation

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"go.mewis.me/codemcp/internal/backgrounddelivery"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
)

const (
	defaultCoalesceWindow = 10 * time.Millisecond
	defaultRetryDelay     = 100 * time.Millisecond
	maxRetryDelay         = 30 * time.Second
	defaultOutputTail     = 2048
	maxContextItems       = 16
)

type Mode string

const (
	ModeIdleContinuation Mode = "idle_continuation"
	ModeInFlightSteering Mode = "in_flight_steering"
)

type Capability struct {
	IdleContinuation bool
	InFlightSteering bool
	InFlight         bool
}

type Outcome string

const (
	OutcomeDelivered   Outcome = "delivered"
	OutcomeRetryable   Outcome = "retryable"
	OutcomeUnsupported Outcome = "unsupported"
	OutcomeStale       Outcome = "stale"
	OutcomeClosed      Outcome = "closed"
)

type CompletionContext struct {
	DeliveryID  string
	ProcessID   string
	ExecutionID string
	TaskID      string
	CallID      string
	Status      string
	Reason      string
	Summary     string
	OutputTail  string
}

type Request struct {
	Owner          backgrounddelivery.Owner
	WorkspaceID    string
	Mode           Mode
	IdempotencyKey string
	Completions    []CompletionContext
}

type DeliverResult struct {
	Outcome    Outcome
	RetryAfter time.Duration
}

type Adapter interface {
	ID() string
	Owner(context.Context) (backgrounddelivery.Owner, error)
	Capability(context.Context, backgrounddelivery.Owner) (Capability, error)
	Deliver(context.Context, Request) (DeliverResult, error)
	Commit(context.Context, Request) (DeliverResult, error)
	Close() error
}

type OutputTailProvider interface {
	Tail(context.Context, backgrounddelivery.Delivery, int) string
}

type ProcessOutputTailProvider struct {
	Processes *shellruntime.ProcessManager
}

func (p ProcessOutputTailProvider) Tail(_ context.Context, delivery backgrounddelivery.Delivery, limit int) string {
	if p.Processes == nil || strings.TrimSpace(delivery.WorkspaceID) == "" || strings.TrimSpace(delivery.ProcessID) == "" || limit <= 0 {
		return ""
	}
	result, err := p.Processes.Output(delivery.WorkspaceID, delivery.ProcessID, limit)
	if err != nil {
		return ""
	}
	parts := make([]string, 0, 2)
	if text := strings.TrimSpace(result.Stdout); text != "" {
		parts = append(parts, text)
	}
	if text := strings.TrimSpace(result.Stderr); text != "" {
		parts = append(parts, text)
	}
	return boundUTF8(strings.Join(parts, "\n"), limit)
}

type Coordinator struct {
	Broker         *backgrounddelivery.Broker
	Adapter        Adapter
	Output         OutputTailProvider
	CoalesceWindow time.Duration
	OutputTail     int
	closeOnce      sync.Once
}

func (c *Coordinator) Run(ctx context.Context, workspaceID string) error {
	if c == nil || c.Broker == nil || c.Adapter == nil {
		return errors.New("background continuation coordinator is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return errors.New("background continuation workspace is required")
	}
	owner, err := c.Adapter.Owner(ctx)
	if err != nil {
		return err
	}
	if !owner.Valid() {
		return errors.New("background continuation owner is unavailable")
	}
	adapterID := strings.TrimSpace(c.Adapter.ID())
	if adapterID == "" {
		return errors.New("background continuation adapter id is required")
	}

	events := c.Broker.Subscribe()
	defer c.Broker.Unsubscribe(events)
	trigger := make(chan struct{}, 1)
	trigger <- struct{}{}
	var retry <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-events:
			if !ok {
				return nil
			}
			if event.WorkspaceID == workspaceID && event.OwnerID == owner.ID && event.OwnerGeneration == owner.Generation {
				select {
				case trigger <- struct{}{}:
				default:
				}
			}
		case <-trigger:
			if delay := c.coalesceWindow(); delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
			}
			delay, runErr := c.flush(ctx, workspaceID, owner, adapterID)
			if runErr != nil {
				return runErr
			}
			if delay > 0 {
				retry = time.After(delay)
			}
		case <-retry:
			retry = nil
			select {
			case trigger <- struct{}{}:
			default:
			}
		}
	}
}

func (c *Coordinator) Close() error {
	if c == nil || c.Adapter == nil {
		return nil
	}
	var err error
	c.closeOnce.Do(func() { err = c.Adapter.Close() })
	return err
}

func (c *Coordinator) flush(ctx context.Context, workspaceID string, owner backgrounddelivery.Owner, adapterID string) (time.Duration, error) {
	capability, err := c.Adapter.Capability(ctx, owner)
	if err != nil {
		return 0, err
	}
	mode, supported := selectMode(capability)
	if !supported {
		return 0, nil
	}
	values, err := c.Broker.List(workspaceID, owner)
	if err != nil {
		return 0, err
	}
	pending := make([]backgrounddelivery.Delivery, 0, len(values))
	for _, value := range values {
		if value.State == backgrounddelivery.DeliveryPending ||
			(value.State == backgrounddelivery.DeliveryClaimed && value.Claimant == adapterID) {
			pending = append(pending, value)
		}
	}
	if len(pending) == 0 {
		return 0, nil
	}
	sort.SliceStable(pending, func(i, j int) bool { return pending[i].CreatedAt.Before(pending[j].CreatedAt) })
	if len(pending) > maxContextItems {
		pending = pending[:maxContextItems]
	}

	claimed := make([]backgrounddelivery.ClaimResult, 0, len(pending))
	for _, value := range pending {
		claim, claimErr := c.Broker.Claim(workspaceID, owner, value.ID, adapterID)
		if claimErr != nil {
			if errors.Is(claimErr, backgrounddelivery.ErrDeliveryClaimed) ||
				errors.Is(claimErr, backgrounddelivery.ErrDeliverySuppressed) {
				continue
			}
			return 0, claimErr
		}
		if claim.Acquired && !claim.AlreadyDelivered {
			claimed = append(claimed, claim)
		}
	}
	if len(claimed) == 0 {
		return 0, nil
	}

	request := c.buildRequest(ctx, workspaceID, owner, mode, claimed)
	result, deliverErr := c.Adapter.Deliver(ctx, request)
	if deliverErr != nil {
		return c.retryDelay(result.RetryAfter), nil
	}
	switch result.Outcome {
	case OutcomeDelivered:
		commitResult, commitErr := c.Adapter.Commit(ctx, request)
		if commitErr != nil || commitResult.Outcome == OutcomeRetryable {
			return c.retryDelay(commitResult.RetryAfter), nil
		}
		if commitResult.Outcome != OutcomeDelivered {
			c.release(workspaceID, owner, claimed)
			return 0, nil
		}
		for _, claim := range claimed {
			if _, err := c.Broker.Commit(workspaceID, owner, claim.Delivery.ID, claim.Receipt); err != nil {
				return 0, err
			}
		}
		return 0, nil
	case OutcomeRetryable:
		return c.retryDelay(result.RetryAfter), nil
	case OutcomeUnsupported, OutcomeStale, OutcomeClosed:
		c.release(workspaceID, owner, claimed)
		return 0, nil
	default:
		return c.retryDelay(0), nil
	}
}

func (c *Coordinator) release(workspaceID string, owner backgrounddelivery.Owner, claims []backgrounddelivery.ClaimResult) {
	for _, claim := range claims {
		_, _ = c.Broker.Release(workspaceID, owner, claim.Delivery.ID, claim.Receipt)
	}
}

func (c *Coordinator) buildRequest(ctx context.Context, workspaceID string, owner backgrounddelivery.Owner, mode Mode, claims []backgrounddelivery.ClaimResult) Request {
	contexts := make([]CompletionContext, 0, len(claims))
	keys := make([]string, 0, len(claims))
	for _, claim := range claims {
		delivery := claim.Delivery
		item := CompletionContext{
			DeliveryID:  delivery.ID,
			ProcessID:   delivery.ProcessID,
			ExecutionID: delivery.ExecutionID,
			TaskID:      delivery.TaskID,
			CallID:      delivery.CallID,
			Status:      delivery.Status,
			Reason:      string(delivery.Reason),
			Summary:     completionSummary(delivery),
		}
		if c.Output != nil && c.outputTail() > 0 {
			item.OutputTail = boundUTF8(c.Output.Tail(ctx, delivery, c.outputTail()), c.outputTail())
		}
		contexts = append(contexts, item)
		keys = append(keys, claim.Receipt)
	}
	return Request{
		Owner:          owner,
		WorkspaceID:    workspaceID,
		Mode:           mode,
		IdempotencyKey: strings.Join(keys, "."),
		Completions:    contexts,
	}
}

func selectMode(capability Capability) (Mode, bool) {
	if capability.InFlight && capability.InFlightSteering {
		return ModeInFlightSteering, true
	}
	if capability.IdleContinuation {
		return ModeIdleContinuation, true
	}
	return "", false
}

func completionSummary(value backgrounddelivery.Delivery) string {
	status := strings.TrimSpace(value.Status)
	if status == "" {
		status = "finished"
	}
	reason := strings.TrimSpace(string(value.Reason))
	if reason == "" {
		reason = "terminal"
	}
	return fmt.Sprintf("Background process finished with status %s (%s).", status, reason)
}

func (c *Coordinator) coalesceWindow() time.Duration {
	if c.CoalesceWindow < 0 {
		return 0
	}
	if c.CoalesceWindow == 0 {
		return defaultCoalesceWindow
	}
	return c.CoalesceWindow
}

func (c *Coordinator) outputTail() int {
	if c.OutputTail < 0 {
		return 0
	}
	if c.OutputTail == 0 {
		return defaultOutputTail
	}
	return min(c.OutputTail, defaultOutputTail)
}

func (c *Coordinator) retryDelay(value time.Duration) time.Duration {
	if value <= 0 {
		value = defaultRetryDelay
	}
	return min(value, maxRetryDelay)
}

func boundUTF8(value string, limit int) string {
	if limit <= 0 || value == "" {
		return ""
	}
	value = strings.ToValidUTF8(value, "�")
	if len(value) <= limit {
		return value
	}
	start := len(value) - limit
	for start < len(value) && !utf8.RuneStart(value[start]) {
		start++
	}
	return value[start:]
}
