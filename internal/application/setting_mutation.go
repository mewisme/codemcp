package application

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"go.mewis.me/codemcp/internal/config"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/tunnel"
)

type SettingChange struct {
	Key   string
	Value string
	Unset bool
}

type SettingApplyResult struct {
	Results         []SettingResult
	Config          config.Config
	RuntimeReloaded bool
}

type resolvedSettingChange struct {
	change   SettingChange
	spec     config.FieldSpec
	selector *config.FieldSelectorMatch
}

type settingMutationHandler func(*config.Config, resolvedSettingChange) error

var staticSettingMutationHandlers = map[string]settingMutationHandler{
	"tunnel.id":                     mutateTrimmedConfigSetting,
	"tunnel.api_key":                mutateTrimmedConfigSetting,
	"tunnel.control_plane_base_url": mutateTrimmedConfigSetting,
	"tunnel.organization_id":        mutateTrimmedConfigSetting,
	"tunnel.admin.key":              mutateTunnelAdminKey,
}

func (s *SettingService) Apply(ctx context.Context, changes []SettingChange) (result SettingApplyResult, resultErr error) {
	ctx = settingContext(ctx)
	keys := settingChangeKeys(changes)
	span := tracepkg.Start(ctx, "CONFIG", "setting.apply", "Applying canonical setting mutation", tracepkg.Int("changes", len(changes)), tracepkg.Any("keys", keys))
	defer func() {
		if resultErr != nil {
			span.FailMessage("Canonical setting mutation failed", resultErr, tracepkg.Int("changes", len(changes)), tracepkg.Any("keys", keys))
			return
		}
		span.EndMessage("Canonical setting mutation applied", tracepkg.Int("changes", len(changes)), tracepkg.Any("keys", keys), tracepkg.Bool("runtime_reloaded", result.RuntimeReloaded))
	}()

	resolved, err := resolveSettingChanges(changes)
	if err != nil {
		return SettingApplyResult{}, err
	}
	if len(resolved) == 0 {
		cfg, err := LoadConfig(ctx)
		if err != nil {
			return SettingApplyResult{}, err
		}
		return SettingApplyResult{Config: cfg}, nil
	}
	for _, item := range resolved {
		if item.selector != nil {
			if len(resolved) != 1 {
				return SettingApplyResult{}, errors.New("dynamic setting selectors cannot be combined with other setting mutations")
			}
			return s.applyDynamicSettingChange(ctx, item)
		}
	}

	replaceRuntimeKey, replaceAdminKey := false, false
	for _, item := range resolved {
		switch item.spec.Key {
		case "tunnel.api_key":
			replaceRuntimeKey = true
		case "tunnel.admin.key":
			replaceAdminKey = true
		}
	}
	loader := config.Load
	if replaceRuntimeKey || replaceAdminKey {
		loader = func() (config.Config, error) {
			return config.LoadForTunnelSecretReplacement(replaceRuntimeKey, replaceAdminKey)
		}
	}
	previous, source, err := loadConfigWithTracedLoader(ctx, "config.mutation.load", "Loading configuration for canonical setting mutation", loader)
	if err != nil {
		return SettingApplyResult{}, err
	}
	next := previous

	validateSpan := tracepkg.Start(ctx, "CONFIG", "config.field.validate", "Validating canonical setting mutation", tracepkg.Int("changes", len(resolved)), tracepkg.Any("keys", keys))
	for _, item := range resolved {
		handler := staticSettingMutationHandlers[item.spec.Key]
		if handler == nil {
			handler = mutateConfigSetting
		}
		if err := handler(&next, item); err != nil {
			validateSpan.FailMessage("Canonical setting mutation validation failed", err, tracepkg.String("key", item.spec.Key))
			return SettingApplyResult{}, err
		}
	}
	if err := config.Validate(next); err != nil {
		validateSpan.FailMessage("Canonical setting mutation validation failed", err, tracepkg.Any("keys", keys))
		return SettingApplyResult{}, err
	}
	validateSpan.EndMessage("Canonical setting mutation validated", tracepkg.Int("changes", len(resolved)), tracepkg.Any("keys", keys))

	if shouldSyncTunnelMetadata(previous.Tunnel, next.Tunnel) {
		if _, _, err := config.SyncTunnelMetadata(ctx, next.Tunnel); err != nil {
			return SettingApplyResult{}, fmt.Errorf("persist tunnel metadata: %w", err)
		}
	}

	reloaded := false
	if !reflect.DeepEqual(previous, next) {
		if _, reloaded, err = saveConfigMutation(ctx, previous, next); err != nil {
			return SettingApplyResult{}, err
		}
	}

	results := make([]SettingResult, 0, len(resolved))
	for _, item := range resolved {
		value, err := settingResultFromConfig(next, item.spec)
		if err != nil {
			return SettingApplyResult{}, err
		}
		value.RuntimeReloaded = reloaded
		results = append(results, value)
	}
	tracepkg.Emit(ctx, "CONFIG", "setting.apply.persisted", "Canonical setting mutation persisted", tracepkg.String("config", source.Path), tracepkg.Int("changes", len(resolved)), tracepkg.Any("keys", keys), tracepkg.Bool("runtime_reloaded", reloaded))
	return SettingApplyResult{Results: results, Config: next, RuntimeReloaded: reloaded}, nil
}

func resolveSettingChanges(changes []SettingChange) ([]resolvedSettingChange, error) {
	resolved := make([]resolvedSettingChange, 0, len(changes))
	seen := make(map[string]struct{}, len(changes))
	adminScopes := 0
	for _, change := range changes {
		change.Key = strings.TrimSpace(change.Key)
		if change.Key == "" {
			return nil, errors.New("setting key is required")
		}
		spec, selector, err := resolveSetting(change.Key)
		if err != nil {
			return nil, err
		}
		if !spec.Writable {
			return nil, fmt.Errorf("setting %q is not writable", change.Key)
		}
		if change.Unset && !spec.Clearable && !spec.DefaultReset {
			return nil, fmt.Errorf("setting %q cannot be cleared or reset", change.Key)
		}
		identity := spec.Key
		if selector != nil {
			identity = change.Key
		}
		if _, exists := seen[identity]; exists {
			return nil, fmt.Errorf("duplicate setting mutation: %s", identity)
		}
		switch spec.Key {
		case "tunnel.admin.organization_id", "tunnel.admin.workspace_id", "tunnel.admin.tenant_id":
			adminScopes++
			if adminScopes > 1 {
				return nil, errors.New("tunnel admin mutation must configure exactly one organization, workspace, or tenant scope")
			}
		}
		seen[identity] = struct{}{}
		resolved = append(resolved, resolvedSettingChange{change: change, spec: spec, selector: selector})
	}
	return resolved, nil
}

func (s *SettingService) applyDynamicSettingChange(ctx context.Context, item resolvedSettingChange) (SettingApplyResult, error) {
	value := item.change.Value
	if item.change.Unset {
		value = ""
	}
	if err := s.setDynamicSetting(ctx, *item.selector, value); err != nil {
		return SettingApplyResult{}, err
	}
	result, err := s.Read(ctx, dynamicReadKey(*item.selector))
	if err != nil {
		return SettingApplyResult{}, err
	}
	cfg, err := LoadConfig(ctx)
	if err != nil {
		return SettingApplyResult{}, err
	}
	return SettingApplyResult{Results: []SettingResult{result}, Config: cfg}, nil
}

func mutateConfigSetting(next *config.Config, item resolvedSettingChange) error {
	raw := item.change.Value
	if item.change.Unset {
		defaults := config.Default()
		value, err := config.RawValue(defaults, item.spec.Key)
		if err != nil {
			return err
		}
		raw = value
	}
	return config.SetValue(next, item.spec.Key, raw)
}

func mutateTrimmedConfigSetting(next *config.Config, item resolvedSettingChange) error {
	if item.change.Unset {
		return mutateConfigSetting(next, item)
	}
	item.change.Value = strings.TrimSpace(item.change.Value)
	return mutateConfigSetting(next, item)
}

func mutateTunnelAdminKey(next *config.Config, item resolvedSettingChange) error {
	if item.change.Unset {
		next.Tunnel.Admin.Key = ""
		tunnel.ApplyAdminScope(&next.Tunnel, tunnel.AdminScope{})
		tunnel.InvalidateAdminVerification(&next.Tunnel)
		return nil
	}
	key := strings.TrimSpace(item.change.Value)
	if key == "" {
		return errors.New("OpenAI admin key is required")
	}
	next.Tunnel.Admin.Key = key
	tunnel.InvalidateAdminVerification(&next.Tunnel)
	return nil
}

func shouldSyncTunnelMetadata(previous, next tunnel.Config) bool {
	if !tunnel.Configured(next) {
		return false
	}
	return previous.ID != next.ID ||
		previous.APIKey != next.APIKey ||
		previous.ControlPlaneBaseURL != next.ControlPlaneBaseURL
}

func settingChangeKeys(changes []SettingChange) []string {
	keys := make([]string, 0, len(changes))
	for _, change := range changes {
		key := strings.TrimSpace(change.Key)
		if key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

func settingApplyResultForKey(result SettingApplyResult, key string) (SettingResult, error) {
	for _, item := range result.Results {
		if item.Spec.Key == key {
			return item, nil
		}
	}
	return SettingResult{}, fmt.Errorf("setting mutation result missing %q", key)
}

func settingResultFromConfig(cfg config.Config, spec config.FieldSpec) (SettingResult, error) {
	result := SettingResult{Spec: spec}
	if spec.Secret {
		raw, err := config.RawValue(cfg, spec.Key)
		if err != nil {
			return SettingResult{}, err
		}
		configured := strings.TrimSpace(raw) != ""
		result.Configured = boolPointer(configured)
		if !configured {
			result.Value = "not configured"
			return result, nil
		}
		switch spec.Presentation {
		case config.SettingPresentationConfiguredState:
			result.Value = "configured"
		case config.SettingPresentationMaskedPreview:
			result.Value = tracepkg.MaskSecret(raw, true)
		default:
			result.Value = "********"
		}
		return result, nil
	}
	if !spec.Readable {
		return SettingResult{}, fmt.Errorf("setting %q is not readable", spec.Key)
	}
	value, err := config.RawValue(cfg, spec.ReadKey)
	if err != nil {
		return SettingResult{}, err
	}
	result.Value = value
	return result, nil
}

func settingChangeForAdminScope(scope tunnel.AdminScope) (SettingChange, error) {
	scope = tunnel.AdminScope{
		OrganizationID: strings.TrimSpace(scope.OrganizationID),
		WorkspaceID:    strings.TrimSpace(scope.WorkspaceID),
		TenantID:       strings.TrimSpace(scope.TenantID),
	}
	if err := tunnel.ValidateAdminScope(scope); err != nil {
		return SettingChange{}, err
	}
	switch {
	case scope.OrganizationID != "":
		return SettingChange{Key: "tunnel.admin.organization_id", Value: scope.OrganizationID}, nil
	case scope.WorkspaceID != "":
		return SettingChange{Key: "tunnel.admin.workspace_id", Value: scope.WorkspaceID}, nil
	default:
		return SettingChange{Key: "tunnel.admin.tenant_id", Value: scope.TenantID}, nil
	}
}
