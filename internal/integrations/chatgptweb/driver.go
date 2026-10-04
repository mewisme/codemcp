package chatgptweb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/integrations/browser"
)

const (
	DefaultSurfaceTimeout         = 30 * time.Second
	DefaultControlTimeout         = 15 * time.Second
	DefaultSubmitTimeout          = 20 * time.Second
	DefaultTurnTimeout            = 30 * time.Minute
	DefaultCompletionProofTimeout = 60 * time.Second
	DefaultStableFinalFor         = 1500 * time.Millisecond
	DefaultDriverPollInterval     = 150 * time.Millisecond
	DefaultDriverCloseTimeout     = 3 * time.Second
)

type DriverOptions struct {
	AuthProbe              AuthProbe
	MaxPromptBytes         int
	SurfaceTimeout         time.Duration
	ControlTimeout         time.Duration
	SubmitTimeout          time.Duration
	TurnTimeout            time.Duration
	CompletionProofTimeout time.Duration
	StableFinalFor         time.Duration
	PollInterval           time.Duration
	CloseTimeout           time.Duration
}

type Driver struct {
	tab                    browser.BrowserTab
	auth                   AuthProbe
	maxPromptBytes         int
	surfaceTimeout         time.Duration
	controlTimeout         time.Duration
	submitTimeout          time.Duration
	turnTimeout            time.Duration
	completionProofTimeout time.Duration
	stableFinalFor         time.Duration
	pollInterval           time.Duration
	closeTimeout           time.Duration

	opMu       sync.Mutex
	mu         sync.Mutex
	started    bool
	closed     bool
	turnCancel context.CancelFunc
	request    TurnRequest
	state      TurnState
}

func NewDriver(tab browser.BrowserTab, options DriverOptions) (*Driver, error) {
	if tab == nil {
		return nil, errors.New("ChatGPT Web driver requires a browser tab")
	}
	if options.AuthProbe == nil {
		options.AuthProbe = DOMAuthProbe{}
	}
	if options.MaxPromptBytes == 0 {
		options.MaxPromptBytes = DefaultMaxPromptBytes
	}
	if options.MaxPromptBytes < 1 {
		return nil, errors.New("ChatGPT Web prompt byte limit must be positive")
	}
	if options.SurfaceTimeout == 0 {
		options.SurfaceTimeout = DefaultSurfaceTimeout
	}
	if options.ControlTimeout == 0 {
		options.ControlTimeout = DefaultControlTimeout
	}
	if options.SubmitTimeout == 0 {
		options.SubmitTimeout = DefaultSubmitTimeout
	}
	if options.TurnTimeout == 0 {
		options.TurnTimeout = DefaultTurnTimeout
	}
	if options.CompletionProofTimeout == 0 {
		options.CompletionProofTimeout = DefaultCompletionProofTimeout
	}
	if options.StableFinalFor == 0 {
		options.StableFinalFor = DefaultStableFinalFor
	}
	if options.PollInterval == 0 {
		options.PollInterval = DefaultDriverPollInterval
	}
	if options.CloseTimeout == 0 {
		options.CloseTimeout = DefaultDriverCloseTimeout
	}
	for name, value := range map[string]time.Duration{"surface timeout": options.SurfaceTimeout, "control timeout": options.ControlTimeout, "submit timeout": options.SubmitTimeout, "turn timeout": options.TurnTimeout, "completion proof timeout": options.CompletionProofTimeout, "stable final duration": options.StableFinalFor, "poll interval": options.PollInterval, "close timeout": options.CloseTimeout} {
		if value <= 0 {
			return nil, fmt.Errorf("ChatGPT Web %s must be positive", name)
		}
	}
	return &Driver{tab: tab, auth: options.AuthProbe, maxPromptBytes: options.MaxPromptBytes, surfaceTimeout: options.SurfaceTimeout, controlTimeout: options.ControlTimeout, submitTimeout: options.SubmitTimeout, turnTimeout: options.TurnTimeout, completionProofTimeout: options.CompletionProofTimeout, stableFinalFor: options.StableFinalFor, pollInterval: options.PollInterval, closeTimeout: options.CloseTimeout, state: TurnPreparing}, nil
}

func (driver *Driver) Start(ctx context.Context, request TurnRequest) (TurnResult, error) {
	if driver == nil {
		return TurnResult{}, errors.New("ChatGPT Web driver is unavailable")
	}
	driver.opMu.Lock()
	defer driver.opMu.Unlock()
	if err := driver.ensureOpen(); err != nil {
		return TurnResult{}, err
	}
	driver.mu.Lock()
	if driver.started {
		driver.mu.Unlock()
		return TurnResult{}, errors.New("ChatGPT Web driver already started")
	}
	driver.mu.Unlock()
	request, prompt, err := driver.normalizeInitialRequest(request)
	if err != nil {
		return TurnResult{}, err
	}
	turnCtx, cancel := context.WithCancel(nonNilContext(ctx))
	driver.setTurnCancel(cancel)
	defer driver.clearTurnCancel(cancel)
	defer cancel()
	if err := driver.tab.Navigate(turnCtx, TemporaryChatURL); err != nil {
		return TurnResult{}, driverError(ErrorUIContract, "open Temporary Chat", "could not open the exact Temporary Chat surface", err)
	}
	if err := driver.waitFreshSurface(turnCtx); err != nil {
		return TurnResult{}, err
	}
	request.Bootstrap = ""
	request.Prompt = ""
	driver.mu.Lock()
	driver.request = request
	driver.started = true
	driver.mu.Unlock()
	return driver.runTurn(turnCtx, request, prompt)
}

func (driver *Driver) FollowUp(ctx context.Context, prompt string) (TurnResult, error) {
	if driver == nil {
		return TurnResult{}, errors.New("ChatGPT Web driver is unavailable")
	}
	driver.opMu.Lock()
	defer driver.opMu.Unlock()
	if err := driver.ensureOpen(); err != nil {
		return TurnResult{}, err
	}
	driver.mu.Lock()
	started := driver.started
	request := driver.request
	state := driver.state
	driver.mu.Unlock()
	if !started {
		return TurnResult{}, errors.New("ChatGPT Web driver has not started")
	}
	if state != TurnFinal {
		return TurnResult{}, fmt.Errorf("ChatGPT Web follow-up requires an idle final turn, current state is %s", state)
	}
	prompt = normalizePrompt(prompt)
	if prompt == "" {
		return TurnResult{}, errors.New("ChatGPT Web follow-up prompt is required")
	}
	routedPrompt := prompt
	if request.RequireConnector {
		routedPrompt = connectorRoutePrefix(request.ConnectorName) + prompt
	}
	if len([]byte(routedPrompt)) > driver.maxPromptBytes {
		return TurnResult{}, fmt.Errorf("ChatGPT Web follow-up exceeds %d bytes", driver.maxPromptBytes)
	}
	turnCtx, cancel := context.WithCancel(nonNilContext(ctx))
	driver.setTurnCancel(cancel)
	defer driver.clearTurnCancel(cancel)
	defer cancel()
	if err := driver.assertRetainedSurface(turnCtx); err != nil {
		return TurnResult{}, err
	}
	return driver.runTurn(turnCtx, request, prompt)
}

func (driver *Driver) Cancel(ctx context.Context) error {
	if driver == nil {
		return nil
	}
	driver.mu.Lock()
	if driver.closed {
		driver.mu.Unlock()
		return nil
	}
	driver.closed = true
	cancel := driver.turnCancel
	driver.state = TurnCancelled
	driver.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	closeCtx, closeCancel := context.WithTimeout(nonNilContext(ctx), driver.closeTimeout)
	defer closeCancel()
	_, _ = stopTurn(closeCtx, driver.tab)
	if err := driver.tab.Close(closeCtx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func (driver *Driver) State() TurnState {
	if driver == nil {
		return TurnCancelled
	}
	driver.mu.Lock()
	defer driver.mu.Unlock()
	return driver.state
}

func (driver *Driver) runTurn(ctx context.Context, request TurnRequest, prompt string) (TurnResult, error) {
	turnCtx, cancel := context.WithTimeout(ctx, driver.turnTimeout)
	defer cancel()
	baseline, err := driver.inspectHealthy(turnCtx, "preflight")
	if err != nil {
		return TurnResult{}, err
	}
	if baseline.Generating {
		return TurnResult{}, uiContractError("preflight", "previous ChatGPT turn is still generating")
	}
	if baseline.ComposerCount != 1 {
		return TurnResult{}, uiContractError("preflight", "expected exactly one visible composer, found %d", baseline.ComposerCount)
	}

	driver.setState(TurnPreparing)
	controlCtx, controlCancel := context.WithTimeout(turnCtx, driver.controlTimeout)
	controls, controlErr := configureControls(controlCtx, driver.tab, request.Model, request.ReasoningEffort)
	controlCancel()
	if controlErr != nil {
		code := ErrorModelMismatch
		controlDetail := strings.ToLower(controlErr.Error())
		if strings.TrimSpace(request.Model) == "" || strings.Contains(controlDetail, "effort") || strings.Contains(controlDetail, "slider") {
			code = ErrorEffortMismatch
		}
		return TurnResult{}, driverError(code, "model controls", "explicit model/reasoning selection could not be selected and verified", controlErr)
	}
	if strings.TrimSpace(request.Model) != "" && !controls.ModelVerified {
		return TurnResult{}, driverError(ErrorModelMismatch, "model controls", "requested model was not verified", nil)
	}
	if strings.TrimSpace(request.ReasoningEffort) != "" && !controls.EffortVerified {
		return TurnResult{}, driverError(ErrorEffortMismatch, "model controls", "requested reasoning effort was not verified", nil)
	}

	connector := ""
	if request.RequireConnector {
		connector = strings.TrimSpace(request.ConnectorName)
		if connector == "" {
			connector = DefaultConnectorName
		}
	}

	attachCtx, attachCancel := context.WithTimeout(turnCtx, driver.controlTimeout)
	attached, attachErr := attachPrompt(attachCtx, driver.tab, prompt, connector)
	attachCancel()
	if attachErr != nil {
		return TurnResult{}, driverError(ErrorUIContract, "composer", "prompt could not be attached without altering connector state", attachErr)
	}
	if request.RequireConnector && attached.ConnectorCount != 1 {
		return TurnResult{}, driverError(ErrorConnectorMismatch, "connector", fmt.Sprintf("connector route @%s was not retained before submission", connector), nil)
	}
	if normalizePromptForIntegrity(attached.Text) != normalizePromptForIntegrity(prompt) {
		return TurnResult{}, uiContractError("composer", "prompt body changed before submission")
	}

	driver.setState(TurnSubmitting)
	sendCtx, sendCancel := context.WithTimeout(turnCtx, driver.submitTimeout)
	activation, sendErr := activateSend(sendCtx, driver.tab)
	sendCancel()
	if sendErr != nil {
		return TurnResult{}, driverError(ErrorSubmissionAmbiguous, "submit", "Send could not be activated safely; the prompt will not be resent", sendErr)
	}
	if !activation.Activated {
		return TurnResult{}, driverError(ErrorSubmissionAmbiguous, "submit", "Send activation was not proven; the prompt will not be resent", nil)
	}

	if err := driver.waitSubmissionAccepted(turnCtx, baseline); err != nil {
		return TurnResult{}, err
	}
	result, err := driver.waitFinal(turnCtx, baseline)
	if err != nil {
		return TurnResult{}, err
	}
	_, effortLabel, _ := normalizeEffort(request.ReasoningEffort)
	result.Model = strings.TrimSpace(request.Model)
	result.Effort = effortLabel
	result.Connector = connector
	return result, nil
}

func (driver *Driver) waitSubmissionAccepted(ctx context.Context, baseline domSnapshot) error {
	deadline := time.NewTimer(driver.submitTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(driver.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return driver.contextTurnError("submit", ctx.Err())
		case <-driver.tab.Done():
			return driverError(ErrorUpstream, "submit", "browser tab closed before submission acceptance was proven", driver.tab.Err())
		case <-deadline.C:
			return driverError(ErrorSubmissionAmbiguous, "submit", "Send was activated but acceptance could not be proven; the prompt will not be resent", nil)
		case <-ticker.C:
			snapshot, err := driver.inspectHealthy(ctx, "submit")
			if err != nil {
				return err
			}
			if snapshot.UserTurns > baseline.UserTurns || snapshot.AssistantTurns > baseline.AssistantTurns || snapshot.Generating {
				driver.setState(TurnGenerating)
				return nil
			}
		}
	}
}

func (driver *Driver) waitFinal(ctx context.Context, baseline domSnapshot) (TurnResult, error) {
	ticker := time.NewTicker(driver.pollInterval)
	defer ticker.Stop()
	stableText := ""
	var stableSince time.Time
	var unprovenSince time.Time
	var emptySince time.Time
	var missingSince time.Time
	toolObserved := false
	for {
		select {
		case <-ctx.Done():
			return TurnResult{}, driver.contextTurnError("turn", ctx.Err())
		case <-driver.tab.Done():
			return TurnResult{}, driverError(ErrorUpstream, "turn", "browser tab closed before a stable final response", driver.tab.Err())
		case <-ticker.C:
			snapshot, err := driver.inspectHealthy(ctx, "turn")
			if err != nil {
				return TurnResult{}, err
			}
			if snapshot.AssistantTurns < baseline.AssistantTurns {
				return TurnResult{}, uiContractError("turn", "assistant turn count moved backwards")
			}
			if snapshot.ToolActive {
				toolObserved = true
				driver.setState(TurnTool)
			} else if snapshot.Generating {
				driver.setState(TurnGenerating)
			}
			text := normalizePrompt(snapshot.LatestAssistantText)
			candidate := snapshot.AssistantTurns > baseline.AssistantTurns && text != "" && !snapshot.Generating && !snapshot.ToolActive
			if snapshot.AssistantTurns <= baseline.AssistantTurns && !snapshot.Generating && !snapshot.ToolActive {
				if missingSince.IsZero() {
					missingSince = time.Now()
				}
				if time.Since(missingSince) >= driver.completionProofTimeout {
					return TurnResult{}, driverError(ErrorCompletionAmbiguous, "turn", "submission was accepted but no new assistant turn could be proven", nil)
				}
			} else {
				missingSince = time.Time{}
			}
			if snapshot.AssistantTurns > baseline.AssistantTurns && text == "" && !snapshot.Generating && !snapshot.ToolActive && snapshot.CompletionActionVisible {
				if emptySince.IsZero() {
					emptySince = time.Now()
				}
				if time.Since(emptySince) >= driver.completionProofTimeout {
					return TurnResult{}, driverError(ErrorCompletionAmbiguous, "turn", "completed-turn evidence exposed no stable final answer projection; the ChatGPT DOM may have changed", nil)
				}
			} else {
				emptySince = time.Time{}
			}
			if candidate && snapshot.CompletionActionVisible {
				unprovenSince = time.Time{}
				if text != stableText {
					stableText = text
					stableSince = time.Now()
				} else if !stableSince.IsZero() && time.Since(stableSince) >= driver.stableFinalFor {
					driver.setState(TurnFinal)
					return TurnResult{State: TurnFinal, Text: text, ToolObserved: toolObserved}, nil
				}
				continue
			}
			stableText = ""
			stableSince = time.Time{}
			if candidate && !snapshot.CompletionActionVisible {
				if unprovenSince.IsZero() {
					unprovenSince = time.Now()
				}
				if time.Since(unprovenSince) >= driver.completionProofTimeout {
					return TurnResult{}, driverError(ErrorCompletionAmbiguous, "turn", "ChatGPT stopped generating but did not expose completed-turn evidence", nil)
				}
			} else {
				unprovenSince = time.Time{}
			}
		}
	}
}

func (driver *Driver) waitFreshSurface(ctx context.Context) error {
	probeCtx, cancel := context.WithTimeout(ctx, driver.surfaceTimeout)
	defer cancel()
	ticker := time.NewTicker(driver.pollInterval)
	defer ticker.Stop()
	for {
		if err := dismissTemporaryChatOnboarding(probeCtx, driver.tab); err != nil {
			return driverError(ErrorUIContract, "Temporary Chat", "Temporary Chat onboarding could not be resolved safely", err)
		}
		evidence, authErr := driver.auth.Probe(probeCtx, driver.tab)
		snapshot, domErr := inspectDOM(probeCtx, driver.tab)
		if domErr == nil {
			if snapshot.Origin != "" && snapshot.Origin != "https://chatgpt.com" {
				return driverError(ErrorAuthentication, "Temporary Chat", "ChatGPT redirected away from the authenticated application", nil)
			}
			if snapshot.SessionExpired {
				return driverError(ErrorAuthentication, "Temporary Chat", "ChatGPT session expired; run cm integration chatgpt-web login", nil)
			}
			if snapshot.RateLimited {
				return driverError(ErrorRateLimited, "Temporary Chat", "ChatGPT is rate limited", nil)
			}
			if snapshot.TemporaryChat && snapshot.ComposerCount == 1 && (snapshot.UserTurns != 0 || snapshot.AssistantTurns != 0) {
				return uiContractError("Temporary Chat", "fresh Temporary Chat unexpectedly contained existing conversation turns")
			}
		}
		if authErr == nil && domErr == nil && evidence.Ready() && snapshot.TemporaryChat && snapshot.ComposerCount == 1 {
			return nil
		}
		select {
		case <-probeCtx.Done():
			if authErr != nil {
				return driverError(ErrorAuthentication, "Temporary Chat", "server-authenticated composer could not be verified", authErr)
			}
			return driverError(ErrorUIContract, "Temporary Chat", "exact Temporary Chat state and one visible composer could not be proven", domErr)
		case <-driver.tab.Done():
			return driverError(ErrorUpstream, "Temporary Chat", "browser tab closed during setup", driver.tab.Err())
		case <-ticker.C:
		}
	}
}

func (driver *Driver) assertRetainedSurface(ctx context.Context) error {
	snapshot, err := driver.inspectHealthy(ctx, "follow-up")
	if err != nil {
		return err
	}
	if snapshot.Origin != "https://chatgpt.com" {
		return driverError(ErrorAuthentication, "follow-up", "ChatGPT conversation left the authenticated application", nil)
	}
	if snapshot.ComposerCount != 1 {
		return uiContractError("follow-up", "expected exactly one visible composer, found %d", snapshot.ComposerCount)
	}
	if snapshot.Generating {
		return uiContractError("follow-up", "cannot send while the previous turn is still generating")
	}
	return nil
}

func (driver *Driver) inspectHealthy(ctx context.Context, op string) (domSnapshot, error) {
	observeCtx, cancel := context.WithTimeout(ctx, driver.controlTimeout)
	defer cancel()
	snapshot, err := inspectDOM(observeCtx, driver.tab)
	if err != nil {
		return domSnapshot{}, driverError(ErrorUIContract, op, "ChatGPT DOM observation failed", err)
	}
	if snapshot.SessionExpired {
		return domSnapshot{}, driverError(ErrorAuthentication, op, "ChatGPT session expired; run cm integration chatgpt-web login", nil)
	}
	if snapshot.RateLimited {
		return domSnapshot{}, driverError(ErrorRateLimited, op, "ChatGPT rate limit is active; retry later", nil)
	}
	if snapshot.UpstreamError {
		return domSnapshot{}, driverError(ErrorUpstream, op, "ChatGPT displayed a terminal response error", nil)
	}
	if snapshot.Origin != "" && snapshot.Origin != "https://chatgpt.com" {
		return domSnapshot{}, driverError(ErrorAuthentication, op, "ChatGPT redirected away from chatgpt.com", nil)
	}
	return snapshot, nil
}

func (driver *Driver) normalizeInitialRequest(request TurnRequest) (TurnRequest, string, error) {
	request.Prompt = normalizePrompt(request.Prompt)
	request.Bootstrap = normalizePrompt(request.Bootstrap)
	request.Model = strings.TrimSpace(request.Model)
	request.ReasoningEffort = strings.TrimSpace(request.ReasoningEffort)
	request.ConnectorName = strings.TrimSpace(request.ConnectorName)
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	if request.RequireConnector {
		if request.ConnectorName == "" {
			request.ConnectorName = DefaultConnectorName
		}
		if request.WorkspaceID == "" {
			return TurnRequest{}, "", driverError(ErrorConnectorMismatch, "connector", "workspace ID is required for managed connector context", nil)
		}
	}
	if request.Prompt == "" {
		return TurnRequest{}, "", errors.New("ChatGPT Web task prompt is required")
	}
	if _, _, err := normalizeEffort(request.ReasoningEffort); err != nil {
		return TurnRequest{}, "", err
	}
	prompt := request.Prompt
	if request.Bootstrap != "" {
		prompt = request.Bootstrap + "\n\n" + request.Prompt
	}
	routedPrompt := prompt
	if request.RequireConnector {
		routedPrompt = connectorRoutePrefix(request.ConnectorName) + prompt
	}
	if len([]byte(routedPrompt)) > driver.maxPromptBytes {
		return TurnRequest{}, "", fmt.Errorf("ChatGPT Web composed prompt exceeds %d bytes", driver.maxPromptBytes)
	}
	return request, prompt, nil
}

func normalizePromptForIntegrity(value string) string {
	return strings.Join(strings.Fields(normalizePrompt(value)), " ")
}

func (driver *Driver) contextTurnError(op string, err error) error {
	if errors.Is(err, context.Canceled) {
		return driverError(ErrorCancelled, op, "turn cancelled", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return driverError(ErrorCompletionAmbiguous, op, "bounded ChatGPT turn deadline expired before stable completion", err)
	}
	return err
}

func (driver *Driver) ensureOpen() error {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	if driver.closed {
		return errors.New("ChatGPT Web driver is closed")
	}
	return nil
}
func (driver *Driver) setState(state TurnState) {
	driver.mu.Lock()
	driver.state = state
	driver.mu.Unlock()
}
func (driver *Driver) setTurnCancel(cancel context.CancelFunc) {
	driver.mu.Lock()
	driver.turnCancel = cancel
	driver.mu.Unlock()
}
func (driver *Driver) clearTurnCancel(cancel context.CancelFunc) {
	driver.mu.Lock()
	driver.turnCancel = nil
	driver.mu.Unlock()
}
func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
