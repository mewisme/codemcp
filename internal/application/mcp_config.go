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
	cfg, err := LoadConfig(ctx)
	if err != nil || !mcpconfig.Eligible(cfg, mcpconfig.AccessWrite) {
		return mcpconfigwire.SetApprovalBinding{}, mcpconfig.ErrorAccessDenied
	}
	changes, _, err := mcpconfigwire.CanonicalSetArguments(arguments)
	if err != nil {
		return mcpconfigwire.SetApprovalBinding{}, mcpconfig.ErrorInvalidRequest
	}
	canonical := make([]mcpconfigwire.Change, 0, len(changes))
	for _, change := range changes {
		key := strings.TrimSpace(change.Key)
		if mcpConfigSecretSetting(key) {
			return mcpconfigwire.SetApprovalBinding{}, mcpconfig.ErrorSecretWriteForbidden
		}
		spec, ok := mcpconfig.ResolveSetting(key, mcpconfig.AccessWrite)
		if !ok {
			return mcpconfigwire.SetApprovalBinding{}, mcpconfig.ErrorUnsupportedSetting
		}
		if err := mcpconfig.ValidateWriteSpec(spec); err != nil {
			return mcpconfigwire.SetApprovalBinding{}, mcpconfig.ErrorUnsupportedSetting
		}
		if spec.Selector != nil {
			if _, err := s.settings.Present(ctx, spec.Key); err != nil {
				return mcpconfigwire.SetApprovalBinding{}, mcpconfig.ErrorUnsupportedSetting
			}
		}
		canonical = append(canonical, mcpconfigwire.Change{Key: spec.Key, Value: change.Value})
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
