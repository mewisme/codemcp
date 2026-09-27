package page

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
)

func TestIntegrationsReadViewMutationUsesCanonicalSettingEffect(t *testing.T) {
	previous := configformat.RootPath()
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	page, err := NewIntegrationsReadView(t.Context(), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	updated, cmd := page.Update(IntegrationCommandMsg{Command: IntegrationRTKEnable})
	page = updated.(*ReadViewPage)
	if cmd == nil || !page.loading || page.progress == nil {
		t.Fatalf("RTK enable did not start canonical operation: cmd=%v loading=%t progress=%v", cmd != nil, page.loading, page.progress != nil)
	}
	updated, _ = page.Update(cmd())
	page = updated.(*ReadViewPage)
	if page.err != nil || page.loading {
		t.Fatalf("RTK enable completion err=%v loading=%t", page.err, page.loading)
	}
	cfg, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Integrations.RTK.Enabled {
		t.Fatal("TUI integration mutation did not apply the canonical RTK setting effect")
	}
	if page.notice == "" {
		t.Fatal("successful integration mutation did not expose completion state")
	}
}

func TestIntegrationsReadViewCloseCancelsRunningOperation(t *testing.T) {
	page := newReadViewPage(t.Context(), "Integrations", func(context.Context) (any, error) { return nil, nil })
	started := make(chan struct{}, 1)
	page.integrationRun = func(ctx context.Context, operation capability.ID, workspaceID string) (any, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	updated, cmd := page.Update(IntegrationCommandMsg{Command: IntegrationRTKEnable})
	page = updated.(*ReadViewPage)
	if cmd == nil {
		t.Fatal("integration operation command missing")
	}
	result := make(chan any, 1)
	go func() { result <- cmd() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("integration operation did not start")
	}
	page.Close()
	select {
	case value := <-result:
		message, ok := value.(integrationOperationMsg)
		if !ok || !errors.Is(message.err, context.Canceled) {
			t.Fatalf("cancelled operation result=%#v", value)
		}
	case <-time.After(time.Second):
		t.Fatal("integration operation remained blocked after page disposal")
	}
	if page.operationCancel != nil {
		t.Fatal("page retained operation cancel function after disposal")
	}
}
