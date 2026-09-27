package application

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"go.mewis.me/codemcp/internal/config"
	typesafeintegration "go.mewis.me/codemcp/internal/integrations/typesafe"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/tunnel"
	"go.mewis.me/codemcp/internal/upstream"
)

type SettingResult struct {
	Spec            config.FieldSpec
	Value           string
	Configured      *bool
	RuntimeReloaded bool
}

type SettingDiff struct {
	SettingResult
	Baseline string
}

type SettingWhy struct {
	Spec        config.FieldSpec
	Baseline    string
	HasBaseline bool
}

type SettingSetOptions struct {
	TunnelAdminScope *tunnel.AdminScope
	SecretSource     string
}

type SettingService struct {
	upstream *UpstreamService
}

func NewSettingService() *SettingService { return &SettingService{} }

func (s *SettingService) List(ctx context.Context, prefix string) ([]SettingResult, error) {
	ctx = settingContext(ctx)
	prefix = strings.TrimSpace(prefix)
	if _, ok := config.MatchSettingSelector(prefix); ok {
		value, err := s.Present(ctx, prefix)
		if err != nil {
			return nil, err
		}
		return []SettingResult{value}, nil
	}
	specs := presentationSettingSpecs(prefix)
	results := make([]SettingResult, 0, len(specs))
	for _, spec := range specs {
		if spec.Selector != nil {
			continue
		}
		result, err := s.Present(ctx, spec.Key)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *SettingService) Present(ctx context.Context, key string) (SettingResult, error) {
	ctx = settingContext(ctx)
	spec, selector, err := resolveSetting(key)
	if err != nil {
		return SettingResult{}, err
	}
	if !spec.Secret {
		return s.Read(ctx, key)
	}
	if selector != nil {
		if _, _, err := s.resolveAndCheckDynamic(ctx, key, *selector); err != nil {
			return SettingResult{}, err
		}
	}
	result, err := s.presentSecret(ctx, spec, selector)
	if err != nil {
		return SettingResult{}, err
	}
	result.Spec = presentationSpec(spec, selector)
	return result, nil
}

func (s *SettingService) Diff(ctx context.Context, prefix string) ([]SettingDiff, error) {
	ctx = settingContext(ctx)
	results, err := s.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	defaults := config.Default()
	diffs := make([]SettingDiff, 0)
	for _, result := range results {
		baseline, ok, err := settingBaseline(defaults, result.Spec)
		if err != nil {
			return nil, err
		}
		if !ok || result.Value == baseline {
			continue
		}
		diffs = append(diffs, SettingDiff{SettingResult: result, Baseline: baseline})
	}
	return diffs, nil
}

func (s *SettingService) Why(ctx context.Context, key string) (result SettingWhy, resultErr error) {
	ctx = settingContext(ctx)
	span := tracepkg.Start(ctx, "CONFIG", "setting.why", "Explaining canonical setting", tracepkg.String("key", key))
	defer func() {
		if resultErr != nil {
			span.FailMessage("Canonical setting explanation failed", resultErr, tracepkg.String("key", key))
			return
		}
		span.EndMessage("Canonical setting explained", tracepkg.String("key", result.Spec.Key), tracepkg.String("owner", result.Spec.ApplicationOwner), tracepkg.Bool("has_baseline", result.HasBaseline))
	}()
	spec, selector, err := resolveSetting(key)
	if err != nil {
		return SettingWhy{}, err
	}
	if selector != nil {
		if _, _, err := s.resolveAndCheckDynamic(ctx, key, *selector); err != nil {
			return SettingWhy{}, err
		}
		spec = presentationSpec(spec, selector)
	}
	baseline, ok, err := settingBaseline(config.Default(), spec)
	if err != nil {
		return SettingWhy{}, err
	}
	return SettingWhy{Spec: spec, Baseline: baseline, HasBaseline: ok}, nil
}

func (s *SettingService) Read(ctx context.Context, key string) (result SettingResult, resultErr error) {
	ctx = settingContext(ctx)
	span := tracepkg.Start(ctx, "CONFIG", "setting.read", "Reading canonical setting", tracepkg.String("key", key))
	defer func() {
		if resultErr != nil {
			span.FailMessage("Canonical setting read failed", resultErr, tracepkg.String("key", key))
			return
		}
		span.EndMessage("Canonical setting read", tracepkg.String("key", result.Spec.Key), tracepkg.Bool("configured", result.Configured != nil && *result.Configured))
	}()
	spec, selector, err := resolveSetting(key)
	if err != nil {
		return SettingResult{}, err
	}
	if !spec.Readable {
		if spec.ConfiguredStateKey != "" {
			return SettingResult{}, fmt.Errorf("setting %q is write-only; use its presentation-safe configured state or credential operation", key)
		}
		return SettingResult{}, fmt.Errorf("setting %q is write-only", key)
	}
	result = SettingResult{Spec: presentationSpec(spec, selector)}
	if selector != nil {
		result.Value, result.Configured, resultErr = s.readDynamicSetting(ctx, *selector)
		return result, resultErr
	}
	if configured, ok, err := readConfiguredSetting(ctx, spec.Key); ok {
		if err != nil {
			return SettingResult{}, err
		}
		result.Value = strconv.FormatBool(configured)
		result.Configured = boolPointer(configured)
		return result, nil
	}
	cfg, err := LoadConfig(ctx)
	if err != nil {
		return SettingResult{}, err
	}
	value, err := config.RawValue(cfg, spec.ReadKey)
	if err != nil {
		return SettingResult{}, err
	}
	result.Value = value
	return result, nil
}

func (s *SettingService) Set(ctx context.Context, key, raw string) (SettingResult, error) {
	return s.SetWithOptions(ctx, key, raw, SettingSetOptions{})
}

func (s *SettingService) SetWithOptions(ctx context.Context, key, raw string, options SettingSetOptions) (result SettingResult, resultErr error) {
	ctx = settingContext(ctx)
	fields := []tracepkg.Field{tracepkg.String("key", key)}
	if source := strings.TrimSpace(options.SecretSource); source != "" {
		fields = append(fields, tracepkg.String("secret_source", source))
	}
	span := tracepkg.Start(ctx, "CONFIG", "setting.set", "Setting canonical setting", fields...)
	defer settingMutationFinish(span, "set", key, &result, &resultErr)()

	spec, selector, err := resolveSetting(key)
	if err != nil {
		return SettingResult{}, err
	}
	changes := []SettingChange{{Key: key, Value: raw}}
	if selector == nil && spec.Key == "tunnel.admin.key" && options.TunnelAdminScope != nil {
		scopeChange, err := settingChangeForAdminScope(*options.TunnelAdminScope)
		if err != nil {
			return SettingResult{}, err
		}
		changes = append(changes, scopeChange)
	}
	applied, err := s.Apply(ctx, changes)
	if err != nil {
		return SettingResult{}, err
	}
	if selector != nil && len(applied.Results) == 1 {
		return applied.Results[0], nil
	}
	return settingApplyResultForKey(applied, spec.Key)
}

func (s *SettingService) Unset(ctx context.Context, key string) (result SettingResult, resultErr error) {
	ctx = settingContext(ctx)
	span := tracepkg.Start(ctx, "CONFIG", "setting.unset", "Clearing canonical setting", tracepkg.String("key", key))
	defer settingMutationFinish(span, "unset", key, &result, &resultErr)()

	spec, selector, err := resolveSetting(key)
	if err != nil {
		return SettingResult{}, err
	}
	applied, err := s.Apply(ctx, []SettingChange{{Key: key, Unset: true}})
	if err != nil {
		return SettingResult{}, err
	}
	if selector != nil && len(applied.Results) == 1 {
		return applied.Results[0], nil
	}
	return settingApplyResultForKey(applied, spec.Key)
}

func (s *SettingService) Rotate(ctx context.Context, key string) (result SettingResult, resultErr error) {
	ctx = settingContext(ctx)
	span := tracepkg.Start(ctx, "CONFIG", "setting.rotate", "Rotating canonical setting credential", tracepkg.String("key", key))
	defer settingMutationFinish(span, "rotate", key, &result, &resultErr)()

	spec, selector, err := resolveSetting(key)
	if err != nil {
		return SettingResult{}, err
	}
	if selector != nil || !spec.Rotatable {
		return SettingResult{}, fmt.Errorf("setting %q is not rotatable", key)
	}
	kind := ""
	switch spec.Key {
	case "auth.mcp_token":
		kind = "mcp"
	case "auth.admin_token":
		kind = "admin"
	default:
		return SettingResult{}, fmt.Errorf("setting %q is not rotatable", key)
	}
	token, _, err := RotateAuthToken(ctx, kind)
	if err != nil {
		return SettingResult{}, err
	}
	return SettingResult{Spec: spec, Value: token, Configured: boolPointer(true)}, nil
}

func (s *SettingService) Reveal(ctx context.Context, key string) (result SettingResult, resultErr error) {
	ctx = settingContext(ctx)
	span := tracepkg.Start(ctx, "CONFIG", "setting.reveal", "Revealing canonical setting credential", tracepkg.String("key", key))
	defer settingMutationFinish(span, "reveal", key, &result, &resultErr)()

	spec, selector, err := resolveSetting(key)
	if err != nil {
		return SettingResult{}, err
	}
	if selector != nil || !spec.Revealable {
		return SettingResult{}, fmt.Errorf("setting %q is not revealable", key)
	}
	cfg, err := LoadConfig(ctx)
	if err != nil {
		return SettingResult{}, err
	}
	switch spec.Key {
	case "tunnel.api_key":
		if strings.TrimSpace(cfg.Tunnel.APIKey) == "" {
			return SettingResult{}, fmt.Errorf("setting %q is not configured", key)
		}
		return SettingResult{Spec: spec, Value: cfg.Tunnel.APIKey, Configured: boolPointer(true)}, nil
	case "tunnel.admin.key":
		if strings.TrimSpace(cfg.Tunnel.Admin.Key) == "" {
			return SettingResult{}, fmt.Errorf("setting %q is not configured", key)
		}
		return SettingResult{Spec: spec, Value: cfg.Tunnel.Admin.Key, Configured: boolPointer(true)}, nil
	default:
		return SettingResult{}, fmt.Errorf("setting %q is not revealable", key)
	}
}

func (s *SettingService) Verify(ctx context.Context, key string) (result SettingResult, resultErr error) {
	ctx = settingContext(ctx)
	span := tracepkg.Start(ctx, "CONFIG", "setting.verify", "Verifying canonical setting credential", tracepkg.String("key", key))
	defer settingMutationFinish(span, "verify", key, &result, &resultErr)()

	spec, selector, err := resolveSetting(key)
	if err != nil {
		return SettingResult{}, err
	}
	if selector != nil || !spec.Verifiable || spec.Key != "tunnel.admin.key" {
		return SettingResult{}, fmt.Errorf("setting %q is not verifiable", key)
	}
	if _, _, err := VerifyTunnelAdminKey(ctx); err != nil {
		return SettingResult{}, err
	}
	return s.Present(ctx, spec.Key)
}

func (s *SettingService) presentSecret(ctx context.Context, spec config.FieldSpec, selector *config.FieldSelectorMatch) (SettingResult, error) {
	if selector != nil {
		return SettingResult{}, fmt.Errorf("dynamic secret presentation is not supported for %q", spec.Key)
	}
	configured := false
	if spec.ConfiguredStateKey != "" {
		value, ok, err := readConfiguredSetting(ctx, spec.ConfiguredStateKey)
		if err != nil {
			return SettingResult{}, err
		}
		configured = ok && value
	}
	if !configured {
		return SettingResult{Value: "not configured", Configured: boolPointer(false)}, nil
	}
	switch spec.Presentation {
	case config.SettingPresentationConfiguredState:
		return SettingResult{Value: "configured", Configured: boolPointer(true)}, nil
	case config.SettingPresentationMaskedPreview:
		cfg, err := LoadConfig(ctx)
		if err != nil {
			return SettingResult{}, err
		}
		raw := ""
		switch spec.Key {
		case "tunnel.api_key":
			raw = cfg.Tunnel.APIKey
		case "tunnel.admin.key":
			raw = cfg.Tunnel.Admin.Key
		default:
			return SettingResult{Value: "********", Configured: boolPointer(true)}, nil
		}
		return SettingResult{Value: tracepkg.MaskSecret(raw, true), Configured: boolPointer(true)}, nil
	default:
		return SettingResult{Value: "********", Configured: boolPointer(true)}, nil
	}
}

func resolveSetting(key string) (config.FieldSpec, *config.FieldSelectorMatch, error) {
	key = strings.TrimSpace(key)
	if spec, ok := config.SettingByKey(key); ok && spec.Selector == nil {
		if spec.InternalOnly {
			return config.FieldSpec{}, nil, fmt.Errorf("unsupported setting: %s", key)
		}
		return spec, nil, nil
	}
	match, ok := config.MatchSettingSelector(key)
	if !ok {
		return config.FieldSpec{}, nil, fmt.Errorf("unsupported setting: %s", key)
	}
	return match.Spec, &match, nil
}

func (s *SettingService) resolveAndCheckDynamic(ctx context.Context, key string, match config.FieldSelectorMatch) (config.FieldSpec, *config.FieldSelectorMatch, error) {
	if match.Spec.Selector == nil {
		return config.FieldSpec{}, nil, fmt.Errorf("unsupported setting: %s", key)
	}
	switch match.Spec.Selector.Resource {
	case "upstream.server":
		service, err := s.upstreamService(ctx)
		if err != nil {
			return config.FieldSpec{}, nil, err
		}
		if _, err := service.Get(ctx, match.ResourceID); err != nil {
			return config.FieldSpec{}, nil, err
		}
	case "tunnel.managed":
		if _, err := GetManagedTunnel(ctx, match.ResourceID, ManagedTunnelOptions{}); err != nil {
			return config.FieldSpec{}, nil, err
		}
	default:
		return config.FieldSpec{}, nil, fmt.Errorf("unsupported setting resource: %s", match.Spec.Selector.Resource)
	}
	return match.Spec, &match, nil
}

func readConfiguredSetting(ctx context.Context, key string) (bool, bool, error) {
	switch key {
	case "auth.mcp_token_configured", "auth.admin_token_configured":
		status, err := GetAuthStatusContext(ctx)
		if key == "auth.mcp_token_configured" {
			return status.MCPConfigured, true, err
		}
		return status.AdminConfigured, true, err
	case "tunnel.api_key_configured":
		dashboard, err := TunnelStatus()
		if err != nil {
			return false, true, err
		}
		return strings.TrimSpace(dashboard.Config.APIKey) != "", true, nil
	case "tunnel.admin.key_configured":
		cfg, err := LoadConfig(ctx)
		return strings.TrimSpace(cfg.Tunnel.Admin.Key) != "", true, err
	case "tunnel.admin.configured":
		status, err := TunnelAdminKeyStatusContext(ctx)
		return status.Configured, true, err
	case "tunnel.admin.verified":
		status, err := TunnelAdminKeyStatusContext(ctx)
		return status.Verified, true, err
	case "tunnel.admin.read_access":
		status, err := TunnelAdminKeyStatusContext(ctx)
		return status.Access.Read, true, err
	case "tunnel.admin.manage_access":
		status, err := TunnelAdminKeyStatusContext(ctx)
		return status.Access.Manage, true, err
	case "integrations.typesafe.api_key_configured":
		status, err := typesafeintegration.Credential(config.RootPath())
		return status.Configured, true, err
	case "telegram.token_configured":
		configured, err := telegramTokenConfigured()
		return configured, true, err
	default:
		return false, false, nil
	}
}

func (s *SettingService) readDynamicSetting(ctx context.Context, match config.FieldSelectorMatch) (string, *bool, error) {
	suffix := dynamicSettingSuffix(match.Spec.Key)
	switch match.Spec.Selector.Resource {
	case "upstream.server":
		service, err := s.upstreamService(ctx)
		if err != nil {
			return "", nil, err
		}
		result, err := service.Get(ctx, match.ResourceID)
		if err != nil {
			return "", nil, err
		}
		return upstreamSettingValue(result.Value, suffix)
	case "tunnel.managed":
		result, err := GetManagedTunnel(ctx, match.ResourceID, ManagedTunnelOptions{})
		if err != nil {
			return "", nil, err
		}
		return managedTunnelSettingValue(result.Metadata, suffix)
	default:
		return "", nil, fmt.Errorf("unsupported setting resource: %s", match.Spec.Selector.Resource)
	}
}

func (s *SettingService) setDynamicSetting(ctx context.Context, match config.FieldSelectorMatch, raw string) error {
	suffix := dynamicSettingSuffix(match.Spec.Key)
	switch match.Spec.Selector.Resource {
	case "upstream.server":
		service, err := s.upstreamService(ctx)
		if err != nil {
			return err
		}
		current, err := service.Get(ctx, match.ResourceID)
		if err != nil {
			return err
		}
		server := current.Value
		if err := applyUpstreamSetting(&server, suffix, raw); err != nil {
			return err
		}
		_, err = service.Update(ctx, match.ResourceID, server)
		return err
	case "tunnel.managed":
		if _, err := GetManagedTunnel(ctx, match.ResourceID, ManagedTunnelOptions{}); err != nil {
			return err
		}
		request, err := managedTunnelUpdateRequest(suffix, raw)
		if err != nil {
			return err
		}
		_, err = UpdateManagedTunnel(ctx, match.ResourceID, request, ManagedTunnelOptions{})
		return err
	default:
		return fmt.Errorf("unsupported setting resource: %s", match.Spec.Selector.Resource)
	}
}

func (s *SettingService) upstreamService(ctx context.Context) (*UpstreamService, error) {
	if s != nil && s.upstream != nil {
		return s.upstream, nil
	}
	return LoadUpstreamService(ctx)
}

func upstreamSettingValue(server upstream.Server, suffix string) (string, *bool, error) {
	switch suffix {
	case "enabled":
		return strconv.FormatBool(server.Enabled), nil, nil
	case "name":
		return server.Name, nil, nil
	case "transport":
		return server.Transport, nil, nil
	case "command":
		return server.Command, nil, nil
	case "args":
		return strings.Join(server.Args, ","), nil, nil
	case "cwd":
		return server.CWD, nil, nil
	case "url":
		return server.URL, nil, nil
	case "bearer_token_env_var":
		return server.BearerTokenEnvVar, nil, nil
	case "auth.type":
		return server.Auth.Type, nil, nil
	case "auth.scope":
		return server.Auth.Scope, nil, nil
	case "tool_prefix":
		return server.ToolPrefix, nil, nil
	case "expose":
		return server.Expose, nil, nil
	case "tools":
		return strings.Join(server.Tools, ","), nil, nil
	case "disabled_tools":
		return strings.Join(server.DisabledTools, ","), nil, nil
	case "idle_timeout_sec":
		return strconv.Itoa(server.IdleTimeoutSec), nil, nil
	case "allow_private_network":
		return strconv.FormatBool(server.AllowPrivateNetwork), nil, nil
	default:
		return "", nil, fmt.Errorf("unsupported upstream setting: %s", suffix)
	}
}

func applyUpstreamSetting(server *upstream.Server, suffix, raw string) error {
	if server == nil {
		return errors.New("upstream server is required")
	}
	switch suffix {
	case "enabled":
		value, err := parseSettingBool(raw, "upstream enabled")
		if err != nil {
			return err
		}
		server.Enabled = value
	case "name":
		server.Name = strings.TrimSpace(raw)
	case "transport":
		server.Transport = strings.ToLower(strings.TrimSpace(raw))
	case "command":
		server.Command = strings.TrimSpace(raw)
	case "args":
		server.Args = splitSettingList(raw)
	case "cwd":
		server.CWD = strings.TrimSpace(raw)
	case "url":
		server.URL = strings.TrimSpace(raw)
	case "bearer_token_env_var":
		server.BearerTokenEnvVar = strings.TrimSpace(raw)
	case "auth.type":
		server.Auth.Type = strings.ToLower(strings.TrimSpace(raw))
	case "auth.scope":
		server.Auth.Scope = strings.TrimSpace(raw)
	case "tool_prefix":
		server.ToolPrefix = strings.TrimSpace(raw)
	case "expose":
		server.Expose = strings.ToLower(strings.TrimSpace(raw))
	case "tools":
		server.Tools = splitSettingList(raw)
	case "disabled_tools":
		server.DisabledTools = splitSettingList(raw)
	case "idle_timeout_sec":
		value, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("upstream idle_timeout_sec must be an integer")
		}
		server.IdleTimeoutSec = value
	case "allow_private_network":
		value, err := parseSettingBool(raw, "upstream allow_private_network")
		if err != nil {
			return err
		}
		server.AllowPrivateNetwork = value
	default:
		return fmt.Errorf("unsupported upstream setting: %s", suffix)
	}
	return nil
}

func managedTunnelSettingValue(metadata tunnel.Metadata, suffix string) (string, *bool, error) {
	switch suffix {
	case "name":
		return metadata.Name, nil, nil
	case "description":
		return metadata.Description, nil, nil
	case "tenant_ids":
		return strings.Join(metadata.TenantIDs, ","), nil, nil
	case "workspace_ids":
		return strings.Join(metadata.WorkspaceIDs, ","), nil, nil
	case "organization_ids":
		return strings.Join(metadata.OrganizationIDs, ","), nil, nil
	default:
		return "", nil, fmt.Errorf("unsupported managed tunnel setting: %s", suffix)
	}
}

func managedTunnelUpdateRequest(suffix, raw string) (tunnel.UpdateRequest, error) {
	request := tunnel.UpdateRequest{}
	switch suffix {
	case "name":
		value := strings.TrimSpace(raw)
		request.Name = &value
	case "description":
		value := raw
		request.Description = &value
	case "tenant_ids":
		value := NormalizeTunnelIDs(splitSettingList(raw))
		request.TenantIDs = &value
	case "workspace_ids":
		value := NormalizeTunnelIDs(splitSettingList(raw))
		request.WorkspaceIDs = &value
	case "organization_ids":
		value := NormalizeTunnelIDs(splitSettingList(raw))
		request.OrganizationIDs = &value
	default:
		return tunnel.UpdateRequest{}, fmt.Errorf("unsupported managed tunnel setting: %s", suffix)
	}
	return request, nil
}

func presentationSettingSpecs(prefix string) []config.FieldSpec {
	prefix = strings.TrimSpace(prefix)
	configuredKeys := map[string]struct{}{}
	for _, spec := range config.Settings() {
		if spec.Secret && spec.ConfiguredStateKey != "" {
			configuredKeys[spec.ConfiguredStateKey] = struct{}{}
		}
	}
	result := make([]config.FieldSpec, 0)
	for _, spec := range config.Settings() {
		if spec.InternalOnly {
			continue
		}
		if _, surrogate := configuredKeys[spec.Key]; surrogate {
			continue
		}
		if !spec.Readable && !spec.Secret {
			continue
		}
		if prefix != "" && spec.Key != prefix && !strings.HasPrefix(spec.Key, prefix+".") && !strings.HasPrefix(spec.Key, prefix+"[") {
			continue
		}
		result = append(result, spec)
	}
	return result
}

func presentationSpec(spec config.FieldSpec, selector *config.FieldSelectorMatch) config.FieldSpec {
	if selector == nil {
		return spec
	}
	spec.Key = strings.Replace(spec.Key, "<id>", selector.ResourceID, 1)
	if spec.ReadKey != "" {
		spec.ReadKey = strings.Replace(spec.ReadKey, "<id>", selector.ResourceID, 1)
	}
	if spec.WriteKey != "" {
		spec.WriteKey = strings.Replace(spec.WriteKey, "<id>", selector.ResourceID, 1)
	}
	if spec.ConfiguredStateKey != "" {
		spec.ConfiguredStateKey = strings.Replace(spec.ConfiguredStateKey, "<id>", selector.ResourceID, 1)
	}
	return spec
}

func dynamicSettingSuffix(template string) string {
	_, after, _ := strings.Cut(template, "].")
	return after
}

func dynamicReadKey(match config.FieldSelectorMatch) string {
	if match.Spec.Secret && match.Spec.ConfiguredStateKey != "" {
		return strings.Replace(match.Spec.ConfiguredStateKey, "<id>", match.ResourceID, 1)
	}
	return strings.Replace(match.Spec.Key, "<id>", match.ResourceID, 1)
}

func splitSettingList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\n", ",")
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func parseSettingBool(raw, key string) (bool, error) {
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	return value, nil
}

func settingContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func boolPointer(value bool) *bool { return &value }

func settingBaseline(defaults config.Config, spec config.FieldSpec) (string, bool, error) {
	if spec.Secret || spec.InternalOnly || !spec.Readable || spec.Selector != nil {
		return "", false, nil
	}
	if strings.HasSuffix(spec.Key, "_configured") {
		return "false", true, nil
	}
	if spec.ReadKey == "" {
		return "", false, nil
	}
	value, err := config.RawValue(defaults, spec.ReadKey)
	if err != nil {
		if spec.Virtual || spec.Derived {
			return "", false, nil
		}
		return "", false, err
	}
	return value, true, nil
}

func settingMutationFinish(span *tracepkg.Span, operation, key string, result *SettingResult, resultErr *error) func() {
	return func() {
		if resultErr != nil && *resultErr != nil {
			span.FailMessage("Canonical setting operation failed", *resultErr, tracepkg.String("operation", operation), tracepkg.String("key", key))
			return
		}
		configured := false
		if result != nil && result.Configured != nil {
			configured = *result.Configured
		}
		resultKey := strings.TrimSpace(key)
		if result != nil && result.Spec.Key != "" {
			resultKey = result.Spec.Key
		}
		reloaded := result != nil && result.RuntimeReloaded
		span.EndMessage("Canonical setting operation completed", tracepkg.String("operation", operation), tracepkg.String("key", resultKey), tracepkg.Bool("configured", configured), tracepkg.Bool("runtime_reloaded", reloaded))
	}
}
