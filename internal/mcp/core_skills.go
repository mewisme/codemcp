package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"

	"go.mewis.me/codemcp/internal/instructionpolicy"
	"go.mewis.me/codemcp/internal/skills"
	"go.mewis.me/codemcp/internal/tools"
)

const (
	coreSkillsRegistrationID = "codemcp-core-skills"
	skillContentMaxBytes     = 500_000
)

func ensureCoreSkills(registry *FeatureRegistry, runtime *tools.Runtime) error {
	if registry == nil || runtime == nil {
		return nil
	}
	registry.coreSkillsOnce.Do(func() {
		registry.coreSkillsErr = registry.Register(coreSkillRegistration(runtime))
	})
	return registry.coreSkillsErr
}

func coreSkillRegistration(runtime *tools.Runtime) FeatureRegistration {
	descriptors := []SkillDescriptor{}
	if values, err := globalResolvedSkills(); err == nil {
		descriptors = skillDescriptors(values)
	}
	return FeatureRegistration{
		ID:     coreSkillsRegistrationID,
		Family: FeatureSkills,
		Skills: descriptors,
	}
}

func globalResolvedSkills() ([]skills.Skill, error) {
	policy, err := instructionpolicy.DefaultStore().Load()
	if err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	return skills.DiscoverUser(home, policy)
}

func workspaceResolvedSkills(runtime *tools.Runtime, workspaceID string) ([]skills.Skill, error) {
	if runtime == nil || runtime.Workspaces == nil {
		return nil, errors.New("skill workspace manager is unavailable")
	}
	item, err := runtime.Workspaces.Get(workspaceID)
	if err != nil {
		return nil, err
	}
	policy, err := instructionpolicy.DefaultStore().Load()
	if err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	return skills.DiscoverWithUser(item.Path, home, policy)
}

func skillDescriptors(values []skills.Skill) []SkillDescriptor {
	out := make([]SkillDescriptor, 0, len(values))
	for _, value := range values {
		extensions := map[string]any{
			"source":   value.Source,
			"builtin":  skills.IsBuiltin(value),
			"readOnly": skills.IsBuiltin(value) || value.Source != ".cm",
		}
		out = append(out, SkillDescriptor{
			Name:        value.Name,
			Description: value.Description,
			Extensions:  extensions,
		})
	}
	return out
}

func loadCanonicalSkill(runtime *tools.Runtime, workspaceID, name string, maxBytes int) (skills.Loaded, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return skills.Loaded{}, errors.New("skill name is required")
	}
	if maxBytes <= 0 || maxBytes > skillContentMaxBytes {
		return skills.Loaded{}, errors.New("max skill bytes is out of range")
	}
	if workspaceID == "" {
		policy, err := instructionpolicy.DefaultStore().Load()
		if err != nil {
			return skills.Loaded{}, err
		}
		home, _ := os.UserHomeDir()
		values, err := skills.DiscoverUser(home, policy)
		if err != nil {
			return skills.Loaded{}, err
		}
		return loadResolvedSkill(values, name, maxBytes)
	}
	values, err := workspaceResolvedSkills(runtime, workspaceID)
	if err != nil {
		return skills.Loaded{}, err
	}
	return loadResolvedSkill(values, name, maxBytes)
}

func loadResolvedSkill(values []skills.Skill, name string, maxBytes int) (skills.Loaded, error) {
	for _, value := range values {
		if value.Name != name {
			continue
		}
		if skills.IsBuiltin(value) {
			return skills.LoadWithUser("", "", name, maxBytes, instructionpolicy.Config{})
		}
		data, err := os.ReadFile(value.Path)
		if err != nil {
			return skills.Loaded{}, err
		}
		truncated := len(data) > maxBytes
		if truncated {
			data = data[:maxBytes]
		}
		return skills.Loaded{Skill: value, Content: string(data), Truncated: truncated}, nil
	}
	return skills.Loaded{}, errors.New("unknown project skill: " + name)
}

func skillContentIdentity(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func skillManifest(value skills.Loaded) map[string]any {
	return map[string]any{
		"name":        value.Skill.Name,
		"description": value.Skill.Description,
		"source":      value.Skill.Source,
		"builtin":     skills.IsBuiltin(value.Skill),
		"readOnly":    skills.IsBuiltin(value.Skill) || value.Skill.Source != ".cm",
		"digest":      "sha256:" + skillContentIdentity(value.Content),
		"bytes":       len([]byte(value.Content)),
		"truncated":   value.Truncated,
	}
}

func canonicalSkillInventory(ctx context.Context, runtime *tools.Runtime, workspaceID string) ([]skills.Skill, error) {
	_ = ctx
	if strings.TrimSpace(workspaceID) == "" {
		return globalResolvedSkills()
	}
	return workspaceResolvedSkills(runtime, workspaceID)
}
