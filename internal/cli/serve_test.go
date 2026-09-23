package cli

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
)

func TestWaitRuntimeHTTPReadyRequiresMCPAndAdminListeners(t *testing.T) {
	mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer mcp.Close()
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer admin.Close()
	cfg := config.Default()
	cfg.Server.Port = testServerPort(t, mcp.Listener.Addr())
	cfg.Admin.Enabled = true
	cfg.Admin.Port = testServerPort(t, admin.Listener.Addr())
	if err := waitRuntimeHTTPReady(context.Background(), cfg, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestWaitRuntimeHTTPReadyRejectsMissingListener(t *testing.T) {
	mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer mcp.Close()
	missing, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	missingPort := testServerPort(t, missing.Addr())
	_ = missing.Close()
	cfg := config.Default()
	cfg.Server.Port = testServerPort(t, mcp.Listener.Addr())
	cfg.Admin.Enabled = true
	cfg.Admin.Port = missingPort
	err = waitRuntimeHTTPReady(context.Background(), cfg, 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "server listeners did not become ready") {
		t.Fatalf("error = %v", err)
	}
}

func TestTunnelOnlyRuntimeRequiresNoHTTPListeners(t *testing.T) {
	cfg := config.Default()
	cfg.Server.Enabled = false
	cfg.Admin.Enabled = false
	bindings, err := openHTTPBindings(cfg, listenerPlan{Hosts: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer bindings.CloseUnstarted()
	if len(bindings.mcpListeners) != 0 || len(bindings.adminListeners) != 0 {
		t.Fatalf("tunnel-only listeners mcp=%d admin=%d", len(bindings.mcpListeners), len(bindings.adminListeners))
	}
	if err := waitRuntimeHTTPReady(context.Background(), cfg, 10*time.Millisecond); err != nil {
		t.Fatalf("tunnel-only HTTP readiness = %v", err)
	}
}

func TestOpenHTTPBindingsFallsBackWhenAdminPortIsBusy(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	cfg := config.Default()
	cfg.Server.Enabled = false
	cfg.Admin.Enabled = true
	cfg.Admin.Port = testServerPort(t, occupied.Addr())
	bindings, err := openHTTPBindings(cfg, listenerPlan{Hosts: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer bindings.CloseUnstarted()
	if bindings.cfg.Admin.Port == cfg.Admin.Port {
		t.Fatalf("admin port did not fall back from occupied port %d", cfg.Admin.Port)
	}
}

func TestTunnelOnlyServePublishesRuntimeControl(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Server.Enabled = false
	cfg.Admin.Enabled = false
	cfg.Auth.MCPEnabled, cfg.Auth.AdminEnabled = false, false
	cfg.Server.AllowUnauthenticatedLoopback = true
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/poll") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"commands":[]}`)
	}))
	defer controlPlane.Close()
	cfg.Tunnel.Enabled = true
	cfg.Tunnel.ID = "tunnel_00000000000000000000000000000000"
	cfg.Tunnel.APIKey = "runtime-test"
	cfg.Tunnel.ControlPlaneBaseURL = controlPlane.URL
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := &cobra.Command{Use: "test"}
	cmd.SetContext(ctx)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	done := make(chan error, 1)
	go func() { done <- runServer(cmd, nil) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, err := requestRuntimeStatus(context.Background())
		if err == nil {
			if status.ServerEnabled || !status.TunnelEnabled {
				t.Fatalf("tunnel-only status=%#v", status)
			}
			if status.Starting || status.Lifecycle != "ready" {
				time.Sleep(25 * time.Millisecond)
				continue
			}
			if err := requestRuntimeShutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("tunnel-only serve did not stop")
			}
			return
		}
		if !runtimecontrol.IsUnavailable(err) {
			t.Fatal(err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("tunnel-only runtime control was not published")
}

func testServerPort(t *testing.T, address net.Addr) int {
	t.Helper()
	_, portText, err := net.SplitHostPort(address.String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return port
}
