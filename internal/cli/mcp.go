package cli

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/app"
	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/auth"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/mcp"
	"go.mewis.me/codemcp/internal/mcpauth"
	"go.mewis.me/codemcp/internal/workspace"
)

var errMCPAuthCredentialMissing = errors.New("MCP authentication is enabled but no credential is configured")

func mcpCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "mcp", Short: "Serve CodeMCP transports"}
	cmd.AddCommand(mcpStdioCommand(), mcpHTTPCommand())
	return cmd
}

func mcpHTTPCommand() *cobra.Command {
	var workspace, host, profileName, clientCAFile string
	var port int
	var noSSE bool
	cmd := &cobra.Command{
		Use:   "http",
		Short: "Serve MCP over Streamable HTTP with legacy SSE fallback",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCPHTTP(cmd, workspace, host, port, !noSSE, profileName, clientCAFile)
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", "", "bind MCP sessions to a registered workspace ID or path")
	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "listen host")
	cmd.Flags().IntVar(&port, "port", 0, "listen port; defaults to configured MCP port")
	cmd.Flags().BoolVar(&noSSE, "no-sse", false, "disable legacy SSE compatibility endpoint")
	cmd.Flags().StringVar(&profileName, "profile", string(mcp.BaseProfileID), "MCP projection profile: base or openai")
	cmd.Flags().StringVar(&clientCAFile, "client-ca", "", "PEM CA bundle for optional OpenAI-profile client certificate authentication")
	return cmd
}

func runMCPHTTP(cmd *cobra.Command, workspace, host string, port int, enableSSE bool, profileName, clientCAFile string) (runErr error) {
	cfg, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	if port == 0 {
		port = cfg.HTTP.MCP.Port
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid MCP HTTP port: %d", port)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return errors.New("MCP HTTP host is required")
	}
	profile, err := mcp.ResolveProfile(profileName)
	if err != nil {
		return err
	}
	clientCAFile = strings.TrimSpace(clientCAFile)
	clientCertificatePolicy := mcp.ClientCertificatePolicy{}
	if clientCAFile != "" {
		if !mcp.RequiresClientCertificateAuthentication(profile) {
			return errors.New("--client-ca is only supported with --profile openai")
		}
		data, readErr := os.ReadFile(clientCAFile)
		if readErr != nil {
			return fmt.Errorf("read MCP client CA bundle: %w", readErr)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(data) {
			return errors.New("MCP client CA bundle contains no valid certificates")
		}
		clientCertificatePolicy.Roots = roots
	}
	cfg.HTTP.MCP.Enabled = false
	cfg.HTTP.Admin.Enabled = false
	cfg.Tunnel.Enabled = false
	runtime, err := app.NewWithLoggerContext(cmd.Context(), cfg, commandLogger(cmd))
	if err != nil {
		return err
	}
	if err := runtime.Start(cmd.Context()); err != nil {
		return err
	}
	defer func() {
		if err := runtime.Stop(); err != nil && runErr == nil {
			runErr = err
		}
	}()
	workspaceID, err := resolveMCPWorkspace(runtime.Tools.Workspaces, workspace)
	if err != nil {
		return err
	}
	authRequirements := []mcp.AuthRequirement(nil)
	if cfg.HTTP.MCP.Auth.Enabled {
		authRequirements = append(authRequirements, mcp.BearerAuthRequirement(mcpauth.ScopeTools))
	}
	handler, err := mcp.NewSDKHTTPHandlerWithProfileAuth(runtime.Tools, workspaceID, enableSSE, profile, authRequirements...)
	if err != nil {
		return err
	}
	if cfg.HTTP.MCP.Auth.Enabled && cfg.HTTP.MCP.Auth.TokenHash == "" {
		return errMCPAuthCredentialMissing
	}
	if !mcpHTTPLoopbackHost(host) {
		return errors.New("standalone MCP HTTP is currently loopback-only; use 127.0.0.1, ::1, or localhost")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprintf("%d", port)))
	if err != nil {
		return err
	}
	baseURL := "http://" + listener.Addr().String()
	authority, err := mcpauth.New(baseURL, baseURL+"/mcp", func() (mcpauth.Config, error) {
		current, loadErr := config.LoadRuntime()
		if loadErr != nil {
			return mcpauth.Config{}, loadErr
		}
		return mcpauth.Config{Enabled: current.HTTP.MCP.Auth.Enabled, LegacyBearer: current.HTTP.MCP.Auth.LegacyBearer, TokenHash: current.HTTP.MCP.Auth.TokenHash}, nil
	}, auth.VerifyToken)
	if err != nil {
		_ = listener.Close()
		return err
	}
	handler = authority.Handler(handler)
	handler = clientCertificatePolicy.Middleware(handler)
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 1 << 20}
	go func() {
		<-cmd.Context().Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	commandLogger(cmd).Ready("MCP", "mcp.http.ready", "MCP HTTP server ready", logger.With("address", listener.Addr().String()), logger.With("sse", enableSSE))
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func mcpHTTPLoopbackHost(host string) bool {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func mcpStdioCommand() *cobra.Command {
	var workspace string
	cmd := &cobra.Command{
		Use:   "stdio",
		Short: "Serve MCP over stdin/stdout for local MCP clients",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCPStdio(cmd, workspace)
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", "", "bind this MCP session to a registered workspace ID or path")
	markMachineOutput(cmd)
	return cmd
}

func runMCPStdio(cmd *cobra.Command, workspace string) (runErr error) {
	if cmd == nil {
		return errors.New("stdio command is unavailable")
	}
	source, err := config.Source()
	if err != nil {
		return err
	}
	if !source.Exists {
		return application.ErrNotInitialized
	}
	cfg, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	cfg.HTTP.MCP.Enabled = false
	cfg.HTTP.Admin.Enabled = false
	cfg.Tunnel.Enabled = false
	runtime, err := app.NewWithLoggerContext(cmd.Context(), cfg, commandLogger(cmd))
	if err != nil {
		return err
	}
	if err := runtime.Start(cmd.Context()); err != nil {
		return err
	}
	defer func() {
		if err := runtime.Stop(); err != nil && runErr == nil {
			runErr = err
		}
	}()
	workspaceID, err := resolveMCPWorkspace(runtime.Tools.Workspaces, workspace)
	if err != nil {
		return err
	}
	stdio, err := mcp.NewStdioRuntimeWithWorkspace(runtime.Tools, readCloser{cmd.InOrStdin()}, writeCloser{cmd.OutOrStdout()}, workspaceID)
	if err != nil {
		return err
	}
	err = stdio.Run(cmd.Context())
	if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func resolveMCPWorkspace(manager interface {
	Get(string) (workspace.Workspace, error)
	List() ([]workspace.Workspace, error)
}, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, "ws_") {
		item, err := manager.Get(value)
		if err != nil {
			return "", fmt.Errorf("resolve MCP workspace %q: %w", value, err)
		}
		return item.ID, nil
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve MCP workspace path: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve MCP workspace path %q: %w", value, err)
	}
	canonical = filepath.Clean(canonical)
	items, err := manager.List()
	if err != nil {
		return "", err
	}
	for _, item := range items {
		if filepath.Clean(item.Path) == canonical {
			return item.ID, nil
		}
	}
	return "", fmt.Errorf("workspace path is not registered: %s", canonical)
}

type readCloser struct{ io.Reader }

func (readCloser) Close() error { return nil }

type writeCloser struct{ io.Writer }

func (writeCloser) Close() error { return nil }
