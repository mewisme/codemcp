package oauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type callbackResult struct {
	Credential Credential
	Err        error
}

func (s *Store) Login(ctx context.Context, config LoginConfig, options LoginOptions) (Credential, error) {
	ctx = s.traceContext(ctx)
	span := tracepkg.Start(ctx, "OAUTH", "oauth.login", "Starting OAuth login", tracepkg.String("server", config.ServerID), tracepkg.URL("server_url", config.ServerURL), tracepkg.Bool("url_handler", options.OnURL != nil))
	listenSpan := tracepkg.Start(ctx, "OAUTH", "oauth.callback.listen", "Opening OAuth callback listener", tracepkg.String("bind_host", "127.0.0.1"))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		listenSpan.FailMessage("OAuth callback listener failed", err)
		span.FailMessage("OAuth login failed to open callback listener", err)
		return Credential{}, fmt.Errorf("open OAuth callback listener: %w", err)
	}
	defer listener.Close()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	listenSpan.EndMessage("OAuth callback listener opened", tracepkg.String("bind_host", host), tracepkg.String("bind_port", port), tracepkg.String("bind_address", listener.Addr().String()))

	flows := NewFlowManager(s)
	session, err := flows.Begin(ctx, config, "http://"+listener.Addr().String()+"/callback", options.ExtraScope)
	if err != nil {
		span.FailMessage("OAuth login flow preparation failed", errors.New("OAuth login flow preparation failed"))
		return Credential{}, err
	}
	defer flows.Cancel(session.ID)

	resultCh := make(chan callbackResult, 1)
	serverErrCh := make(chan error, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback/", func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		tracepkg.Emit(ctx, "OAUTH", "oauth.callback.request", "OAuth callback request accepted", tracepkg.Bool("state_present", query.Get("state") != ""), tracepkg.Bool("code_present", query.Get("code") != ""), tracepkg.Bool("issuer_present", query.Get("iss") != ""), tracepkg.Bool("oauth_error_present", query.Get("error") != ""))
		credential, err := flows.Complete(request.Context(), strings.TrimPrefix(request.URL.Path, "/callback/"), query.Get("state"), query.Get("code"), query.Get("iss"), query.Get("error"), query.Get("error_description"))
		select {
		case resultCh <- callbackResult{Credential: credential, Err: err}:
		default:
		}
		if err != nil {
			http.Error(writer, "OAuth authorization failed: "+err.Error(), http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = writer.Write([]byte("Authorization received. You can close this window."))
	})
	callbackServer := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	serverSpan := tracepkg.Start(ctx, "OAUTH", "oauth.callback.server", "Serving OAuth callback requests", tracepkg.String("bind_address", listener.Addr().String()))
	go func() {
		if err := callbackServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverSpan.FailMessage("OAuth callback server failed", err)
			serverErrCh <- err
		}
	}()
	defer func() {
		shutdownSpan := tracepkg.Start(ctx, "OAUTH", "oauth.callback.shutdown", "Shutting down OAuth callback server", tracepkg.String("bind_address", listener.Addr().String()))
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := callbackServer.Shutdown(shutdownCtx)
		cancel()
		if err != nil {
			shutdownSpan.FailMessage("OAuth callback server shutdown failed", err)
			serverSpan.FailMessage("OAuth callback server shutdown failed", err)
		} else {
			shutdownSpan.EndMessage("OAuth callback server stopped")
			serverSpan.EndMessage("OAuth callback server stopped")
		}
	}()

	if options.OnURL != nil {
		dispatchSpan := tracepkg.Start(ctx, "OAUTH", "oauth.authorization.dispatch", "Dispatching OAuth authorization URL", tracepkg.URL("authorization_endpoint", authorizationEndpoint(session.AuthorizationURL)))
		if err := options.OnURL(session.AuthorizationURL); err != nil {
			dispatchSpan.FailMessage("OAuth authorization URL dispatch failed", errors.New("OAuth authorization URL handler failed"))
			span.FailMessage("OAuth authorization URL dispatch failed", errors.New("OAuth authorization URL handler failed"))
			return Credential{}, err
		}
		dispatchSpan.EndMessage("OAuth authorization URL dispatched")
	}
	select {
	case <-ctx.Done():
		span.FailMessage("OAuth login canceled", ctx.Err())
		return Credential{}, ctx.Err()
	case err := <-serverErrCh:
		span.FailMessage("OAuth callback server failed", err)
		return Credential{}, fmt.Errorf("OAuth callback server: %w", err)
	case result := <-resultCh:
		if result.Err != nil {
			span.FailMessage("OAuth login callback failed", errors.New("OAuth callback processing failed"))
			return result.Credential, result.Err
		}
		span.EndMessage("OAuth login completed", tracepkg.String("server", result.Credential.ServerID), tracepkg.URL("issuer", result.Credential.Issuer), tracepkg.String("registration", result.Credential.Registration), tracepkg.Any("scopes", append([]string(nil), result.Credential.Scopes...)), tracepkg.Bool("has_refresh", result.Credential.RefreshToken != ""), tracepkg.Bool("expires", !result.Credential.ExpiresAt.IsZero()))
		return result.Credential, nil
	}
}

func authorizationEndpoint(raw string) string {
	value, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	value.RawQuery = ""
	value.Fragment = ""
	value.User = nil
	return value.String()
}
