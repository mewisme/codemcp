package application

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/explain"
	"go.mewis.me/codemcp/internal/llm"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const (
	maxApprovalExplainCommandBytes = explain.MaxCommandBytes
	maxApprovalExplainSummaryRunes = explain.MaxSummaryRunes
	maxApprovalExplainListItems    = explain.MaxListItems
	maxApprovalExplainItemRunes    = explain.MaxItemRunes
)

type ApprovalExplanationState string

const (
	ApprovalExplanationNone    ApprovalExplanationState = "none"
	ApprovalExplanationPending ApprovalExplanationState = "pending"
	ApprovalExplanationReady   ApprovalExplanationState = "ready"
	ApprovalExplanationFailed  ApprovalExplanationState = "failed"
)

type ApprovalExplanation = explain.Explanation

type ApprovalExplanationResult struct {
	RequestID   string                   `json:"request_id"`
	State       ApprovalExplanationState `json:"state"`
	Attempt     uint64                   `json:"attempt,omitempty"`
	Explanation *ApprovalExplanation     `json:"explanation,omitempty"`
	Failure     string                   `json:"failure,omitempty"`
	UpdatedAt   time.Time                `json:"updated_at,omitempty"`
}

type ApprovalExplainStatus struct {
	Mode           config.ExplainMode `json:"mode"`
	Available      bool               `json:"available"`
	ActiveProvider llm.ProviderID     `json:"active_provider,omitempty"`
	Model          string             `json:"model,omitempty"`
	Configured     bool               `json:"configured"`
	Readiness      llm.Readiness      `json:"readiness"`
	Reason         string             `json:"reason,omitempty"`
}

type ApprovalExplainInput struct {
	ID    string `json:"id"`
	Retry bool   `json:"retry,omitempty"`
}

type ApprovalExplanationReadInput struct {
	ID string `json:"id"`
}

type ApprovalExplainService struct {
	manager   *approval.Manager
	llm       *LLMService
	explainer *explain.Service
	mode      func() config.ExplainMode
	now       func() time.Time

	mu       sync.Mutex
	records  map[string]ApprovalExplanationResult
	inflight map[string]context.CancelFunc
	attempts map[string]uint64

	lifecycleMu sync.Mutex
	cancel      context.CancelFunc
	runCtx      context.Context
	sub         *approval.EventSubscription
	stopping    bool
	wg          sync.WaitGroup
}

func NewApprovalExplainService(manager *approval.Manager, llmService *LLMService, mode func() config.ExplainMode, explainer ...*explain.Service) *ApprovalExplainService {
	var inference LLMInferenceFacade
	if llmService != nil {
		inference = llmService.InferenceFacade()
	}
	service := newApprovalExplainService(manager, llmService, inference, mode)
	if len(explainer) > 0 && explainer[0] != nil {
		service.explainer = explainer[0]
	}
	return service
}

func newApprovalExplainService(manager *approval.Manager, llmService *LLMService, inference LLMInferenceFacade, mode func() config.ExplainMode) *ApprovalExplainService {
	if mode == nil {
		mode = func() config.ExplainMode { return config.ExplainOff }
	}
	service := &ApprovalExplainService{
		manager: manager, llm: llmService, mode: mode, now: func() time.Time { return time.Now().UTC() },
		records: map[string]ApprovalExplanationResult{}, inflight: map[string]context.CancelFunc{}, attempts: map[string]uint64{},
	}
	service.explainer = explain.NewService(explain.Options{Inference: inference, StructuredOutput: service.shouldUseStructuredExplanation})
	return service
}

func (s *ApprovalExplainService) Start(parent context.Context) error {
	if s == nil || s.manager == nil {
		return errors.New("approval Explain service is unavailable")
	}
	if parent == nil {
		parent = context.Background()
	}
	s.lifecycleMu.Lock()
	if s.cancel != nil {
		s.lifecycleMu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	sub := s.manager.Events().Subscribe()
	s.cancel, s.runCtx, s.sub, s.stopping = cancel, ctx, sub, false
	s.wg.Add(1)
	s.lifecycleMu.Unlock()

	go s.run(ctx, sub)
	if s.mode() == config.ExplainAuto {
		for _, request := range s.manager.List(approval.Filter{Status: approval.StatusPending}) {
			_, _ = s.Trigger(ctx, ApprovalExplainInput{ID: request.ID})
		}
	}
	return nil
}

func (s *ApprovalExplainService) Stop() {
	if s == nil {
		return
	}
	s.lifecycleMu.Lock()
	cancel, sub := s.cancel, s.sub
	s.cancel, s.runCtx, s.sub, s.stopping = nil, nil, nil, true
	s.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if sub != nil && s.manager != nil {
		s.manager.Events().Unsubscribe(sub)
	}
	s.mu.Lock()
	for _, cancelInflight := range s.inflight {
		cancelInflight()
	}
	s.inflight = map[string]context.CancelFunc{}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *ApprovalExplainService) Status(ctx context.Context) (ApprovalExplainStatus, error) {
	mode := config.ExplainOff
	if s != nil && s.mode != nil {
		mode = s.mode()
	}
	result := ApprovalExplainStatus{Mode: mode, Readiness: llm.ReadinessUnknown}
	if mode == config.ExplainOff {
		result.Reason = "explanations are disabled"
		return result, nil
	}
	if s == nil || s.llm == nil {
		result.Readiness = llm.ReadinessUnavailable
		result.Reason = "LLM service is unavailable"
		return result, nil
	}
	status, err := s.llm.Status(ctx)
	if err != nil {
		return ApprovalExplainStatus{}, err
	}
	result.ActiveProvider = status.Active.ID
	result.Model = status.Active.Model
	result.Configured = status.Active.Configured
	result.Readiness = status.Active.Readiness
	result.Reason = status.Active.Reason
	result.Available = status.Active.Configured && status.Active.Readiness == llm.ReadinessReady
	if result.Reason == "" && !result.Available {
		if !result.Configured {
			result.Reason = "active LLM provider configuration is incomplete"
		} else {
			result.Reason = "active LLM provider readiness has not been established"
		}
	}
	return result, nil
}

func (s *ApprovalExplainService) Read(ctx context.Context, reference string) (ApprovalExplanationResult, error) {
	if s == nil || s.manager == nil {
		return ApprovalExplanationResult{}, errors.New("approval Explain service is unavailable")
	}
	source, err := s.manager.ExplanationSource(reference)
	if err != nil {
		return ApprovalExplanationResult{}, err
	}
	key := approvalExplanationGenerationKey(source)
	s.mu.Lock()
	result, ok := s.records[key]
	s.mu.Unlock()
	if !ok {
		return ApprovalExplanationResult{RequestID: source.RequestID, State: ApprovalExplanationNone}, nil
	}
	return cloneApprovalExplanationResult(result), nil
}

func (s *ApprovalExplainService) Trigger(ctx context.Context, input ApprovalExplainInput) (ApprovalExplanationResult, error) {
	if s == nil || s.manager == nil || s.explainer == nil {
		return ApprovalExplanationResult{}, errors.New("approval Explain service is unavailable")
	}
	mode := s.mode()
	if mode == config.ExplainOff {
		return ApprovalExplanationResult{}, errors.New("explanations are disabled")
	}
	source, err := s.manager.ExplanationSource(input.ID)
	if err != nil {
		return ApprovalExplanationResult{}, err
	}
	if source.Status != approval.StatusPending {
		return ApprovalExplanationResult{}, errors.New("approval request is no longer pending")
	}
	key := approvalExplanationGenerationKey(source)
	s.mu.Lock()
	if current, ok := s.records[key]; ok {
		if current.State == ApprovalExplanationPending || current.State == ApprovalExplanationReady || (current.State == ApprovalExplanationFailed && !input.Retry) {
			s.mu.Unlock()
			return cloneApprovalExplanationResult(current), nil
		}
	}
	if _, ok := s.inflight[key]; ok {
		current := s.records[key]
		s.mu.Unlock()
		return cloneApprovalExplanationResult(current), nil
	}
	runCtx, cancel, ok := s.beginGeneration()
	if !ok {
		s.mu.Unlock()
		return ApprovalExplanationResult{}, errors.New("approval Explain service is stopping")
	}
	s.attempts[key]++
	attempt := s.attempts[key]
	result := ApprovalExplanationResult{RequestID: source.RequestID, State: ApprovalExplanationPending, Attempt: attempt, UpdatedAt: s.nowUTC()}
	s.records[key] = result
	s.inflight[key] = cancel
	s.mu.Unlock()

	go s.generate(runCtx, source, key, attempt)
	return cloneApprovalExplanationResult(result), nil
}

func (s *ApprovalExplainService) beginGeneration() (context.Context, context.CancelFunc, bool) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.stopping {
		return nil, nil, false
	}
	runCtx := s.runCtx
	if runCtx == nil {
		runCtx = context.Background()
	}
	ctx, cancel := context.WithCancel(runCtx)
	s.wg.Add(1)
	return ctx, cancel, true
}

func (s *ApprovalExplainService) run(ctx context.Context, sub *approval.EventSubscription) {
	defer s.wg.Done()
	if sub == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-sub.Events:
			if !ok {
				return
			}
			s.consumeEvent(ctx, event)
		case _, ok := <-sub.Overflow:
			if !ok {
				return
			}
			s.manager.Events().AcknowledgeOverflow(sub)
		}
	}
}

func (s *ApprovalExplainService) consumeEvent(ctx context.Context, event approval.Event) {
	if event.Subject != approval.EventSubjectRequest || strings.TrimSpace(event.RequestID) == "" {
		return
	}
	switch event.Name {
	case approval.EventPending:
		if s.mode() == config.ExplainAuto {
			_, _ = s.Trigger(ctx, ApprovalExplainInput{ID: event.RequestID})
		}
	case approval.EventApproved, approval.EventDenied, approval.EventExpired, approval.EventCancelled:
		s.cancelRequestGeneration(event.RequestID)
	}
}

func (s *ApprovalExplainService) cancelRequestGeneration(requestID string) {
	requestID = strings.TrimSpace(requestID)
	s.mu.Lock()
	for key, cancel := range s.inflight {
		if strings.HasPrefix(key, requestID+"\x00") {
			cancel()
			delete(s.inflight, key)
		}
	}
	s.mu.Unlock()
}

func (s *ApprovalExplainService) generate(ctx context.Context, source approval.ExplanationSource, key string, attempt uint64) {
	defer s.wg.Done()
	safeCommand, err := approvalExplainCommand(source)
	if err != nil {
		s.completeFailure(source, key, attempt, "command cannot be safely explained")
		return
	}
	explanation, err := s.explainer.Generate(ctx, explain.CommandInput{Command: safeCommand, TargetTool: source.TargetTool})
	if err != nil {
		if ctx.Err() != nil {
			s.clearInflight(key, attempt)
			return
		}
		s.completeFailure(source, key, attempt, llmReadinessReason(err))
		return
	}
	current, err := s.manager.ExplanationSource(source.RequestID)
	if err != nil || current.Status != approval.StatusPending || !current.CreatedAt.Equal(source.CreatedAt) {
		s.clearInflight(key, attempt)
		return
	}
	s.mu.Lock()
	currentRecord, ok := s.records[key]
	if !ok || currentRecord.Attempt != attempt || currentRecord.State != ApprovalExplanationPending {
		s.mu.Unlock()
		return
	}
	ready := ApprovalExplanationResult{
		RequestID: source.RequestID, State: ApprovalExplanationReady, Attempt: attempt,
		Explanation: &explanation, UpdatedAt: s.nowUTC(),
	}
	s.records[key] = ready
	delete(s.inflight, key)
	s.mu.Unlock()
	s.manager.PublishExplanationEvent(current, approval.EventExplanationReady, attempt)
}

func (s *ApprovalExplainService) shouldUseStructuredExplanation(ctx context.Context) bool {
	if s == nil || s.llm == nil {
		return true
	}
	provider, err := s.llm.ActiveProvider(ctx)
	return err == nil && provider.Capabilities != nil && provider.Capabilities.StructuredOutput
}

func (s *ApprovalExplainService) completeFailure(source approval.ExplanationSource, key string, attempt uint64, reason string) {
	current, err := s.manager.ExplanationSource(source.RequestID)
	if err != nil || current.Status != approval.StatusPending || !current.CreatedAt.Equal(source.CreatedAt) {
		s.clearInflight(key, attempt)
		return
	}
	reason = tracepkg.SanitizeText(strings.TrimSpace(reason))
	if reason == "" {
		reason = "approval explanation failed"
	}
	if utf8.RuneCountInString(reason) > 160 {
		reason = string([]rune(reason)[:160])
	}
	s.mu.Lock()
	currentRecord, ok := s.records[key]
	if !ok || currentRecord.Attempt != attempt || currentRecord.State != ApprovalExplanationPending {
		s.mu.Unlock()
		return
	}
	failed := ApprovalExplanationResult{RequestID: source.RequestID, State: ApprovalExplanationFailed, Attempt: attempt, Failure: reason, UpdatedAt: s.nowUTC()}
	s.records[key] = failed
	delete(s.inflight, key)
	s.mu.Unlock()
	s.manager.PublishExplanationEvent(current, approval.EventExplanationFailed, attempt)
}

func (s *ApprovalExplainService) clearInflight(key string, attempt uint64) {
	s.mu.Lock()
	if current, ok := s.records[key]; ok && current.Attempt == attempt && current.State == ApprovalExplanationPending {
		delete(s.records, key)
	}
	delete(s.inflight, key)
	s.mu.Unlock()
}

func approvalExplainCommand(source approval.ExplanationSource) (string, error) {
	if source.TargetTool != "run_command" && source.TargetTool != "start_process" {
		return "", errors.New("unsupported approval target")
	}
	return explain.SanitizeCommand(source.Command)
}

func parseApprovalExplanation(result llm.Result, generatedAt time.Time) (ApprovalExplanation, error) {
	return explain.ParseResult(result, generatedAt)
}

func approvalExplanationGenerationKey(source approval.ExplanationSource) string {
	return source.RequestID + "\x00" + source.CreatedAt.UTC().Format(time.RFC3339Nano)
}

func cloneApprovalExplanationResult(value ApprovalExplanationResult) ApprovalExplanationResult {
	if value.Explanation != nil {
		copyValue := *value.Explanation
		copyValue.Steps = cloneStrings(copyValue.Steps)
		copyValue.Effects = cloneStrings(copyValue.Effects)
		copyValue.RiskNotes = cloneStrings(copyValue.RiskNotes)
		copyValue.Unknowns = cloneStrings(copyValue.Unknowns)
		value.Explanation = &copyValue
	}
	return value
}

func cloneStrings(value []string) []string {
	return append([]string(nil), value...)
}

func (s *ApprovalExplainService) nowUTC() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func BindApprovalExplainOperations(dispatcher *Dispatcher, service *ApprovalExplainService) error {
	if dispatcher == nil || service == nil {
		return errors.New("approval Explain operation dependencies are unavailable")
	}
	bindings := []struct {
		id      capability.ID
		handler OperationHandler
	}{
		{capability.RequestExplainStatus, func(ctx context.Context, _ any) (any, error) { return service.Status(ctx) }},
		{capability.RequestExplanationView, typedOperation[ApprovalExplanationReadInput](capability.RequestExplanationView, func(ctx context.Context, input ApprovalExplanationReadInput) (any, error) {
			return service.Read(ctx, input.ID)
		})},
		{capability.RequestExplain, typedOperation[ApprovalExplainInput](capability.RequestExplain, func(ctx context.Context, input ApprovalExplainInput) (any, error) { return service.Trigger(ctx, input) })},
	}
	for _, binding := range bindings {
		if err := dispatcher.Register(binding.id, binding.handler); err != nil {
			return err
		}
	}
	return nil
}
