package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.mewis.me/codemcp/internal/auth"
	"go.mewis.me/codemcp/internal/config"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type AuthStatus struct {
	MCPEnabled              bool
	MCPConfigured           bool
	MCPLegacyBearer         bool
	AdminEnabled            bool
	AdminConfigured         bool
	UnauthenticatedLoopback bool
	CleartextHTTP           bool
}

func GetAuthStatus() (AuthStatus, error) {
	return GetAuthStatusContext(context.Background())
}

func GetAuthStatusContext(ctx context.Context) (AuthStatus, error) {
	span := tracepkg.Start(ctx, "AUTH", "auth.status", "Loading authentication status")
	cfg, _, err := loadConfigTraced(ctx, "auth.status.config.load", "Loading configuration for authentication status")
	if err != nil {
		span.FailMessage("Authentication status load failed", err)
		return AuthStatus{}, err
	}
	status := authStatus(cfg)
	span.EndMessage("Authentication status loaded", tracepkg.Bool("mcp_enabled", status.MCPEnabled), tracepkg.Bool("mcp_configured", status.MCPConfigured), tracepkg.Bool("admin_enabled", status.AdminEnabled), tracepkg.Bool("admin_configured", status.AdminConfigured), tracepkg.Bool("unauthenticated_loopback", status.UnauthenticatedLoopback), tracepkg.Bool("cleartext_http", status.CleartextHTTP))
	return status, nil
}

func RotateAuthToken(ctx context.Context, kind string) (string, AuthStatus, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	span := tracepkg.Start(ctx, "AUTH", "auth.token.rotate", "Rotating authentication token", tracepkg.String("requested_kind", kind))
	normalizeSpan := tracepkg.Start(ctx, "AUTH", "auth.kind.normalize", "Normalizing authentication kind", tracepkg.String("input", kind))
	kind, err := normalizeAuthKind(kind)
	if err != nil {
		normalizeSpan.FailMessage("Authentication kind normalization failed", err)
		span.FailMessage("Authentication token rotation failed", err)
		return "", AuthStatus{}, err
	}
	normalizeSpan.EndMessage("Authentication kind normalized", tracepkg.String("kind", kind))
	previous, _, err := loadConfigTraced(ctx, "auth.config.load", "Loading configuration for authentication")
	if err != nil {
		span.FailMessage("Authentication token rotation failed", err)
		return "", AuthStatus{}, err
	}
	cfg := previous
	generateSpan := tracepkg.Start(ctx, "AUTH", "auth.token.generate", "Generating authentication token", tracepkg.String("kind", kind))
	token := auth.GenerateToken(kind)
	generateSpan.EndMessage("Authentication token generated", tracepkg.String("kind", kind), tracepkg.Bool("generated", true))
	hashSpan := tracepkg.Start(ctx, "AUTH", "auth.token.hash", "Hashing authentication token", tracepkg.String("kind", kind))
	hash := auth.HashToken(token)
	hashSpan.EndMessage("Authentication token hashed", tracepkg.String("kind", kind))
	if kind == "mcp" {
		cfg.Auth.MCPTokenHash = hash
		cfg.Auth.MCPEnabled = true
	} else {
		cfg.Auth.AdminTokenHash = hash
		cfg.Auth.AdminEnabled = true
		cfg.Admin.Enabled = true
	}
	validateSpan := tracepkg.Start(ctx, "AUTH", "auth.config.validate", "Validating authentication configuration", tracepkg.String("kind", kind))
	if err := config.Validate(cfg); err != nil {
		validateSpan.FailMessage("Authentication configuration validation failed", err)
		span.FailMessage("Authentication token rotation failed", err)
		return "", AuthStatus{}, err
	}
	validateSpan.EndMessage("Authentication configuration validated", tracepkg.String("kind", kind))
	rollbackSecret, err := replaceAuthSecrets(config.RootPath(), map[string]string{kind: token})
	if err != nil {
		span.FailMessage("Authentication token rotation failed", err)
		return "", AuthStatus{}, err
	}
	if _, _, err := saveConfigMutation(ctx, previous, cfg); err != nil {
		err = errors.Join(err, rollbackSecret())
		span.FailMessage("Authentication token rotation failed", err)
		return "", AuthStatus{}, err
	}
	status := authStatus(cfg)
	span.EndMessage("Authentication token rotated", tracepkg.String("kind", kind), tracepkg.Bool("enabled", authEnabled(status, kind)), tracepkg.Bool("configured", authConfigured(status, kind)))
	return token, status, nil
}

func SetAuthEnabled(ctx context.Context, kind string, enabled bool) (AuthStatus, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	span := tracepkg.Start(ctx, "AUTH", "auth.state.set", "Setting authentication state", tracepkg.String("requested_kind", kind), tracepkg.Bool("enabled", enabled))
	normalizeSpan := tracepkg.Start(ctx, "AUTH", "auth.kind.normalize", "Normalizing authentication kind", tracepkg.String("input", kind))
	kind, err := normalizeAuthKind(kind)
	if err != nil {
		normalizeSpan.FailMessage("Authentication kind normalization failed", err)
		span.FailMessage("Authentication state update failed", err)
		return AuthStatus{}, err
	}
	normalizeSpan.EndMessage("Authentication kind normalized", tracepkg.String("kind", kind))
	key := "auth.mcp_enabled"
	if kind == "admin" {
		key = "auth.admin_enabled"
	}
	applied, err := NewSettingService().Apply(ctx, []SettingChange{{Key: key, Value: fmt.Sprint(enabled)}})
	if err != nil {
		span.FailMessage("Authentication state update failed", err)
		return AuthStatus{}, err
	}
	status := authStatus(applied.Config)
	span.EndMessage("Authentication state updated", tracepkg.String("kind", kind), tracepkg.Bool("enabled", authEnabled(status, kind)), tracepkg.Bool("configured", authConfigured(status, kind)), tracepkg.Bool("runtime_reloaded", applied.RuntimeReloaded))
	return status, nil
}

func normalizeAuthKind(kind string) (string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != "mcp" && kind != "admin" {
		return "", fmt.Errorf("unsupported auth kind: %s", kind)
	}
	return kind, nil
}

func authStatus(cfg config.Config) AuthStatus {
	return AuthStatus{
		MCPEnabled: cfg.Auth.MCPEnabled, MCPConfigured: cfg.Auth.MCPTokenHash != "", MCPLegacyBearer: cfg.Auth.MCPLegacyBearer,
		AdminEnabled: cfg.Auth.AdminEnabled, AdminConfigured: cfg.Auth.AdminTokenHash != "",
		UnauthenticatedLoopback: config.UnauthenticatedLoopbackActive(cfg),
		CleartextHTTP:           config.CleartextHTTPActive(cfg),
	}
}

func authEnabled(status AuthStatus, kind string) bool {
	if kind == "admin" {
		return status.AdminEnabled
	}
	return status.MCPEnabled
}

func authConfigured(status AuthStatus, kind string) bool {
	if kind == "admin" {
		return status.AdminConfigured
	}
	return status.MCPConfigured
}
