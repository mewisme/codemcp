package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/controlguard"
	"go.mewis.me/codemcp/internal/integrations/semantic"
)

func semanticApprovalContext(caller string) context.Context {
	ctx := WithCallSource(context.Background(), "tunnel")
	return WithApprovalCorrelation(ctx, caller, caller+"-request")
}

func semanticAssessment(class semantic.RiskClass, confidence float64) semantic.RiskAssessment {
	return semantic.RiskAssessment{
		Class: class, Confidence: confidence, Category: "filesystem_mutation",
		Provider: semantic.ProviderMetadata{Provider: "fake", Model: "fixture"},
	}
}

func TestSemanticApprovalRiskMappingsPreserveOrTightenNativeAllow(t *testing.T) {
	tests := []struct {
		name       string
		class      semantic.RiskClass
		wantAllow  bool
		wantReview bool
		wantDeny   bool
	}{
		{name: "low", class: semantic.RiskLow, wantAllow: true},
		{name: "medium", class: semantic.RiskMedium, wantReview: true},
		{name: "high", class: semantic.RiskHigh, wantReview: true},
		{name: "critical", class: semantic.RiskCritical, wantDeny: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runtime, workspaceID := newApprovalShellRuntime(t)
			item, err := runtime.Workspaces.Get(workspaceID)
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			configureSemanticApprovalClassifier(t, runtime, semantic.RiskClassifierFunc(func(_ context.Context, input semantic.RiskInput) (semantic.RiskAssessment, error) {
				calls.Add(1)
				if input.Invocation.Operation != "run_command" || input.WorkspaceID != workspaceID {
					t.Fatalf("risk input=%#v", input)
				}
				return semanticAssessment(tc.class, 0.95), nil
			}))
			target := filepath.Join(item.Path, "semantic-map-"+tc.name)
			args := map[string]any{"workspace_id": workspaceID, "command": "touch " + filepath.Base(target)}
			result, err := runtime.Call(semanticApprovalContext("caller-"+tc.name), "run_command", args)
			if err != nil {
				t.Fatal(err)
			}
			_, statErr := os.Stat(target)
			switch {
			case tc.wantAllow:
				if result.IsError || statErr != nil {
					t.Fatalf("low-risk native allow was not preserved: result=%#v stat=%v", result, statErr)
				}
			case tc.wantReview:
				challenge, ok := result.StructuredContent.(approvalRequiredResponse)
				if !result.IsError || !ok || challenge.GuardCode != string(controlguard.CodeSemanticRisk) || statErr == nil {
					t.Fatalf("review result=%#v stat=%v", result, statErr)
				}
			case tc.wantDeny:
				if !result.IsError || result.StructuredContent != nil || statErr == nil || !strings.Contains(result.Content[0].Text, "semantic risk critical") {
					t.Fatalf("deny result=%#v stat=%v", result, statErr)
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("classifier calls=%d", calls.Load())
			}
		})
	}
}

func TestSemanticApprovalDisabledPreservesDeterministicCommandBehavior(t *testing.T) {
	runtime, workspaceID := newApprovalShellRuntime(t)
	item, _ := runtime.Workspaces.Get(workspaceID)
	var calls atomic.Int32
	if err := runtime.Semantic.RegisterProvider("fake", semantic.ProviderRegistration{
		RiskClassifier: semantic.RiskClassifierFunc(func(context.Context, semantic.RiskInput) (semantic.RiskAssessment, error) {
			calls.Add(1)
			return semanticAssessment(semantic.RiskCritical, 1), nil
		}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Semantic.SelectProvider("fake"); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(item.Path, "semantic-disabled")
	result, err := runtime.Call(semanticApprovalContext("disabled"), "run_command", map[string]any{
		"workspace_id": workspaceID, "command": "touch " + filepath.Base(target),
	})
	if err != nil || result.IsError {
		t.Fatalf("disabled command=%#v err=%v", result, err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("disabled semantic changed native execution: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("disabled semantic invoked classifier %d time(s)", calls.Load())
	}
}

func TestSemanticApprovalFailureAndLowConfidenceRequireCanonicalReview(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*testing.T, *Runtime)
	}{
		{name: "missing provider", configure: func(t *testing.T, runtime *Runtime) {
			policy := DefaultSemanticApprovalPolicy()
			policy.Enabled = true
			policy.Provider = "missing"
			runtime.SetSemanticApprovalPolicy(policy)
		}},
		{name: "provider failure", configure: func(t *testing.T, runtime *Runtime) {
			configureSemanticApprovalClassifier(t, runtime, semantic.RiskClassifierFunc(func(context.Context, semantic.RiskInput) (semantic.RiskAssessment, error) {
				return semantic.RiskAssessment{}, semantic.NewError(semantic.ErrorRateLimited, "provider-private-detail")
			}))
		}},
		{name: "provider timeout", configure: func(t *testing.T, runtime *Runtime) {
			configureSemanticApprovalClassifier(t, runtime, semantic.RiskClassifierFunc(func(context.Context, semantic.RiskInput) (semantic.RiskAssessment, error) {
				return semantic.RiskAssessment{}, semantic.NewError(semantic.ErrorTimeout, "provider-private-detail")
			}))
		}},
		{name: "low confidence", configure: func(t *testing.T, runtime *Runtime) {
			configureSemanticApprovalClassifier(t, runtime, semantic.RiskClassifierFunc(func(context.Context, semantic.RiskInput) (semantic.RiskAssessment, error) {
				return semanticAssessment(semantic.RiskLow, 0.2), nil
			}))
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runtime, workspaceID := newApprovalShellRuntime(t)
			item, _ := runtime.Workspaces.Get(workspaceID)
			tc.configure(t, runtime)
			target := filepath.Join(item.Path, "semantic-failure-"+strings.ReplaceAll(tc.name, " ", "-"))
			result, err := runtime.Call(semanticApprovalContext("failure-"+tc.name), "run_command", map[string]any{
				"workspace_id": workspaceID, "command": "touch " + filepath.Base(target),
			})
			if err != nil || !result.IsError {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			challenge, ok := result.StructuredContent.(approvalRequiredResponse)
			if !ok || challenge.GuardCode != string(controlguard.CodeSemanticRisk) || !strings.Contains(challenge.Reason, "classification unavailable") {
				t.Fatalf("challenge=%#v", result.StructuredContent)
			}
			if strings.Contains(challenge.Reason, "provider-private-detail") {
				t.Fatalf("provider detail leaked to review: %q", challenge.Reason)
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatalf("classification failure executed mutation: %v", err)
			}
		})
	}
}

func TestSemanticApprovalFailureModeDenyNeverCreatesReviewOrSideEffect(t *testing.T) {
	runtime, workspaceID := newApprovalShellRuntime(t)
	item, _ := runtime.Workspaces.Get(workspaceID)
	policy := DefaultSemanticApprovalPolicy()
	policy.Enabled = true
	policy.Provider = "missing"
	policy.FailMode = SemanticApprovalDeny
	runtime.SetSemanticApprovalPolicy(policy)
	target := filepath.Join(item.Path, "semantic-fail-deny")
	result, err := runtime.Call(semanticApprovalContext("fail-deny"), "run_command", map[string]any{
		"workspace_id": workspaceID, "command": "touch " + filepath.Base(target),
	})
	if err != nil || !result.IsError || result.StructuredContent != nil {
		t.Fatalf("deny failure result=%#v err=%v", result, err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("deny failure executed mutation: %v", err)
	}
	if requests := runtime.Approvals.List(approval.Filter{}); len(requests) != 0 {
		t.Fatalf("deny failure created approval state: %#v", requests)
	}
}

func TestSemanticApprovalWithoutReviewCorrelationBlocksMutation(t *testing.T) {
	runtime, workspaceID := newApprovalShellRuntime(t)
	item, _ := runtime.Workspaces.Get(workspaceID)
	policy := DefaultSemanticApprovalPolicy()
	policy.Enabled = true
	policy.Provider = "missing"
	runtime.SetSemanticApprovalPolicy(policy)
	target := filepath.Join(item.Path, "semantic-no-correlation")
	result, err := runtime.Call(context.Background(), "run_command", map[string]any{
		"workspace_id": workspaceID, "command": "touch " + filepath.Base(target),
	})
	if err != nil || !result.IsError || result.StructuredContent != nil {
		t.Fatalf("uncorrelated result=%#v err=%v", result, err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("uncorrelated review failure executed mutation: %v", err)
	}
}

func TestSemanticApprovalNativeGuardPrecedesClassifier(t *testing.T) {
	runtime, workspaceID := newApprovalShellRuntime(t)
	var calls atomic.Int32
	configureSemanticApprovalClassifier(t, runtime, semantic.RiskClassifierFunc(func(context.Context, semantic.RiskInput) (semantic.RiskAssessment, error) {
		calls.Add(1)
		return semanticAssessment(semantic.RiskLow, 1), nil
	}))
	result, err := runtime.Call(semanticApprovalContext("native-guard"), "run_command", map[string]any{
		"workspace_id": workspaceID, "command": "unset CM_TOOL_CONTEXT",
	})
	if err != nil || !result.IsError {
		t.Fatalf("native guard result=%#v err=%v", result, err)
	}
	if _, ok := result.StructuredContent.(approvalRequiredResponse); ok {
		t.Fatalf("native hard deny became semantic/human approval: %#v", result.StructuredContent)
	}
	if calls.Load() != 0 {
		t.Fatalf("classifier ran before deterministic guard: %d", calls.Load())
	}
}

func TestSemanticApprovalPathAndNetworkGuardsPrecedeClassifier(t *testing.T) {
	outsideMutation := "touch /tmp/codemcp-semantic-outside"
	if os.PathSeparator == '\\' {
		outsideMutation = "touch " + filepath.Join(t.TempDir(), "codemcp-semantic-outside")
	}
	for _, command := range []string{outsideMutation, "ftp example.com"} {
		t.Run(command, func(t *testing.T) {
			runtime, workspaceID := newApprovalShellRuntime(t)
			var calls atomic.Int32
			configureSemanticApprovalClassifier(t, runtime, semantic.RiskClassifierFunc(func(context.Context, semantic.RiskInput) (semantic.RiskAssessment, error) {
				calls.Add(1)
				return semanticAssessment(semantic.RiskLow, 1), nil
			}))
			result, err := runtime.Call(semanticApprovalContext("local-guard"), "run_command", map[string]any{"workspace_id": workspaceID, "command": command})
			if err != nil || !result.IsError {
				t.Fatalf("guard result=%#v err=%v", result, err)
			}
			if calls.Load() != 0 {
				t.Fatalf("classifier bypassed local guard: %d", calls.Load())
			}
		})
	}
}

func TestSemanticApprovalNativeRequireApprovalPrecedesClassifier(t *testing.T) {
	runtime, workspaceID := newApprovalShellRuntime(t)
	var calls atomic.Int32
	configureSemanticApprovalClassifier(t, runtime, semantic.RiskClassifierFunc(func(context.Context, semantic.RiskInput) (semantic.RiskAssessment, error) {
		calls.Add(1)
		return semanticAssessment(semantic.RiskLow, 1), nil
	}))
	result, err := runtime.Call(semanticApprovalContext("native-review"), "run_command", map[string]any{
		"workspace_id": workspaceID, "command": "cm update",
	})
	if err != nil || !result.IsError {
		t.Fatalf("native review result=%#v err=%v", result, err)
	}
	challenge, ok := result.StructuredContent.(approvalRequiredResponse)
	if !ok || challenge.GuardCode != string(controlguard.CodeControlPlaneMutation) {
		t.Fatalf("native review was replaced by semantic policy: %#v", result.StructuredContent)
	}
	if calls.Load() != 0 {
		t.Fatalf("classifier ran before native required approval: %d", calls.Load())
	}
}

func TestSemanticApprovalUsesWholeCompoundEffectiveCommand(t *testing.T) {
	runtime, workspaceID := newApprovalShellRuntime(t)
	var captured semantic.RiskInput
	configureSemanticApprovalClassifier(t, runtime, semantic.RiskClassifierFunc(func(_ context.Context, input semantic.RiskInput) (semantic.RiskAssessment, error) {
		captured = input
		return semanticAssessment(semantic.RiskMedium, 0.95), nil
	}))
	command := "touch compound-a && touch compound-b"
	result, err := runtime.Call(semanticApprovalContext("compound"), "run_command", map[string]any{"workspace_id": workspaceID, "command": command})
	if err != nil || !result.IsError {
		t.Fatalf("compound result=%#v err=%v", result, err)
	}
	arguments, _ := captured.Invocation.Arguments.(map[string]any)
	if got, _ := arguments["command"].(string); !strings.Contains(got, "compound-a") || !strings.Contains(got, "compound-b") || !strings.Contains(got, "&&") {
		t.Fatalf("compound semantic input=%#v", captured)
	}
}

func TestSemanticApprovalCoversBackgroundProcessExecution(t *testing.T) {
	runtime, workspaceID := newApprovalShellRuntime(t)
	var calls atomic.Int32
	configureSemanticApprovalClassifier(t, runtime, semantic.RiskClassifierFunc(func(_ context.Context, input semantic.RiskInput) (semantic.RiskAssessment, error) {
		calls.Add(1)
		if input.Invocation.Operation != "start_process" {
			t.Fatalf("operation=%q", input.Invocation.Operation)
		}
		return semanticAssessment(semantic.RiskHigh, 0.95), nil
	}))
	result, err := runtime.Call(semanticApprovalContext("background-semantic"), "start_process", map[string]any{
		"workspace_id": workspaceID, "command": "touch semantic-background",
	})
	if err != nil || !result.IsError {
		t.Fatalf("background result=%#v err=%v", result, err)
	}
	challenge, ok := result.StructuredContent.(approvalRequiredResponse)
	if !ok || challenge.GuardCode != string(controlguard.CodeSemanticRisk) || challenge.TargetTool != "start_process" {
		t.Fatalf("background challenge=%#v", result.StructuredContent)
	}
	if calls.Load() != 1 {
		t.Fatalf("background classifier calls=%d", calls.Load())
	}
}

func TestSemanticApprovalApprovedRetrySkipsReclassificationAndRemainsOneShot(t *testing.T) {
	runtime, workspaceID := newApprovalShellRuntime(t)
	item, _ := runtime.Workspaces.Get(workspaceID)
	var calls atomic.Int32
	configureSemanticApprovalClassifier(t, runtime, semantic.RiskClassifierFunc(func(context.Context, semantic.RiskInput) (semantic.RiskAssessment, error) {
		calls.Add(1)
		return semanticAssessment(semantic.RiskMedium, 0.95), nil
	}))
	target := filepath.Join(item.Path, "semantic-approved")
	args := map[string]any{"workspace_id": workspaceID, "command": "touch " + filepath.Base(target)}
	ctx := semanticApprovalContext("semantic-retry")
	first, err := runtime.Call(ctx, "run_command", args)
	if err != nil || !first.IsError {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	challenge := first.StructuredContent.(approvalRequiredResponse)
	request, created, err := runtime.Approvals.CreateRequestWithTitle(challenge.ChallengeID, "semantic-retry", workspaceID, "Create approved file")
	if err != nil || !created {
		t.Fatalf("request=%#v created=%t err=%v", request, created, err)
	}
	if _, err := runtime.Approvals.Approve(request.ID, "reviewer", "reviewed"); err != nil {
		t.Fatal(err)
	}
	policy := runtime.semanticApprovalPolicy()
	policy.Actions[semantic.RiskMedium] = SemanticApprovalDeny
	runtime.SetSemanticApprovalPolicy(policy)

	retry, err := runtime.Call(semanticApprovalContext("semantic-retry"), "run_command", args)
	if err != nil || retry.IsError {
		t.Fatalf("approved retry=%#v err=%v", retry, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("approved retry reclassified: calls=%d", calls.Load())
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("approved retry did not execute: %v", err)
	}
	consumed, ok := runtime.Approvals.Get(request.ID)
	if !ok || consumed.Status != approval.StatusConsumed {
		t.Fatalf("request=%#v ok=%t", consumed, ok)
	}
	replay, err := runtime.Call(semanticApprovalContext("semantic-retry"), "run_command", args)
	if err != nil || !replay.IsError {
		t.Fatalf("one-shot replay=%#v err=%v", replay, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("fresh replay did not reclassify: calls=%d", calls.Load())
	}
}

func TestSemanticApprovalWorkspaceAuthorizationRunsBeforeClassifier(t *testing.T) {
	runtime, _ := newApprovalShellRuntime(t)
	var calls atomic.Int32
	configureSemanticApprovalClassifier(t, runtime, semantic.RiskClassifierFunc(func(context.Context, semantic.RiskInput) (semantic.RiskAssessment, error) {
		calls.Add(1)
		return semanticAssessment(semantic.RiskLow, 1), nil
	}))
	result, err := runtime.Call(semanticApprovalContext("unauthorized"), "run_command", map[string]any{
		"workspace_id": "ws_missing", "command": "touch never",
	})
	if err == nil && !result.IsError {
		t.Fatalf("missing workspace unexpectedly executed: %#v", result)
	}
	if calls.Load() != 0 {
		t.Fatalf("classifier saw unauthorized workspace: %d", calls.Load())
	}
}

func TestSemanticApprovalConcurrentReviewKeepsMutationsPending(t *testing.T) {
	runtime, workspaceID := newApprovalShellRuntime(t)
	item, _ := runtime.Workspaces.Get(workspaceID)
	var calls atomic.Int32
	configureSemanticApprovalClassifier(t, runtime, semantic.RiskClassifierFunc(func(context.Context, semantic.RiskInput) (semantic.RiskAssessment, error) {
		calls.Add(1)
		return semanticAssessment(semantic.RiskMedium, 0.95), nil
	}))
	type callResult struct {
		result Result
		err    error
	}
	results := make(chan callResult, 2)
	for _, caller := range []string{"concurrent-a", "concurrent-b"} {
		caller := caller
		go func() {
			result, err := runtime.Call(semanticApprovalContext(caller), "run_command", map[string]any{
				"workspace_id": workspaceID, "command": "touch " + caller,
			})
			results <- callResult{result: result, err: err}
		}()
	}
	for i := 0; i < 2; i++ {
		value := <-results
		if value.err != nil || !value.result.IsError {
			t.Fatalf("concurrent result=%#v err=%v", value.result, value.err)
		}
		challenge, ok := value.result.StructuredContent.(approvalRequiredResponse)
		if !ok || challenge.GuardCode != string(controlguard.CodeSemanticRisk) {
			t.Fatalf("concurrent challenge=%#v", value.result.StructuredContent)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("concurrent classifier calls=%d", calls.Load())
	}
	for _, name := range []string{"concurrent-a", "concurrent-b"} {
		if _, err := os.Stat(filepath.Join(item.Path, name)); !os.IsNotExist(err) {
			t.Fatalf("concurrent review executed %s: %v", name, err)
		}
	}
}
