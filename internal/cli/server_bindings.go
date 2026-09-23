package cli

import (
	"context"
	"errors"
	"net"
	"net/http"
	"slices"

	"go.mewis.me/codemcp/internal/app"
	"go.mewis.me/codemcp/internal/config"
)

type httpBindings struct {
	cfg            config.Config
	plan           listenerPlan
	mcpListeners   []net.Listener
	adminListeners []net.Listener
	servers        []*http.Server
}

func openHTTPBindings(cfg config.Config, plan listenerPlan) (*httpBindings, error) {
	return openHTTPBindingsContext(context.Background(), cfg, plan)
}

func openHTTPBindingsContext(ctx context.Context, cfg config.Config, plan listenerPlan) (*httpBindings, error) {
	return openHTTPBindingsModeContext(ctx, cfg, plan, false)
}

func openHTTPBindingsExactContext(ctx context.Context, cfg config.Config, plan listenerPlan) (*httpBindings, error) {
	return openHTTPBindingsModeContext(ctx, cfg, plan, true)
}

func openHTTPBindingsModeContext(ctx context.Context, cfg config.Config, plan listenerPlan, exact bool) (*httpBindings, error) {
	bindings := &httpBindings{cfg: cfg, plan: plan}
	listen := listenOnHostsWithFallbackContext
	if exact {
		listen = listenOnHostsExactContext
	}
	var err error
	if cfg.Server.Enabled {
		bindings.mcpListeners, bindings.cfg.Server.Port, err = listen(ctx, "mcp", plan.Hosts, cfg.Server.Port)
		if err != nil {
			return nil, err
		}
	}
	if cfg.Admin.Enabled {
		bindings.adminListeners, bindings.cfg.Admin.Port, err = listen(ctx, "admin", plan.Hosts, cfg.Admin.Port)
		if err != nil {
			closeListeners(bindings.mcpListeners)
			return nil, err
		}
	}
	return bindings, nil
}

func (b *httpBindings) Start(runtime *app.App, errCh chan<- error) {
	if b == nil {
		return
	}
	b.servers = make([]*http.Server, 0, len(b.mcpListeners)+len(b.adminListeners))
	for _, listener := range b.mcpListeners {
		server := newHTTPServer(runtime.MCPHandler())
		b.servers = append(b.servers, server)
		go serveHTTP(server, listener, errCh)
	}
	for _, listener := range b.adminListeners {
		server := newHTTPServer(runtime.AdminHandler())
		b.servers = append(b.servers, server)
		go serveHTTP(server, listener, errCh)
	}
}

func (b *httpBindings) Shutdown() error {
	if b == nil {
		return nil
	}
	err := shutdownServers(b.servers)
	closeListeners(b.mcpListeners)
	closeListeners(b.adminListeners)
	b.servers = nil
	b.mcpListeners = nil
	b.adminListeners = nil
	return err
}

func (b *httpBindings) CloseUnstarted() {
	if b == nil {
		return
	}
	closeListeners(b.mcpListeners)
	closeListeners(b.adminListeners)
	b.mcpListeners = nil
	b.adminListeners = nil
}

func networkConfigEqual(left, right config.Config) bool {
	return left.Server.Enabled == right.Server.Enabled && left.Server.Port == right.Server.Port && config.ExposureEqual(left.Server.Expose, right.Server.Expose) && left.Admin == right.Admin
}

func listenerPlanEqual(left, right listenerPlan) bool { return slices.Equal(left.Hosts, right.Hosts) }

func listenerPortsDisjoint(left config.Config, right config.Config) bool {
	leftPorts := map[int]struct{}{}
	if left.Server.Enabled {
		leftPorts[left.Server.Port] = struct{}{}
	}
	if left.Admin.Enabled {
		leftPorts[left.Admin.Port] = struct{}{}
	}
	if right.Server.Enabled {
		if _, exists := leftPorts[right.Server.Port]; exists {
			return false
		}
	}
	if right.Admin.Enabled {
		if _, exists := leftPorts[right.Admin.Port]; exists {
			return false
		}
	}
	return true
}

func restoreHTTPBindingsContext(ctx context.Context, runtime *app.App, cfg config.Config, plan listenerPlan, errCh chan<- error) (*httpBindings, error) {
	bindings, err := openHTTPBindingsExactContext(ctx, cfg, plan)
	if err != nil {
		return nil, err
	}
	bindings.Start(runtime, errCh)
	return bindings, nil
}

func serveHTTP(server *http.Server, listener net.Listener, errCh chan<- error) {
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		errCh <- err
	}
}
