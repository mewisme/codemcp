package application

import (
	"context"
	"strings"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/mcpconfig"
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

func mcpConfigReadEligible(ctx context.Context) bool {
	cfg, err := LoadConfig(ctx)
	return err == nil && mcpconfig.Eligible(cfg, mcpconfig.AccessRead)
}

func mcpConfigSettingMatchesPrefix(key, prefix string) bool {
	if prefix == "" {
		return true
	}
	return key == prefix || strings.HasPrefix(key, prefix+".") || strings.HasPrefix(key, prefix+"[")
}
