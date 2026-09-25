package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/mcpconfig"
	mcpconfigwire "go.mewis.me/codemcp/internal/mcpconfig/wire"
	"go.mewis.me/codemcp/internal/upstream"
)

type MCPConfigReadService struct {
	settings *SettingService
}

func NewMCPConfigReadService() *MCPConfigReadService {
	return &MCPConfigReadService{settings: NewSettingService()}
}

func (s *MCPConfigReadService) List(ctx context.Context, prefix string) ([]mcpconfig.Setting, mcpconfig.ErrorCode) {
	if !mcpConfigReadEligible(ctx) {
		return nil, mcpconfig.ErrorAccessDenied
	}
	prefix = strings.TrimSpace(prefix)
	if _, exists := config.SettingByKey(prefix); exists && prefix != "" {
		if _, safe := mcpconfig.ResolveSetting(prefix, mcpconfig.AccessRead); !safe {
			return nil, mcpconfig.ErrorUnsupportedSetting
		}
	}
	if _, exists := config.MatchSettingSelector(prefix); exists {
		spec, safe := mcpconfig.ResolveSetting(prefix, mcpconfig.AccessRead)
		if !safe {
			return nil, mcpconfig.ErrorUnsupportedSetting
		}
		value, err := s.settings.Present(ctx, prefix)
		if err != nil {
			return nil, mcpconfig.ErrorUnsupportedSetting
		}
		projected, ok := mcpconfig.ProjectSetting(value.Spec, value.Value, value.Configured)
		if !ok || projected.Key != spec.Key {
			return nil, mcpconfig.ErrorUnsupportedSetting
		}
		return []mcpconfig.Setting{projected}, ""
	}

	result := make([]mcpconfig.Setting, 0)
	for _, spec := range mcpconfig.SupportedSettings(mcpconfig.AccessRead) {
		if spec.Selector != nil || !mcpConfigSettingMatchesPrefix(spec.Key, prefix) {
			continue
		}
		value, err := s.settings.Present(ctx, spec.Key)
		if err != nil {
			return nil, mcpconfig.ErrorInvalidRequest
		}
		projected, ok := mcpconfig.ProjectSetting(value.Spec, value.Value, value.Configured)
		if ok {
			result = append(result, projected)
		}
	}
	return result, ""
}

func (s *MCPConfigReadService) Get(ctx context.Context, key string) (mcpconfig.Setting, mcpconfig.ErrorCode) {
	if !mcpConfigReadEligible(ctx) {
		return mcpconfig.Setting{}, mcpconfig.ErrorAccessDenied
	}
	key = strings.TrimSpace(key)
	spec, ok := mcpconfig.ResolveSetting(key, mcpconfig.AccessRead)
	if !ok {
		return mcpconfig.Setting{}, mcpconfig.ErrorUnsupportedSetting
	}
	value, err := s.settings.Present(ctx, key)
	if err != nil {
		if spec.Selector != nil {
			return mcpconfig.Setting{}, mcpconfig.ErrorUnsupportedSetting
		}
		return mcpconfig.Setting{}, mcpconfig.ErrorInvalidRequest
	}
	projected, ok := mcpconfig.ProjectSetting(value.Spec, value.Value, value.Configured)
	if !ok {
		return mcpconfig.Setting{}, mcpconfig.ErrorUnsupportedSetting
	}
	return projected, ""
}

func (s *MCPConfigReadService) BindSetApproval(ctx context.Context, arguments map[string]any) (mcpconfigwire.SetApprovalBinding, mcpconfig.ErrorCode) {
	canonical, code := s.validateSetChanges(ctx, arguments)
	if code != "" {
		return mcpconfigwire.SetApprovalBinding{}, code
	}
	root := filepath.Clean(config.RootPath())
	fingerprint, err := mcpConfigApprovalFingerprint(root)
	if err != nil {
		return mcpconfigwire.SetApprovalBinding{}, mcpconfig.ErrorInvalidRequest
	}
	return mcpconfigwire.SetApprovalBinding{
		Changes: canonical, ConfigRoot: root, ConfigFingerprint: fingerprint,
	}, ""
}

func (s *MCPConfigReadService) ApplySet(ctx context.Context, arguments map[string]any, approved mcpconfigwire.SetApprovalBinding) (mcpconfigwire.MutationResult, *mcpconfigwire.MutationError) {
	canonical, code := s.validateSetChanges(ctx, arguments)
	if code != "" {
		return mcpconfigwire.MutationResult{}, mcpConfigMutationError(canonical, mcpconfigwire.MutationUnchanged, mcpconfigwire.RuntimeSyncCurrent, code)
	}
	if !mcpConfigChangesEqual(canonical, approved.Changes) {
		return mcpconfigwire.MutationResult{}, mcpConfigMutationError(canonical, mcpconfigwire.MutationUnchanged, mcpconfigwire.RuntimeSyncCurrent, mcpconfigwire.ErrorApprovalRequired)
	}

	before := make(map[string]SettingResult, len(canonical))
	for _, change := range canonical {
		value, err := s.settings.Present(ctx, change.Key)
		if err != nil {
			return mcpconfigwire.MutationResult{}, mcpConfigMutationError(canonical, mcpconfigwire.MutationUnchanged, mcpconfigwire.RuntimeSyncCurrent, mcpconfigwire.ErrorApplyFailed)
		}
		before[change.Key] = value
	}

	root := filepath.Clean(config.RootPath())
	if root != filepath.Clean(approved.ConfigRoot) {
		return mcpconfigwire.MutationResult{}, mcpConfigMutationError(canonical, mcpconfigwire.MutationUnchanged, mcpconfigwire.RuntimeSyncCurrent, mcpconfigwire.ErrorApprovalRequired)
	}
	fingerprint, err := mcpConfigApprovalFingerprint(root)
	if err != nil || fingerprint != strings.TrimSpace(approved.ConfigFingerprint) {
		return mcpconfigwire.MutationResult{}, mcpConfigMutationError(canonical, mcpconfigwire.MutationUnchanged, mcpconfigwire.RuntimeSyncCurrent, mcpconfigwire.ErrorApprovalRequired)
	}

	changes := make([]SettingChange, len(canonical))
	for index, change := range canonical {
		changes[index] = SettingChange{Key: change.Key, Value: change.Value}
	}
	applied, err := s.settings.Apply(ctx, changes)
	if err != nil {
		state := mcpconfigwire.MutationUnchanged
		syncState := mcpconfigwire.RuntimeSyncCurrent
		code := mcpconfigwire.ErrorApplyFailed
		switch {
		case errors.Is(err, ErrConfigReconciliationRequired):
			state = mcpconfigwire.MutationReconciliationRequired
			syncState = mcpconfigwire.RuntimeSyncPending
			code = mcpconfigwire.ErrorReconciliationNeeded
		case errors.Is(err, ErrConfigMutationRolledBack):
			state = mcpconfigwire.MutationRolledBack
			syncState = mcpconfigwire.RuntimeSyncCurrent
		}
		return mcpconfigwire.MutationResult{}, mcpConfigMutationError(canonical, state, syncState, code)
	}

	resultByKey := make(map[string]SettingResult, len(applied.Results))
	for _, result := range applied.Results {
		resultByKey[result.Spec.Key] = result
	}
	outcomes := make([]mcpconfigwire.MutationOutcome, 0, len(canonical))
	changed := false
	for _, change := range canonical {
		current, ok := resultByKey[change.Key]
		if !ok {
			return mcpconfigwire.MutationResult{}, mcpConfigMutationError(canonical, mcpconfigwire.MutationReconciliationRequired, mcpconfigwire.RuntimeSyncPending, mcpconfigwire.ErrorReconciliationNeeded)
		}
		itemChanged := !mcpConfigSettingStateEqual(before[change.Key], current)
		changed = changed || itemChanged
		outcomes = append(outcomes, mcpconfigwire.MutationOutcome{Key: change.Key, Changed: itemChanged})
	}

	state := mcpconfigwire.MutationUnchanged
	syncState := mcpconfigwire.RuntimeSyncCurrent
	if changed {
		state = mcpconfigwire.MutationPersisted
		syncState = mcpconfigwire.RuntimeSyncPersisted
		if applied.RuntimeReloaded {
			state = mcpconfigwire.MutationRuntimeSynced
			syncState = mcpconfigwire.RuntimeSyncCurrent
		}
	}
	keys := make([]string, len(canonical))
	for index, change := range canonical {
		keys[index] = change.Key
	}
	return mcpconfigwire.MutationResult{
		State: state, Keys: keys, Outcomes: outcomes, ChangeCount: len(canonical),
		Changed: changed, RuntimeReloaded: applied.RuntimeReloaded, RuntimeSync: syncState,
	}, nil
}

func (s *MCPConfigReadService) validateSetChanges(ctx context.Context, arguments map[string]any) ([]mcpconfigwire.Change, mcpconfig.ErrorCode) {
	cfg, err := LoadConfig(ctx)
	if err != nil || !mcpconfig.Eligible(cfg, mcpconfig.AccessWrite) {
		return nil, mcpconfig.ErrorAccessDenied
	}
	changes, _, err := mcpconfigwire.CanonicalSetArguments(arguments)
	if err != nil {
		return nil, mcpconfig.ErrorInvalidRequest
	}
	canonical := make([]mcpconfigwire.Change, 0, len(changes))
	for _, change := range changes {
		key := strings.TrimSpace(change.Key)
		if mcpConfigSecretSetting(key) {
			return nil, mcpconfig.ErrorSecretWriteForbidden
		}
		spec, ok := mcpconfig.ResolveSetting(key, mcpconfig.AccessWrite)
		if !ok {
			return nil, mcpconfig.ErrorUnsupportedSetting
		}
		if err := mcpconfig.ValidateWriteSpec(spec); err != nil {
			return nil, mcpconfig.ErrorUnsupportedSetting
		}
		if spec.Selector != nil {
			if _, err := s.settings.Present(ctx, spec.Key); err != nil {
				return nil, mcpconfig.ErrorUnsupportedSetting
			}
		}
		canonical = append(canonical, mcpconfigwire.Change{Key: spec.Key, Value: change.Value})
	}
	settingChanges := make([]SettingChange, len(canonical))
	for index, change := range canonical {
		settingChanges[index] = SettingChange{Key: change.Key, Value: change.Value}
	}
	if err := s.settings.ValidateApply(ctx, settingChanges); err != nil {
		return nil, mcpconfig.ErrorInvalidRequest
	}
	return canonical, ""
}

func mcpConfigChangesEqual(left, right []mcpconfigwire.Change) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func mcpConfigSettingStateEqual(left, right SettingResult) bool {
	if left.Value != right.Value {
		return false
	}
	if left.Configured == nil || right.Configured == nil {
		return left.Configured == nil && right.Configured == nil
	}
	return *left.Configured == *right.Configured
}

func mcpConfigMutationError(changes []mcpconfigwire.Change, state mcpconfigwire.MutationState, syncState mcpconfigwire.RuntimeSyncState, code mcpconfigwire.ErrorCode) *mcpconfigwire.MutationError {
	keys := make([]string, 0, len(changes))
	for _, change := range changes {
		keys = append(keys, strings.TrimSpace(change.Key))
	}
	return &mcpconfigwire.MutationError{
		Code: code, State: state, Keys: keys, ChangeCount: len(keys),
		RuntimeReloaded: false, RuntimeSync: syncState,
	}
}

func mcpConfigReadEligible(ctx context.Context) bool {
	cfg, err := LoadConfig(ctx)
	return err == nil && mcpconfig.Eligible(cfg, mcpconfig.AccessRead)
}

func mcpConfigSecretSetting(key string) bool {
	if spec, ok := config.SettingByKey(key); ok {
		return spec.Secret
	}
	if match, ok := config.MatchSettingSelector(key); ok {
		return match.Spec.Secret
	}
	return false
}

func mcpConfigApprovalFingerprint(root string) (string, error) {
	hash := sha256.New()
	paths := []string{
		config.DefaultPath(),
		config.TunnelSecretPath(),
		upstream.Path(),
	}
	for _, path := range paths {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return "", err
		}
		_, _ = hash.Write([]byte(filepath.ToSlash(relative)))
		_, _ = hash.Write([]byte{0})
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			_, _ = hash.Write([]byte("<missing>"))
			_, _ = hash.Write([]byte{0})
			continue
		}
		if err != nil {
			return "", err
		}
		_, _ = hash.Write(data)
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func mcpConfigSettingMatchesPrefix(key, prefix string) bool {
	if prefix == "" {
		return true
	}
	return key == prefix || strings.HasPrefix(key, prefix+".") || strings.HasPrefix(key, prefix+"[")
}
