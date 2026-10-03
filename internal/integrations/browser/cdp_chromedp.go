package browser

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/inspector"
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
	browserCtx, browserCancel := chromedp.NewContext(allocatorCtx)
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
	client := &chromedpBrowserClient{
		ctx: browserCtx, cancel: browserCancel,
		allocatorCancel: allocatorCancel, done: make(chan struct{}),
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

	mu  sync.Mutex
	err error
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
	tab := &chromedpBrowserTab{
		ctx: tabCtx, cancel: tabCancel,
		id:   cdpContext.Target.TargetID.String(),
		done: make(chan struct{}),
	}
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
			windowID, _, err := browser.GetWindowForTarget().Do(ctx)
			if err != nil {
				return err
			}
			return browser.SetWindowBounds(windowID, &browser.Bounds{WindowState: browser.WindowStateMinimized}).Do(ctx)
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
		err := chromedp.Run(client.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			return browser.Close().Do(ctx)
		}))
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

	closed atomic.Bool
	once   sync.Once
	mu     sync.Mutex
	err    error
}

func (tab *chromedpBrowserTab) ID() string { return tab.id }

func (tab *chromedpBrowserTab) Done() <-chan struct{} { return tab.done }

func (tab *chromedpBrowserTab) Err() error {
	tab.mu.Lock()
	defer tab.mu.Unlock()
	return tab.err
}

func (tab *chromedpBrowserTab) fail(err error) {
	tab.mu.Lock()
	if tab.err == nil {
		tab.err = err
	}
	tab.mu.Unlock()
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
