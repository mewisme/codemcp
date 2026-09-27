package semantic

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func runtimeRequest(index int) Request {
	return Request{
		Consumer: Consumer{ID: "memory_search", Purpose: "memory_rerank"},
		State:    map[string]any{"query": fmt.Sprintf("query-%d", index), "candidate": "candidate"},
		Questions: map[string]Question{
			"relevant": {Type: PrimitiveNoul, Instructions: "Is relevant?"},
		},
	}
}

func runtimeResult(provider, model string, probability float64) Result {
	return Result{
		Answers: map[string]Answer{
			"relevant": {Type: PrimitiveNoul, Noul: &NoulAnswer{ProbabilityYes: probability}},
		},
		ProviderMetadata: ProviderMetadata{
			Provider: provider, Model: model, Duration: time.Millisecond,
			Usage: &Usage{InputTokens: 2, OutputTokens: 1},
		},
	}
}

func TestManagerSelectsProviderRetriesAndCaches(t *testing.T) {
	now := time.Unix(100, 0)
	var alphaCalls atomic.Int32
	var betaCalls atomic.Int32
	manager := NewManager(ManagerOptions{
		Now:   func() time.Time { return now },
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
	if err := manager.RegisterProvider("alpha", ProviderRegistration{
		Model: "model-a",
		Provider: ProviderFunc(func(context.Context, Request) (Result, error) {
			call := alphaCalls.Add(1)
			if call < 3 {
				return Result{}, NewError(ErrorRateLimited, "provider detail must stay local")
			}
			return runtimeResult("alpha", "model-a", 0.8), nil
		}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.RegisterProvider("beta", ProviderRegistration{
		Model: "model-b",
		Provider: ProviderFunc(func(context.Context, Request) (Result, error) {
			betaCalls.Add(1)
			return runtimeResult("beta", "model-b", 0.6), nil
		}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.SelectProvider("alpha"); err != nil {
		t.Fatal(err)
	}

	request := runtimeRequest(1)
	first, err := manager.Evaluate(t.Context(), request)
	if err != nil || alphaCalls.Load() != 3 || first.Runtime.Attempts != 3 || first.Runtime.CacheHit {
		t.Fatalf("first=%#v err=%v calls=%d", first, err, alphaCalls.Load())
	}
	second, err := manager.Evaluate(t.Context(), request)
	if err != nil || alphaCalls.Load() != 3 || !second.Runtime.CacheHit || second.Runtime.Attempts != 0 {
		t.Fatalf("second=%#v err=%v calls=%d", second, err, alphaCalls.Load())
	}
	second.Answers["relevant"].Noul.ProbabilityYes = 0
	third, err := manager.Evaluate(t.Context(), request)
	if err != nil || third.Answers["relevant"].Noul.ProbabilityYes != 0.8 {
		t.Fatalf("cache mutation leaked: %#v err=%v", third, err)
	}

	now = now.Add(CacheTTL)
	expired, err := manager.Evaluate(t.Context(), request)
	if err != nil || expired.Runtime.CacheHit || alphaCalls.Load() != 4 {
		t.Fatalf("expired=%#v err=%v calls=%d", expired, err, alphaCalls.Load())
	}

	if err := manager.SelectProvider("beta"); err != nil {
		t.Fatal(err)
	}
	selected, err := manager.Evaluate(t.Context(), request)
	if err != nil || selected.Provider != "beta" || betaCalls.Load() != 1 {
		t.Fatalf("selected=%#v err=%v beta=%d", selected, err, betaCalls.Load())
	}
	health := manager.Health()
	if !health.Available || health.Provider != "beta" || health.Model != "model-b" || health.LastSuccessAt.IsZero() {
		t.Fatalf("health=%#v", health)
	}
}

func TestManagerOperationDeadlineBoundsProviderOutage(t *testing.T) {
	manager := NewManager(ManagerOptions{Timeout: 40 * time.Millisecond, MaxAttempts: 3, RetryBaseDelay: 5 * time.Millisecond})
	if err := manager.RegisterProvider("slow", ProviderRegistration{
		Model: "slow-model",
		Provider: ProviderFunc(func(ctx context.Context, _ Request) (Result, error) {
			<-ctx.Done()
			return Result{}, ctx.Err()
		}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.SelectProvider("slow"); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err := manager.Evaluate(t.Context(), runtimeRequest(2))
	if !IsCategory(err, ErrorTimeout) {
		t.Fatalf("err=%#v", err)
	}
	if elapsed := time.Since(started); elapsed > 300*time.Millisecond {
		t.Fatalf("provider outage exceeded runtime bound: %s", elapsed)
	}
}

func TestManagerConcurrencyAndQueueAreBounded(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, MaxProviderConcurrency+MaxQueuedEvaluations)
	var active atomic.Int32
	var peak atomic.Int32
	manager := NewManager(ManagerOptions{Timeout: time.Second, MaxAttempts: 1})
	if err := manager.RegisterProvider("bounded", ProviderRegistration{
		Model: "model",
		Provider: ProviderFunc(func(ctx context.Context, _ Request) (Result, error) {
			current := active.Add(1)
			defer active.Add(-1)
			for {
				previous := peak.Load()
				if current <= previous || peak.CompareAndSwap(previous, current) {
					break
				}
			}
			entered <- struct{}{}
			select {
			case <-release:
				return runtimeResult("bounded", "model", 0.7), nil
			case <-ctx.Done():
				return Result{}, ctx.Err()
			}
		}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.SelectProvider("bounded"); err != nil {
		t.Fatal(err)
	}

	total := MaxProviderConcurrency + MaxQueuedEvaluations
	results := make(chan error, total)
	for i := 0; i < total; i++ {
		i := i
		go func() {
			_, err := manager.Evaluate(t.Context(), runtimeRequest(1000+i))
			results <- err
		}()
	}
	for i := 0; i < MaxProviderConcurrency; i++ {
		<-entered
	}
	deadline := time.Now().Add(time.Second)
	for manager.Health().Reuse.Queued != MaxQueuedEvaluations {
		if time.Now().After(deadline) {
			t.Fatalf("reuse=%#v", manager.Health().Reuse)
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := manager.Evaluate(t.Context(), runtimeRequest(9999)); !IsCategory(err, ErrorOverloaded) {
		t.Fatalf("overflow err=%#v", err)
	}
	close(release)
	for i := 0; i < total; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if peak.Load() > MaxProviderConcurrency {
		t.Fatalf("peak=%d", peak.Load())
	}
}

func TestOptionalEvaluationFallsBackButRiskNeverFallsBackOrCaches(t *testing.T) {
	var evaluations atomic.Int32
	var riskCalls atomic.Int32
	manager := NewManager(ManagerOptions{
		MaxAttempts: 1,
		Sleep:       func(context.Context, time.Duration) error { return nil },
	})
	if err := manager.RegisterProvider("fake", ProviderRegistration{
		Model: "model",
		Provider: ProviderFunc(func(context.Context, Request) (Result, error) {
			evaluations.Add(1)
			return Result{}, NewError(ErrorTransport, "private provider detail")
		}),
		RiskClassifier: RiskClassifierFunc(func(_ context.Context, input RiskInput) (RiskAssessment, error) {
			riskCalls.Add(1)
			if strings.Contains(fmt.Sprint(input.Invocation.Arguments), "fail") {
				return RiskAssessment{}, NewError(ErrorTransport, "private command detail")
			}
			return RiskAssessment{
				Class: RiskMedium, Confidence: 0.95, Category: "mutation",
				Provider: ProviderMetadata{Provider: "fake", Model: "risk-model"},
			}, nil
		}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.SelectProvider("fake"); err != nil {
		t.Fatal(err)
	}

	fallback := runtimeResult("native", "deterministic", 0.5)
	got, used := manager.EvaluateOptional(t.Context(), runtimeRequest(3), func() Result { return fallback })
	if used || got.Provider != "native" || evaluations.Load() != 1 {
		t.Fatalf("fallback=%#v used=%t calls=%d", got, used, evaluations.Load())
	}

	input := RiskInput{
		Consumer:    Consumer{ID: "approval.semantic", Purpose: "command_risk"},
		WorkspaceID: "ws_one", CallerID: "caller_one",
		Invocation: CanonicalInvocation{Operation: "shell.execute", Tool: "shell", Arguments: map[string]any{"command": "git clean"}},
	}
	for i := 0; i < 2; i++ {
		assessment, err := manager.ClassifyRisk(t.Context(), input, 0.8)
		if err != nil || assessment.Class != RiskMedium {
			t.Fatalf("assessment=%#v err=%v", assessment, err)
		}
	}
	if riskCalls.Load() != 2 {
		t.Fatalf("risk assessment was cached: calls=%d", riskCalls.Load())
	}
	input.CallerID = "caller_two"
	input.Invocation.Arguments = map[string]any{"command": "fail"}
	if _, err := manager.ClassifyRisk(t.Context(), input, 0.8); !IsCategory(err, ErrorTransport) {
		t.Fatalf("risk failure err=%#v", err)
	}
}

func TestSemanticObservabilityIsMetadataOnly(t *testing.T) {
	const stateSentinel = "STATE_SENTINEL_PRIVATE"
	const instructionSentinel = "INSTRUCTION_SENTINEL_PRIVATE"
	const errorSentinel = "ERROR_SENTINEL_PRIVATE"
	const riskReasonSentinel = "RISK_REASON_SENTINEL_PRIVATE"

	var mu sync.Mutex
	var events []tracepkg.Event
	manager := NewManager(ManagerOptions{MaxAttempts: 1})
	manager.SetTraceObserver(func(event tracepkg.Event) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	})
	if err := manager.RegisterProvider("fake", ProviderRegistration{
		Model: "model",
		Provider: ProviderFunc(func(context.Context, Request) (Result, error) {
			return Result{}, NewError(ErrorRateLimited, errorSentinel)
		}),
		RiskClassifier: RiskClassifierFunc(func(context.Context, RiskInput) (RiskAssessment, error) {
			return RiskAssessment{
				Class: RiskHigh, Confidence: 0.95, Category: "filesystem",
				Reason:   riskReasonSentinel,
				Provider: ProviderMetadata{Provider: "fake", Model: "model"},
			}, nil
		}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.SelectProvider("fake"); err != nil {
		t.Fatal(err)
	}
	request := runtimeRequest(4)
	request.State = map[string]any{"secret": stateSentinel}
	request.Questions["relevant"] = Question{Type: PrimitiveNoul, Instructions: instructionSentinel}
	_, _ = manager.EvaluateOptional(t.Context(), request, func() Result { return Result{} })

	riskInput := RiskInput{
		Consumer:    Consumer{ID: "approval.semantic", Purpose: "command_risk"},
		WorkspaceID: "ws_safe", CallerID: "caller_safe",
		Invocation: CanonicalInvocation{Operation: "shell.execute", Arguments: map[string]any{"command": stateSentinel}},
	}
	if _, err := manager.ClassifyRisk(t.Context(), riskInput, 0.8); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	text := fmt.Sprint(events)
	mu.Unlock()
	for _, sentinel := range []string{stateSentinel, instructionSentinel, errorSentinel, riskReasonSentinel} {
		if strings.Contains(text, sentinel) {
			t.Fatalf("semantic content leaked to trace: %s", text)
		}
	}
	if !strings.Contains(text, "semantic.evaluation.completed") ||
		!strings.Contains(text, "semantic.consumer.completed") ||
		!strings.Contains(text, "semantic.risk.completed") {
		t.Fatalf("missing metadata events: %s", text)
	}
}

func TestProviderReplacementInvalidatesCacheAndLateHealth(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	manager := NewManager(ManagerOptions{MaxAttempts: 1})
	if err := manager.RegisterProvider("swap", ProviderRegistration{
		Model: "old",
		Provider: ProviderFunc(func(context.Context, Request) (Result, error) {
			close(started)
			<-release
			return runtimeResult("swap", "old", 0.9), nil
		}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.SelectProvider("swap"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_, _ = manager.Evaluate(t.Context(), runtimeRequest(5))
		close(done)
	}()
	<-started
	if err := manager.RegisterProvider("swap", ProviderRegistration{
		Model: "new",
		Provider: ProviderFunc(func(context.Context, Request) (Result, error) {
			return runtimeResult("swap", "new", 0.7), nil
		}),
	}); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-done
	health := manager.Health()
	if health.Model != "new" || !health.LastAttemptAt.IsZero() || !health.LastSuccessAt.IsZero() {
		t.Fatalf("late replaced provider changed health=%#v", health)
	}
	result, err := manager.Evaluate(t.Context(), runtimeRequest(5))
	if err != nil || result.Model != "new" || result.Runtime.CacheHit {
		t.Fatalf("new result=%#v err=%v", result, err)
	}
}

func TestConfigureProviderAtomicallyReplacesAndSelectsBothCapabilities(t *testing.T) {
	manager := NewManager(ManagerOptions{MaxAttempts: 1})
	registration := func(generation string) ProviderRegistration {
		return ProviderRegistration{
			Model: generation,
			Provider: ProviderFunc(func(context.Context, Request) (Result, error) {
				result := runtimeResult("typesafe", generation, 0.9)
				result.Model = generation
				return result, nil
			}),
			RiskClassifier: RiskClassifierFunc(func(context.Context, RiskInput) (RiskAssessment, error) {
				return RiskAssessment{
					Class: RiskLow, Confidence: 0.95, Category: "generation",
					Provider: ProviderMetadata{Provider: "typesafe", Model: generation},
				}, nil
			}),
		}
	}
	if err := manager.ConfigureProvider("typesafe", registration("generation-one")); err != nil {
		t.Fatal(err)
	}
	if health := manager.Health(); !health.Available || health.Provider != "typesafe" || health.Model != "generation-one" {
		t.Fatalf("health=%#v", health)
	}
	if err := manager.ConfigureProvider("typesafe", registration("generation-two")); err != nil {
		t.Fatal(err)
	}
	result, err := manager.Evaluate(t.Context(), runtimeRequest(77))
	if err != nil || result.Model != "generation-two" {
		t.Fatalf("evaluation=%#v err=%v", result, err)
	}
	input := RiskInput{
		Consumer: Consumer{ID: "approval.semantic", Purpose: "command_risk"},
		Invocation: CanonicalInvocation{
			Operation: "run_command", Tool: "run_command",
			Arguments: map[string]any{"command": "touch example"},
		},
	}
	assessment, err := manager.ClassifyRisk(t.Context(), input, 0.8)
	if err != nil || assessment.Provider.Model != "generation-two" {
		t.Fatalf("risk=%#v err=%v", assessment, err)
	}
}

func TestSetUnavailableInvalidatesSelectedProviderCache(t *testing.T) {
	var calls atomic.Int32
	manager := NewManager(ManagerOptions{MaxAttempts: 1})
	if err := manager.RegisterProvider("cacheable", ProviderRegistration{
		Model: "model",
		Provider: ProviderFunc(func(context.Context, Request) (Result, error) {
			calls.Add(1)
			return runtimeResult("cacheable", "model", 0.8), nil
		}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.SelectProvider("cacheable"); err != nil {
		t.Fatal(err)
	}
	request := runtimeRequest(6)
	if _, err := manager.Evaluate(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if cached, err := manager.Evaluate(t.Context(), request); err != nil || !cached.Runtime.CacheHit {
		t.Fatalf("cached=%#v err=%v", cached, err)
	}
	manager.SetUnavailable(ErrorDisabled)
	if _, err := manager.Evaluate(t.Context(), request); !IsCategory(err, ErrorDisabled) {
		t.Fatalf("disabled err=%#v", err)
	}
	if err := manager.SelectProvider("cacheable"); err != nil {
		t.Fatal(err)
	}
	result, err := manager.Evaluate(t.Context(), request)
	if err != nil || result.Runtime.CacheHit || calls.Load() != 2 {
		t.Fatalf("post-enable=%#v err=%v calls=%d", result, err, calls.Load())
	}
}
