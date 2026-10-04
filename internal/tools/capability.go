package tools

import (
	"sort"
	"strings"
)

const (
	CapabilityDomainUnclassified = "unclassified"
	CapabilityDomainAgents       = "agents"
	CapabilityDomainCodeGraph    = "codegraph"
	CapabilityDomainConfig       = "config"
	CapabilityDomainContext      = "context"
	CapabilityDomainFilesystem   = "filesystem"
	CapabilityDomainGit          = "git"
	CapabilityDomainIntegrations = "integrations"
	CapabilityDomainMemory       = "memory"
	CapabilityDomainPlans        = "plans"
	CapabilityDomainPrompts      = "prompts"
	CapabilityDomainRewind       = "rewind"
	CapabilityDomainRules        = "rules"
	CapabilityDomainRuntime      = "runtime"
	CapabilityDomainShell        = "shell"
	CapabilityDomainSkills       = "skills"
	CapabilityDomainUpstream     = "upstream"
	CapabilityDomainWorkspace    = "workspace"

	maxCapabilityDomainBytes = 48
)

type CapabilityMetadata struct {
	Domain string `json:"domain"`
}

func toolCapability(domain string) *CapabilityMetadata {
	return &CapabilityMetadata{Domain: domain}
}

type CapabilityGroup struct {
	Domain    string   `json:"domain"`
	Tools     []string `json:"tools"`
	Truncated bool     `json:"truncated,omitempty"`
}

type CapabilityIndex struct {
	Groups        []CapabilityGroup `json:"groups"`
	TotalTools    int               `json:"total_tools"`
	IncludedTools int               `json:"included_tools"`
	Truncated     bool              `json:"truncated,omitempty"`
}

type CapabilityLimits struct {
	MaxGroups            int
	MaxToolsPerGroup     int
	MaxTools             int
	MaxBytes             int
	MaxUnclassifiedTools int
}

func DefaultCapabilityLimits() CapabilityLimits {
	return CapabilityLimits{
		MaxGroups:            32,
		MaxToolsPerGroup:     64,
		MaxTools:             256,
		MaxBytes:             16 * 1024,
		MaxUnclassifiedTools: 32,
	}
}

func GroupCapabilities(schemas []Schema) CapabilityIndex {
	return GroupCapabilitiesWithLimits(schemas, DefaultCapabilityLimits())
}

func GroupCapabilitiesWithLimits(schemas []Schema, limits CapabilityLimits) CapabilityIndex {
	limits = normalizeCapabilityLimits(limits)
	type item struct {
		name   string
		domain string
	}
	items := make([]item, 0, len(schemas))
	for _, schema := range schemas {
		name := strings.TrimSpace(schema.Name)
		if name == "" {
			continue
		}
		domain := CapabilityDomainUnclassified
		if schema.Capability != nil && strings.TrimSpace(schema.Capability.Domain) != "" {
			domain = strings.ToLower(strings.TrimSpace(schema.Capability.Domain))
		}
		items = append(items, item{name: name, domain: domain})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].name != items[j].name {
			return items[i].name < items[j].name
		}
		return items[i].domain < items[j].domain
	})

	byDomain := map[string][]string{}
	seenNames := map[string]struct{}{}
	for _, item := range items {
		if _, seen := seenNames[item.name]; seen {
			continue
		}
		seenNames[item.name] = struct{}{}
		byDomain[item.domain] = append(byDomain[item.domain], item.name)
	}

	index := CapabilityIndex{TotalTools: len(seenNames)}
	domains := make([]string, 0, len(byDomain))
	for domain := range byDomain {
		if domain != CapabilityDomainUnclassified {
			domains = append(domains, domain)
		}
	}
	sort.Strings(domains)
	if _, ok := byDomain[CapabilityDomainUnclassified]; ok {
		domains = append(domains, CapabilityDomainUnclassified)
	}
	if len(domains) > limits.MaxGroups {
		index.Truncated = true
		if _, hasUnclassified := byDomain[CapabilityDomainUnclassified]; hasUnclassified && limits.MaxGroups > 0 {
			classifiedLimit := limits.MaxGroups - 1
			if classifiedLimit < 0 {
				classifiedLimit = 0
			}
			classified := domains[:len(domains)-1]
			if len(classified) > classifiedLimit {
				classified = classified[:classifiedLimit]
			}
			domains = append(append([]string(nil), classified...), CapabilityDomainUnclassified)
		} else {
			domains = domains[:limits.MaxGroups]
		}
	}

	remainingTools := limits.MaxTools
	remainingBytes := limits.MaxBytes
	for _, domain := range domains {
		if remainingTools == 0 || remainingBytes <= len(domain) {
			index.Truncated = true
			break
		}
		group := CapabilityGroup{Domain: domain}
		groupLimit := limits.MaxToolsPerGroup
		if domain == CapabilityDomainUnclassified && limits.MaxUnclassifiedTools < groupLimit {
			groupLimit = limits.MaxUnclassifiedTools
		}
		remainingBytes -= len(domain)
		for _, name := range byDomain[domain] {
			if len(group.Tools) >= groupLimit || remainingTools == 0 || len(name)+1 > remainingBytes {
				group.Truncated = true
				index.Truncated = true
				break
			}
			group.Tools = append(group.Tools, name)
			index.IncludedTools++
			remainingTools--
			remainingBytes -= len(name) + 1
		}
		if len(group.Tools) > 0 || group.Truncated {
			index.Groups = append(index.Groups, group)
		}
	}
	if index.IncludedTools < index.TotalTools {
		index.Truncated = true
	}
	return index
}

func normalizeCapabilityLimits(limits CapabilityLimits) CapabilityLimits {
	defaults := DefaultCapabilityLimits()
	if limits.MaxGroups <= 0 {
		limits.MaxGroups = defaults.MaxGroups
	}
	if limits.MaxToolsPerGroup <= 0 {
		limits.MaxToolsPerGroup = defaults.MaxToolsPerGroup
	}
	if limits.MaxTools <= 0 {
		limits.MaxTools = defaults.MaxTools
	}
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = defaults.MaxBytes
	}
	if limits.MaxUnclassifiedTools <= 0 {
		limits.MaxUnclassifiedTools = defaults.MaxUnclassifiedTools
	}
	return limits
}

func normalizeCapabilityMetadata(metadata *CapabilityMetadata) (*CapabilityMetadata, error) {
	if metadata == nil {
		return nil, nil
	}
	domain := strings.ToLower(strings.TrimSpace(metadata.Domain))
	if domain == "" {
		return nil, errCapabilityDomain("is required")
	}
	if len(domain) > maxCapabilityDomainBytes {
		return nil, errCapabilityDomain("exceeds maximum length")
	}
	if domain == CapabilityDomainUnclassified {
		return nil, errCapabilityDomain("is reserved")
	}
	for index, value := range []byte(domain) {
		letter := value >= 'a' && value <= 'z'
		digit := value >= '0' && value <= '9'
		if index == 0 {
			if !letter {
				return nil, errCapabilityDomain("must start with a lowercase letter")
			}
			continue
		}
		if !letter && !digit && value != '-' {
			return nil, errCapabilityDomain("must contain only lowercase letters, digits, and hyphens")
		}
	}
	if domain[len(domain)-1] == '-' || strings.Contains(domain, "--") {
		return nil, errCapabilityDomain("must use single interior hyphens")
	}
	return &CapabilityMetadata{Domain: domain}, nil
}

type capabilityDomainError string

func (err capabilityDomainError) Error() string {
	return "capability domain " + string(err)
}

func errCapabilityDomain(detail string) error {
	return capabilityDomainError(detail)
}
