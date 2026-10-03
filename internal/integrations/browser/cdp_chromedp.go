package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/inspector"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

type chromedpConnector struct{}

func newChromedpConnector() BrowserConnector { return chromedpConnector{} }

func (chromedpConnector) Connect(ctx context.Context, endpoint BrowserEndpoint) (BrowserClient, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	url := strings.TrimSpace(endpoint.URL)
	if url == "" {
		return nil, errors.New("browser CDP endpoint is required")
	}
	allocatorCtx, allocatorCancel := chromedp.NewRemoteAllocator(context.Background(), url)
	var contextOptions []chromedp.ContextOption
	if targetID, targetErr := existingPageTargetID(ctx, url); targetErr == nil && targetID != "" {
		contextOptions = append(contextOptions, chromedp.WithTargetID(targetID))
	}
	browserCtx, browserCancel := chromedp.NewContext(allocatorCtx, contextOptions...)
	ready := make(chan error, 1)
	go func() {
		ready <- chromedp.Run(browserCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			_, _, _, _, _, err := browser.GetVersion().Do(ctx)
			return err
		}))
	}()
	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case err = <-ready:
	}
	if err != nil {
		browserCancel()
		allocatorCancel()
		return nil, err
	}
	cdpContext := chromedp.FromContext(browserCtx)
	if cdpContext == nil || cdpContext.Target == nil || strings.TrimSpace(cdpContext.Target.TargetID.String()) == "" {
		browserCancel()
		allocatorCancel()
		return nil, errors.New("browser bootstrap target id is unavailable")
	}
	bootstrap := newChromedpBrowserTab(browserCtx, cdpContext.Target.TargetID.String(), true)
	chromedp.ListenTarget(browserCtx, func(event any) {
		if _, ok := event.(*inspector.EventTargetCrashed); ok {
			bootstrap.fail(errors.New("browser tab target crashed"))
		}
	})
	go bootstrap.observe()
	client := &chromedpBrowserClient{
		ctx: browserCtx, cancel: browserCancel,
		allocatorCancel: allocatorCancel, done: make(chan struct{}), bootstrap: bootstrap,
	}
	go client.observe()
	return client, nil
}

type chromedpBrowserClient struct {
	ctx             context.Context
	cancel          context.CancelFunc
	allocatorCancel context.CancelFunc
	done            chan struct{}
	closing         atomic.Bool

	mu        sync.Mutex
	err       error
	bootstrap *chromedpBrowserTab
}

func (client *chromedpBrowserClient) observe() {
	<-client.ctx.Done()
	client.mu.Lock()
	if !client.closing.Load() {
		client.err = client.ctx.Err()
	}
	client.mu.Unlock()
	close(client.done)
}

func (client *chromedpBrowserClient) NewTab(ctx context.Context, url string) (BrowserTab, error) {
	if client == nil {
		return nil, errors.New("browser CDP client is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-client.done:
		return nil, errors.New("browser CDP connection is closed")
	default:
	}
	client.mu.Lock()
	bootstrap := client.bootstrap
	client.bootstrap = nil
	client.mu.Unlock()
	if bootstrap != nil {
		if next := strings.TrimSpace(url); next != "" && next != "about:blank" {
			if err := bootstrap.Navigate(ctx, next); err != nil {
				_ = bootstrap.Close(context.Background())
				return nil, err
			}
		}
		return bootstrap, nil
	}
	tabCtx, tabCancel := chromedp.NewContext(client.ctx)
	ready := make(chan error, 1)
	go func() {
		ready <- chromedp.Run(tabCtx, chromedp.Navigate(url))
	}()
	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case err = <-ready:
	}
	if err != nil {
		tabCancel()
		return nil, err
	}
	cdpContext := chromedp.FromContext(tabCtx)
	if cdpContext == nil || cdpContext.Target == nil || strings.TrimSpace(cdpContext.Target.TargetID.String()) == "" {
		tabCancel()
		return nil, errors.New("browser tab target id is unavailable")
	}
	tab := newChromedpBrowserTab(tabCtx, cdpContext.Target.TargetID.String(), false)
	tab.cancel = tabCancel
	chromedp.ListenTarget(tabCtx, func(event any) {
		if _, ok := event.(*inspector.EventTargetCrashed); ok {
			tab.fail(errors.New("browser tab target crashed"))
		}
	})
	go tab.observe()
	return tab, nil
}

func (client *chromedpBrowserClient) Minimize(ctx context.Context) error {
	if client == nil {
		return errors.New("browser CDP client is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	opCtx, opCancel := context.WithCancel(client.ctx)
	defer opCancel()
	result := make(chan error, 1)
	go func() {
		result <- chromedp.Run(opCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			cdpContext := chromedp.FromContext(client.ctx)
			if cdpContext == nil || cdpContext.Browser == nil {
				return errors.New("browser CDP executor is unavailable")
			}
			browserExecutor := cdp.WithExecutor(ctx, cdpContext.Browser)
			targets, err := target.GetTargets().Do(browserExecutor)
			if err != nil {
				return err
			}
			var pageTarget target.ID
			for _, info := range targets {
				if info.Type == "page" {
					pageTarget = info.TargetID
					break
				}
			}
			if pageTarget == "" {
				return errors.New("browser has no page target to minimize")
			}
			windowID, _, err := browser.GetWindowForTarget().WithTargetID(pageTarget).Do(browserExecutor)
			if err != nil {
				return err
			}
			return browser.SetWindowBounds(windowID, &browser.Bounds{WindowState: browser.WindowStateMinimized}).Do(browserExecutor)
		}))
	}()
	select {
	case <-ctx.Done():
		opCancel()
		return ctx.Err()
	case err := <-result:
		return err
	}
}

func (client *chromedpBrowserClient) Done() <-chan struct{} { return client.done }

func (client *chromedpBrowserClient) Err() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.err
}

func (client *chromedpBrowserClient) Close(ctx context.Context) error {
	if client == nil {
		return nil
	}
	client.closing.Store(true)
	if ctx == nil {
		ctx = context.Background()
	}
	result := make(chan error, 1)
	go func() {
		cdpContext := chromedp.FromContext(client.ctx)
		var err error
		if cdpContext == nil || cdpContext.Browser == nil {
			err = errors.New("browser CDP executor is unavailable")
		} else {
			err = browser.Close().Do(cdp.WithExecutor(client.ctx, cdpContext.Browser))
		}
		client.cancel()
		client.allocatorCancel()
		result <- err
	}()
	select {
	case <-ctx.Done():
		client.cancel()
		client.allocatorCancel()
		return ctx.Err()
	case err := <-result:
		if err != nil && !errors.Is(err, context.Canceled) && !strings.Contains(strings.ToLower(err.Error()), "websocket") {
			return fmt.Errorf("close browser over CDP: %w", err)
		}
		return nil
	}
}

type chromedpBrowserTab struct {
	ctx    context.Context
	cancel context.CancelFunc
	id     string
	done   chan struct{}

	closed        atomic.Bool
	once          sync.Once
	mu            sync.Mutex
	err           error
	sharedContext bool
}

func newChromedpBrowserTab(ctx context.Context, id string, sharedContext bool) *chromedpBrowserTab {
	tab := &chromedpBrowserTab{ctx: ctx, id: id, done: make(chan struct{}), sharedContext: sharedContext}
	if sharedContext {
		tab.cancel = func() {}
	}
	return tab
}

func (tab *chromedpBrowserTab) ID() string { return tab.id }

func (tab *chromedpBrowserTab) Navigate(ctx context.Context, url string) error {
	return tab.run(ctx, chromedp.Navigate(url))
}

func (tab *chromedpBrowserTab) Evaluate(ctx context.Context, expression string, result any) error {
	return tab.run(ctx, chromedp.Evaluate(expression, result, func(params *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams {
		return params.WithAwaitPromise(true)
	}))
}

func (tab *chromedpBrowserTab) Done() <-chan struct{} { return tab.done }

func (tab *chromedpBrowserTab) Err() error {
	tab.mu.Lock()
	defer tab.mu.Unlock()
	return tab.err
}

func (tab *chromedpBrowserTab) run(ctx context.Context, action chromedp.Action) error {
	if tab == nil {
		return errors.New("browser tab is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-tab.done:
		return errors.New("browser tab is closed")
	default:
	}
	opCtx, cancel := context.WithCancel(tab.ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- chromedp.Run(opCtx, action) }()
	select {
	case <-ctx.Done():
		cancel()
		return ctx.Err()
	case err := <-done:
		return err
	}
}

func (tab *chromedpBrowserTab) fail(err error) {
	tab.mu.Lock()
	if tab.err == nil {
		tab.err = err
	}
	tab.mu.Unlock()
	if tab.sharedContext {
		tab.once.Do(func() { close(tab.done) })
		return
	}
	tab.cancel()
}

func (tab *chromedpBrowserTab) observe() {
	<-tab.ctx.Done()
	if !tab.closed.Load() {
		tab.mu.Lock()
		if tab.err == nil {
			tab.err = tab.ctx.Err()
		}
		tab.mu.Unlock()
	}
	tab.once.Do(func() { close(tab.done) })
}

func (tab *chromedpBrowserTab) Close(ctx context.Context) error {
	if tab == nil {
		return nil
	}
	tab.closed.Store(true)
	if ctx == nil {
		ctx = context.Background()
	}
	if tab.sharedContext {
		cdpContext := chromedp.FromContext(tab.ctx)
		if cdpContext == nil || cdpContext.Browser == nil {
			tab.once.Do(func() { close(tab.done) })
			return errors.New("browser CDP executor is unavailable")
		}
		result := make(chan error, 1)
		go func() {
			result <- target.CloseTarget(target.ID(tab.id)).Do(cdp.WithExecutor(ctx, cdpContext.Browser))
		}()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-result:
			tab.once.Do(func() { close(tab.done) })
			return err
		}
	}
	result := make(chan error, 1)
	go func() {
		result <- chromedp.Cancel(tab.ctx)
		tab.cancel()
	}()
	select {
	case <-ctx.Done():
		tab.cancel()
		return ctx.Err()
	case err := <-result:
		return err
	}
}

func existingPageTargetID(ctx context.Context, websocketURL string) (target.ID, error) {
	endpoint, err := devToolsHTTPEndpoint(websocketURL)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/json/list", nil)
	if err != nil {
		return "", err
	}
	response, err := (&http.Client{}).Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("CDP target endpoint returned %s", response.Status)
	}
	var targets []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		URL  string `json:"url"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 256<<10)).Decode(&targets); err != nil {
		return "", err
	}
	var firstPage target.ID
	for _, item := range targets {
		if item.Type == "page" && strings.TrimSpace(item.ID) != "" {
			id := target.ID(strings.TrimSpace(item.ID))
			if firstPage == "" {
				firstPage = id
			}
			if strings.TrimSpace(item.URL) == "about:blank" {
				return id, nil
			}
		}
	}
	return firstPage, nil
}

func devToolsHTTPEndpoint(websocketURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(websocketURL))
	if err != nil || parsed.Host == "" {
		return "", errors.New("invalid browser websocket endpoint")
	}
	switch parsed.Scheme {
	case "ws":
		parsed.Scheme = "http"
	case "wss":
		parsed.Scheme = "https"
	default:
		return "", errors.New("browser endpoint is not a websocket URL")
	}
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}
