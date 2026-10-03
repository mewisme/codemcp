package chatgptweb

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/integrations/browser"
)

type driverFakeTab struct {
	mu sync.Mutex

	done chan struct{}
	err  error

	navigations []string
	closed      bool
	stopCalls   int

	surfaces             []domSnapshot
	surfaceAt            int
	controls             controlResult
	controlsErr          error
	connector            connectorResult
	connectorErr         error
	attachText           string
	attachConnectorCount int
	attachErr            error
	send                 sendResult
	sendErr              error
}

func newDriverFakeTab() *driverFakeTab {
	return &driverFakeTab{
		done:      make(chan struct{}),
		controls:  controlResult{ModelVerified: true, EffortVerified: true},
		connector: connectorResult{Selected: true},
		send:      sendResult{Activated: true},
	}
}

func (tab *driverFakeTab) ID() string { return "driver-tab" }

func (tab *driverFakeTab) Navigate(_ context.Context, url string) error {
	tab.mu.Lock()
	defer tab.mu.Unlock()
	tab.navigations = append(tab.navigations, url)
	return nil
}

func (tab *driverFakeTab) Evaluate(_ context.Context, expression string, result any) error {
	tab.mu.Lock()
	defer tab.mu.Unlock()
	switch {
	case strings.Contains(expression, "/*codemcp:onboarding*/"):
		if value, ok := result.(*bool); ok {
			*value = false
		}
		return nil
	case strings.Contains(expression, "/*codemcp:surface*/"):
		value, ok := result.(*domSnapshot)
		if !ok {
			return errors.New("surface result type mismatch")
		}
		if len(tab.surfaces) == 0 {
			return errors.New("no surface fixture")
		}
		index := tab.surfaceAt
		if index >= len(tab.surfaces) {
			index = len(tab.surfaces) - 1
		} else {
			tab.surfaceAt++
		}
		*value = tab.surfaces[index]
		return nil
	case strings.Contains(expression, "/*codemcp:controls*/"):
		if tab.controlsErr != nil {
			return tab.controlsErr
		}
		value, ok := result.(*controlResult)
		if !ok {
			return errors.New("controls result type mismatch")
		}
		*value = tab.controls
		return nil
	case strings.Contains(expression, "/*codemcp:connector*/"):
		if tab.connectorErr != nil {
			return tab.connectorErr
		}
		value, ok := result.(*connectorResult)
		if !ok {
			return errors.New("connector result type mismatch")
		}
		*value = tab.connector
		return nil
	case strings.Contains(expression, "/*codemcp:attach*/"):
		if tab.attachErr != nil {
			return tab.attachErr
		}
		value, ok := result.(*promptAttachResult)
		if !ok {
			return errors.New("attach result type mismatch")
		}
		*value = promptAttachResult{Text: tab.attachText, ConnectorCount: tab.attachConnectorCount}
		return nil
	case strings.Contains(expression, "/*codemcp:send*/"):
		if tab.sendErr != nil {
			return tab.sendErr
		}
		value, ok := result.(*sendResult)
		if !ok {
			return errors.New("send result type mismatch")
		}
		*value = tab.send
		return nil
	case strings.Contains(expression, "/*codemcp:stop*/"):
		tab.stopCalls++
		if value, ok := result.(*stopResult); ok {
			*value = stopResult{Stopped: true}
		}
		return nil
	default:
		return errors.New("unexpected browser expression")
	}
}

func (tab *driverFakeTab) Done() <-chan struct{} { return tab.done }

func (tab *driverFakeTab) Err() error {
	tab.mu.Lock()
	defer tab.mu.Unlock()
	return tab.err
}

func (tab *driverFakeTab) Close(context.Context) error {
	tab.mu.Lock()
	tab.closed = true
	tab.mu.Unlock()
	return nil
}

func (tab *driverFakeTab) setSurfaces(values ...domSnapshot) {
	tab.mu.Lock()
	tab.surfaces = append([]domSnapshot(nil), values...)
	tab.surfaceAt = 0
	tab.mu.Unlock()
}

func readySurface() domSnapshot {
	return domSnapshot{
		Origin: "https://chatgpt.com", Path: "/", TemporaryChat: true,
		ComposerCount: 1, SendVisible: true, SendEnabled: true,
	}
}

func testDriver(t *testing.T, tab *driverFakeTab) *Driver {
	t.Helper()
	driver, err := NewDriver(tab, DriverOptions{
		AuthProbe:              driverAuthProbeReady{},
		SurfaceTimeout:         20 * time.Millisecond,
		ControlTimeout:         20 * time.Millisecond,
		SubmitTimeout:          20 * time.Millisecond,
		TurnTimeout:            250 * time.Millisecond,
		CompletionProofTimeout: 5 * time.Millisecond,
		StableFinalFor:         2 * time.Millisecond,
		PollInterval:           time.Millisecond,
		CloseTimeout:           20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return driver
}

type driverAuthProbeReady struct{}

func (driverAuthProbeReady) Probe(context.Context, browser.BrowserTab) (AuthEvidence, error) {
	return AuthEvidence{OriginOK: true, TemporaryChat: true, Authenticated: true, Composer: true}, nil
}

func TestDriverStartUsesFreshTemporaryChatAndStableFinal(t *testing.T) {
	tab := newDriverFakeTab()
	fresh := readySurface()
	submitted := fresh
	submitted.UserTurns = 1
	submitted.AssistantTurns = 1
	submitted.Generating = true
	tool := submitted
	tool.ToolActive = true
	tool.LatestAssistantText = "Working"
	final := fresh
	final.UserTurns = 1
	final.AssistantTurns = 1
	final.LatestAssistantText = "Final answer"
	final.CompletionActionVisible = true
	tab.setSurfaces(fresh, fresh, submitted, tool, final)
	tab.attachText = "bootstrap\n\ntask"
	tab.attachConnectorCount = 1

	driver := testDriver(t, tab)
	result, err := driver.Start(context.Background(), TurnRequest{
		Bootstrap: "bootstrap", Prompt: "task", Model: "GPT-5.6 Sol",
		ReasoningEffort: "high", ConnectorName: "CodeMCP", RequireConnector: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != TurnFinal || result.Text != "Final answer" || !result.ToolObserved {
		t.Fatalf("result=%#v", result)
	}
	if result.Model != "GPT-5.6 Sol" || result.Effort != "High" || result.Connector != "CodeMCP" {
		t.Fatalf("selection projection=%#v", result)
	}
	if len(tab.navigations) != 1 || tab.navigations[0] != TemporaryChatURL {
		t.Fatalf("navigations=%v", tab.navigations)
	}
}

func TestDriverFollowUpReusesSameTabAndConversation(t *testing.T) {
	tab := newDriverFakeTab()
	fresh := readySurface()
	submitted := fresh
	submitted.UserTurns, submitted.AssistantTurns, submitted.Generating = 1, 1, true
	final := fresh
	final.UserTurns, final.AssistantTurns = 1, 1
	final.LatestAssistantText, final.CompletionActionVisible = "first", true
	tab.setSurfaces(fresh, fresh, submitted, final)
	tab.attachText, tab.attachConnectorCount = "first task", 1
	driver := testDriver(t, tab)
	if _, err := driver.Start(context.Background(), TurnRequest{Prompt: "first task", ConnectorName: "CodeMCP", RequireConnector: true}); err != nil {
		t.Fatal(err)
	}

	retained := final
	baseline := final
	secondSubmitted := final
	secondSubmitted.UserTurns, secondSubmitted.AssistantTurns, secondSubmitted.Generating = 2, 2, true
	secondFinal := fresh
	secondFinal.UserTurns, secondFinal.AssistantTurns = 2, 2
	secondFinal.LatestAssistantText, secondFinal.CompletionActionVisible = "second", true
	tab.setSurfaces(retained, baseline, secondSubmitted, secondFinal)
	tab.attachText = "follow up"
	result, err := driver.FollowUp(context.Background(), "follow up")
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "second" {
		t.Fatalf("result=%#v", result)
	}
	if len(tab.navigations) != 1 {
		t.Fatalf("follow-up navigated away from retained conversation: %v", tab.navigations)
	}
}

func TestDriverFailsClosedOnExplicitModelMismatch(t *testing.T) {
	tab := newDriverFakeTab()
	tab.setSurfaces(readySurface(), readySurface())
	tab.controlsErr = errors.New("requested model row unavailable or ambiguous")
	driver := testDriver(t, tab)
	_, err := driver.Start(context.Background(), TurnRequest{Prompt: "task", Model: "GPT-unknown"})
	if !IsDriverErrorCode(err, ErrorModelMismatch) {
		t.Fatalf("error=%v", err)
	}
}

func TestDriverFailsClosedOnExplicitEffortMismatch(t *testing.T) {
	tab := newDriverFakeTab()
	tab.setSurfaces(readySurface(), readySurface())
	tab.controlsErr = errors.New("requested effort is locked")
	driver := testDriver(t, tab)
	_, err := driver.Start(context.Background(), TurnRequest{Prompt: "task", ReasoningEffort: "high"})
	if !IsDriverErrorCode(err, ErrorEffortMismatch) {
		t.Fatalf("error=%v", err)
	}
}

func TestDriverFailsClosedOnConnectorMismatch(t *testing.T) {
	tab := newDriverFakeTab()
	tab.setSurfaces(readySurface(), readySurface())
	tab.connector = connectorResult{Selected: false, Rows: []string{"Other App"}}
	driver := testDriver(t, tab)
	_, err := driver.Start(context.Background(), TurnRequest{Prompt: "task", ConnectorName: "CodeMCP", RequireConnector: true})
	if !IsDriverErrorCode(err, ErrorConnectorMismatch) {
		t.Fatalf("error=%v", err)
	}
}

func TestDriverFailsClosedOnRateLimitAndUIDrift(t *testing.T) {
	t.Run("rate limit", func(t *testing.T) {
		tab := newDriverFakeTab()
		surface := readySurface()
		surface.RateLimited = true
		tab.setSurfaces(surface)
		driver := testDriver(t, tab)
		_, err := driver.Start(context.Background(), TurnRequest{Prompt: "task"})
		if !IsDriverErrorCode(err, ErrorRateLimited) {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("ambiguous composer", func(t *testing.T) {
		tab := newDriverFakeTab()
		fresh := readySurface()
		drift := fresh
		drift.ComposerCount = 2
		tab.setSurfaces(fresh, drift)
		driver := testDriver(t, tab)
		_, err := driver.Start(context.Background(), TurnRequest{Prompt: "task"})
		if !IsDriverErrorCode(err, ErrorUIContract) {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestDriverRejectsUnprovenFinalCompletion(t *testing.T) {
	tab := newDriverFakeTab()
	fresh := readySurface()
	submitted := fresh
	submitted.UserTurns, submitted.AssistantTurns, submitted.Generating = 1, 1, true
	unproven := fresh
	unproven.UserTurns, unproven.AssistantTurns = 1, 1
	unproven.LatestAssistantText = "looks done"
	tab.setSurfaces(fresh, fresh, submitted, unproven)
	tab.attachText = "task"
	driver := testDriver(t, tab)
	_, err := driver.Start(context.Background(), TurnRequest{Prompt: "task"})
	if !IsDriverErrorCode(err, ErrorCompletionAmbiguous) {
		t.Fatalf("error=%v", err)
	}
}

func TestDriverDetectsAuthenticationRedirect(t *testing.T) {
	tab := newDriverFakeTab()
	redirect := readySurface()
	redirect.Origin = "https://auth.openai.com"
	redirect.TemporaryChat = false
	tab.setSurfaces(redirect)
	driver := testDriver(t, tab)
	_, err := driver.Start(context.Background(), TurnRequest{Prompt: "task"})
	if !IsDriverErrorCode(err, ErrorAuthentication) {
		t.Fatalf("error=%v", err)
	}
}

func TestDriverRejectsNonFreshTemporaryChat(t *testing.T) {
	tab := newDriverFakeTab()
	surface := readySurface()
	surface.UserTurns = 1
	surface.AssistantTurns = 1
	tab.setSurfaces(surface)
	driver := testDriver(t, tab)
	_, err := driver.Start(context.Background(), TurnRequest{Prompt: "task"})
	if !IsDriverErrorCode(err, ErrorUIContract) || !strings.Contains(err.Error(), "fresh Temporary Chat") {
		t.Fatalf("error=%v", err)
	}
}

func TestDriverClassifiesEffortFailureWhenModelIsAlsoExplicit(t *testing.T) {
	tab := newDriverFakeTab()
	tab.setSurfaces(readySurface(), readySurface())
	tab.controlsErr = errors.New("requested effort slider tick is locked")
	driver := testDriver(t, tab)
	_, err := driver.Start(context.Background(), TurnRequest{Prompt: "task", Model: "GPT-5.6 Sol", ReasoningEffort: "high"})
	if !IsDriverErrorCode(err, ErrorEffortMismatch) {
		t.Fatalf("error=%v", err)
	}
}

func TestDriverCancelStopsAndClosesTab(t *testing.T) {
	tab := newDriverFakeTab()
	driver := testDriver(t, tab)
	driver.setState(TurnGenerating)
	if err := driver.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	if driver.State() != TurnCancelled || tab.stopCalls != 1 || !tab.closed {
		t.Fatalf("state=%s stopCalls=%d closed=%t", driver.State(), tab.stopCalls, tab.closed)
	}
}

func TestDriverPromptBoundAppliesBeforeBrowserNavigation(t *testing.T) {
	tab := newDriverFakeTab()
	driver, err := NewDriver(tab, DriverOptions{AuthProbe: driverAuthProbeReady{}, MaxPromptBytes: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := driver.Start(context.Background(), TurnRequest{Prompt: "123456"}); err == nil {
		t.Fatal("oversized prompt was accepted")
	}
	if len(tab.navigations) != 0 {
		t.Fatalf("oversized prompt navigated browser: %v", tab.navigations)
	}
}

func TestDriverExpressionsNeverSendConversationBackendRequests(t *testing.T) {
	expressions := []string{
		domSnapshotExpression(),
		configureControlsExpression("GPT-5.6 Sol", 2, "High"),
		connectorExpression("CodeMCP", "@codemcp"),
		attachPromptExpression("task", "CodeMCP"),
		activateSendExpression(),
		stopExpression(),
	}
	for _, expression := range expressions {
		lower := strings.ToLower(expression)
		for _, forbidden := range []string{"backend-api/f/conversation", "backend-api/conversation", "fetch("} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("driver expression calls undocumented conversation backend %q", forbidden)
			}
		}
	}
}

func TestResponseProjectionUsesAnswerRootsInsteadOfWholeAssistantContainer(t *testing.T) {
	expression := domSnapshotExpression()
	for _, required := range []string{"answerRootSelector", "data-markdown-text-style", "data-streaming-response-status", "latest_assistant_text:answerText"} {
		if !strings.Contains(expression, required) {
			t.Fatalf("response projection missing %q", required)
		}
	}
	if strings.Contains(expression, "latest_assistant_text:(latest?.innerText") {
		t.Fatal("response projection regressed to whole assistant-container text")
	}
}

func TestNormalizeEffortIsChatGPTSpecificAndExact(t *testing.T) {
	for input, want := range map[string]string{
		"low": "Instant", "medium": "Medium", "high": "High", "xhigh": "Extra High", "max": "Pro",
	} {
		_, label, err := normalizeEffort(input)
		if err != nil || label != want {
			t.Fatalf("effort %q label=%q err=%v want=%q", input, label, err, want)
		}
	}
	if _, _, err := normalizeEffort("guess"); err == nil {
		t.Fatal("unknown effort was guessed")
	}
}
