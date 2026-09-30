package application

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"go.mewis.me/codemcp/internal/config"
	typesafeintegration "go.mewis.me/codemcp/internal/integrations/typesafe"
	"go.mewis.me/codemcp/internal/secretstore"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/tunnel"
	"go.mewis.me/codemcp/internal/upstream"
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

func (s *SettingService) ValidateApply(ctx context.Context, changes []SettingChange) error {
	ctx = settingContext(ctx)
	resolved, err := resolveSettingChanges(changes)
	if err != nil || len(resolved) == 0 {
		return err
	}
	if owned, err := llmSettingChanges(resolved); err != nil {
		return err
	} else if owned {
		return s.validateLLMSettingChanges(ctx, resolved)
	}
	resource, resourceID, dynamic, err := settingDynamicTransaction(resolved)
	if err != nil {
		return err
	}
	if dynamic {
		switch resource {
		case "upstream.server":
			service, err := s.upstreamService(ctx)
			if err != nil {
				return err
			}
			current, err := service.Get(ctx, resourceID)
			if err != nil {
				return err
			}
			staged := current.Value
			for _, item := range resolved {
				raw := item.change.Value
				if item.change.Unset {
					raw = ""
				}
				if err := applyUpstreamSetting(&staged, dynamicSettingSuffix(item.spec.Key), raw); err != nil {
					return err
				}
			}
			_, err = upstream.NormalizeServer(staged)
			return err
		case "tunnel.managed":
			if len(resolved) != 1 {
				return errors.New("managed tunnel settings are remote mutations and cannot be combined in a multi-setting transaction")
			}
			item := resolved[0]
			if _, err := GetManagedTunnel(ctx, item.selector.ResourceID, ManagedTunnelOptions{}); err != nil {
				return err
			}
			raw := item.change.Value
			if item.change.Unset {
				raw = ""
			}
			_, err := managedTunnelUpdateRequest(dynamicSettingSuffix(item.spec.Key), raw)
			return err
		default:
			return fmt.Errorf("unsupported setting resource: %s", resource)
		}
	}
	if item, ok, err := typeSafeSecretSettingChange(resolved); ok || err != nil {
		if err != nil {
			return err
		}
		if !item.change.Unset && strings.TrimSpace(item.change.Value) == "" {
			return errors.New("integrations.typesafe.api_key must not be empty; unset it to clear the credential")
		}
		return nil
	}
	if _, ok, err := telegramSecretSettingChange(resolved); ok || err != nil {
		return err
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
	current, err := loader()
	if err != nil {
		return err
	}
	staged := current
	for _, item := range resolved {
		handler := staticSettingMutationHandlers[item.spec.Key]
		if handler == nil {
			handler = mutateConfigSetting
		}
		if err := handler(&staged, item); err != nil {
			return err
		}
	}
	return config.Validate(staged)
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
	if owned, err := llmSettingChanges(resolved); err != nil {
		return SettingApplyResult{}, err
	} else if owned {
		return s.applyLLMSettingChanges(ctx, resolved)
	}
	dynamicResource, dynamicID, hasDynamic, err := settingDynamicTransaction(resolved)
	if err != nil {
		return SettingApplyResult{}, err
	}
	if hasDynamic {
		switch dynamicResource {
		case "upstream.server":
			return s.applyDynamicUpstreamSettingChanges(ctx, dynamicID, resolved)
		case "tunnel.managed":
			if len(resolved) != 1 {
				return SettingApplyResult{}, errors.New("managed tunnel settings are remote mutations and cannot be combined in a multi-setting transaction")
			}
			return s.applyDynamicSettingChange(ctx, resolved[0])
		default:
			return SettingApplyResult{}, fmt.Errorf("unsupported setting resource: %s", dynamicResource)
		}
	}
	if item, ok, err := typeSafeSecretSettingChange(resolved); ok || err != nil {
		if err != nil {
			return SettingApplyResult{}, err
		}
		value := item.change.Value
		if item.change.Unset {
			value = ""
		} else if strings.TrimSpace(value) == "" {
			return SettingApplyResult{}, errors.New("integrations.typesafe.api_key must not be empty; unset it to clear the credential")
		}
		reloaded, err := applyTypeSafeSecretSettingChange(ctx, value)
		if err != nil {
			return SettingApplyResult{}, err
		}
		presented, err := s.Present(ctx, item.spec.Key)
		if err != nil {
			return SettingApplyResult{}, err
		}
		cfg, err := LoadConfig(ctx)
		if err != nil {
			return SettingApplyResult{}, err
		}
		presented.RuntimeReloaded = reloaded
		return SettingApplyResult{Results: []SettingResult{presented}, Config: cfg, RuntimeReloaded: reloaded}, nil
	}
	if item, ok, err := telegramSecretSettingChange(resolved); ok || err != nil {
		if err != nil {
			return SettingApplyResult{}, err
		}
		return s.applyTelegramSecretSettingChange(ctx, item)
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
	if err := s.validateApprovalExplainEnable(ctx, previous, next, resolved); err != nil {
		validateSpan.FailMessage("Canonical setting mutation validation failed", err, tracepkg.String("key", "approval.explain.mode"))
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

func (s *SettingService) validateApprovalExplainEnable(ctx context.Context, previous, next config.Config, resolved []resolvedSettingChange) error {
	changed := false
	for _, item := range resolved {
		if item.spec.Key == "approval.explain.mode" {
			changed = true
			break
		}
	}
	if !changed || previous.Approval.Explain.Mode == next.Approval.Explain.Mode || next.Approval.Explain.Mode == config.ApprovalExplainOff {
		return nil
	}
	service := s.llmService()
	status, err := service.Status(ctx)
	if err != nil {
		return fmt.Errorf("validate approval Explain LLM provider: %w", err)
	}
	if !status.Active.Configured {
		reason := strings.TrimSpace(status.Active.Reason)
		if reason == "" {
			reason = "active LLM provider is not configured"
		}
		return fmt.Errorf("approval.explain.mode requires a configured active LLM provider: %s", reason)
	}
	if _, err := service.Probe(ctx, string(status.Active.ID)); err != nil {
		return fmt.Errorf("approval.explain.mode requires a successful active LLM probe: %w", err)
	}
	return nil
}

func applyTypeSafeSecretSettingChange(ctx context.Context, value string) (bool, error) {
	root := config.RootPath()
	previous, err := typesafeintegration.LoadAPIKey(root)
	previousExists := err == nil
	if err != nil && !errors.Is(err, secretstore.ErrNotFound) {
		return false, err
	}
	value = strings.TrimSpace(value)
	if (previousExists && previous == value) || (!previousExists && value == "") {
		return false, nil
	}
	if err := typesafeintegration.UpdateAPIKey(root, value); err != nil {
		return false, err
	}
	_, reloaded, reloadErr := reloadPersistedConfigIfRunning(ctx)
	if reloadErr == nil {
		return reloaded, nil
	}

	restoreValue := ""
	if previousExists {
		restoreValue = previous
	}
	if restoreErr := typesafeintegration.UpdateAPIKey(root, restoreValue); restoreErr != nil {
		return false, errors.Join(
			fmt.Errorf("reload TypeSafe credential runtime: %w", reloadErr),
			fmt.Errorf("restore TypeSafe credential: %w", restoreErr),
			ErrConfigReconciliationRequired,
		)
	}
	_, _, reconcileErr := reloadPersistedConfigIfRunning(ctx)
	if reconcileErr != nil {
		return false, errors.Join(
			fmt.Errorf("reload TypeSafe credential runtime: %w", reloadErr),
			fmt.Errorf("reconcile restored TypeSafe credential runtime: %w", reconcileErr),
			ErrConfigReconciliationRequired,
		)
	}
	return false, errors.Join(
		fmt.Errorf("reload TypeSafe credential runtime: %w", reloadErr),
		ErrConfigMutationRolledBack,
	)
}

func typeSafeSecretSettingChange(resolved []resolvedSettingChange) (resolvedSettingChange, bool, error) {
	for _, item := range resolved {
		if item.spec.Key != "integrations.typesafe.api_key" {
			continue
		}
		if len(resolved) != 1 {
			return resolvedSettingChange{}, false, errors.New("TypeSafe API key mutation cannot be combined with config fields in one transaction")
		}
		return item, true, nil
	}
	return resolvedSettingChange{}, false, nil
}

func telegramSecretSettingChange(resolved []resolvedSettingChange) (resolvedSettingChange, bool, error) {
	for _, item := range resolved {
		if item.spec.Key != "telegram.token" {
			continue
		}
		if len(resolved) != 1 {
			return resolvedSettingChange{}, false, errors.New("telegram bot token mutation cannot be combined with config fields in one transaction")
		}
		if !item.change.Unset && strings.TrimSpace(item.change.Value) == "" {
			return resolvedSettingChange{}, false, errors.New("telegram.token must not be empty; unset it to clear the credential")
		}
		return item, true, nil
	}
	return resolvedSettingChange{}, false, nil
}

func (s *SettingService) applyTelegramSecretSettingChange(ctx context.Context, item resolvedSettingChange) (SettingApplyResult, error) {
	value := strings.TrimSpace(item.change.Value)
	if item.change.Unset {
		value = ""
	}
	if err := writeTelegramToken(value); err != nil {
		return SettingApplyResult{}, err
	}
	presented, err := s.Present(ctx, item.spec.Key)
	if err != nil {
		return SettingApplyResult{}, err
	}
	cfg, err := LoadConfig(ctx)
	if err != nil {
		return SettingApplyResult{}, err
	}
	return SettingApplyResult{Results: []SettingResult{presented}, Config: cfg}, nil
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
			identity = dynamicReadKey(*selector)
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

func settingDynamicTransaction(resolved []resolvedSettingChange) (resource, resourceID string, dynamic bool, err error) {
	static := false
	for _, item := range resolved {
		if item.selector == nil {
			static = true
			continue
		}
		if item.selector.Spec.Selector == nil {
			return "", "", false, fmt.Errorf("unsupported setting: %s", item.change.Key)
		}
		currentResource := item.selector.Spec.Selector.Resource
		currentID := item.selector.ResourceID
		if !dynamic {
			resource, resourceID, dynamic = currentResource, currentID, true
			continue
		}
		if resource != currentResource || resourceID != currentID {
			return "", "", false, fmt.Errorf("setting batch spans unsupported transaction owners: %s[%s] and %s[%s]", resource, resourceID, currentResource, currentID)
		}
	}
	if dynamic && static {
		return "", "", false, fmt.Errorf("setting batch spans unsupported transaction owners: global config and %s[%s]", resource, resourceID)
	}
	return resource, resourceID, dynamic, nil
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

func (s *SettingService) applyDynamicUpstreamSettingChanges(ctx context.Context, resourceID string, items []resolvedSettingChange) (SettingApplyResult, error) {
	service, err := s.upstreamService(ctx)
	if err != nil {
		return SettingApplyResult{}, err
	}
	current, err := service.Get(ctx, resourceID)
	if err != nil {
		return SettingApplyResult{}, err
	}
	staged := current.Value
	for _, item := range items {
		if item.selector == nil || item.selector.Spec.Selector == nil || item.selector.Spec.Selector.Resource != "upstream.server" || item.selector.ResourceID != resourceID {
			return SettingApplyResult{}, errors.New("upstream setting transaction contains a mismatched resource owner")
		}
		raw := item.change.Value
		if item.change.Unset {
			raw = ""
		}
		if err := applyUpstreamSetting(&staged, dynamicSettingSuffix(item.spec.Key), raw); err != nil {
			return SettingApplyResult{}, err
		}
	}
	normalized, err := upstream.NormalizeServer(staged)
	if err != nil {
		return SettingApplyResult{}, err
	}

	updated := current.Value
	reconciled := false
	if !reflect.DeepEqual(current.Value, normalized) {
		updated, reconciled, err = service.applySettingUpdate(ctx, resourceID, normalized)
		if err != nil {
			return SettingApplyResult{}, err
		}
	}

	results := make([]SettingResult, 0, len(items))
	for _, item := range items {
		spec := presentationSpec(item.spec, item.selector)
		value, configured, err := upstreamSettingValue(updated, dynamicSettingSuffix(item.spec.Key))
		if err != nil {
			return SettingApplyResult{}, err
		}
		results = append(results, SettingResult{Spec: spec, Value: value, Configured: configured, RuntimeReloaded: reconciled})
	}
	cfg, err := LoadConfig(ctx)
	if err != nil {
		return SettingApplyResult{}, err
	}
	return SettingApplyResult{Results: results, Config: cfg, RuntimeReloaded: reconciled}, nil
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
		if spec.Presentation != config.SettingPresentationMaskedPreview {
			return SettingResult{}, fmt.Errorf("managed secret %q must use masked-preview presentation", spec.Key)
		}
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
		result.Value = tracepkg.MaskSecret(raw, true)
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
