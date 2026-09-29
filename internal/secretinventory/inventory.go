package secretinventory

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.mewis.me/codemcp/internal/auth"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/integrations/typesafe"
	"go.mewis.me/codemcp/internal/llm"
	"go.mewis.me/codemcp/internal/oauth"
	"go.mewis.me/codemcp/internal/secretstore"
	telegramcredential "go.mewis.me/codemcp/internal/telegram/credential"
	"go.mewis.me/codemcp/internal/upstream"
)

type Category string

const (
	CategoryAuthToken      Category = "auth-token"
	CategoryTunnelKey      Category = "tunnel-key"
	CategoryOAuth          Category = "oauth-credential"
	CategoryUpstream       Category = "upstream-credential"
	CategoryRelayToken     Category = "relay-token"
	CategoryTelegramToken  Category = "telegram-token"
	CategoryIntegrationKey Category = "integration-api-key"
	CategoryLLMKey         Category = "llm-api-key"
)

type PortablePolicy string

const PortableExcluded PortablePolicy = "excluded"

type Descriptor struct {
	Account         string             `json:"account"`
	Domain          secretstore.Domain `json:"domain"`
	Category        Category           `json:"category"`
	Configured      bool               `json:"configured"`
	Present         bool               `json:"present"`
	Optional        bool               `json:"optional"`
	RuntimeRequired bool               `json:"runtime_required"`
	PortablePolicy  PortablePolicy     `json:"portable_policy"`
}

type candidate struct {
	account    string
	configured bool
}

type registration struct {
	domain          secretstore.Domain
	category        Category
	optional        bool
	runtimeRequired bool
	enumerate       func(string) ([]candidate, error)
	owns            func(string) bool
}

var registrations = []registration{
	staticRegistration(secretstore.DomainAuth, CategoryAuthToken, true, false, auth.SecretEntries()),
	{
		domain: secretstore.DomainTunnel, category: CategoryTunnelKey, optional: true, runtimeRequired: true,
		enumerate: configuredNames(func(root string) ([]string, error) { return config.TunnelSecretEntries(root) }),
		owns: exactAccounts(
			secretstore.AccountName(secretstore.DomainTunnel, "runtime-key"),
			secretstore.AccountName(secretstore.DomainTunnel, "admin-key"),
		),
	},
	{
		domain: secretstore.DomainOAuth, category: CategoryOAuth, optional: true, runtimeRequired: true,
		enumerate: configuredNames(func(root string) ([]string, error) {
			return oauth.NewStore(configformat.StructuredPath(root, "oauth")).SecretEntries()
		}),
		owns: domainPrefix(secretstore.DomainOAuth),
	},
	{
		domain: secretstore.DomainUpstream, category: CategoryUpstream, optional: true, runtimeRequired: true,
		enumerate: configuredNames(func(root string) ([]string, error) {
			return upstream.NewStore(configformat.StructuredPath(root, "upstreams")).SecretEntries()
		}),
		owns: domainPrefix(secretstore.DomainUpstream),
	},
	staticRegistration(secretstore.DomainCluster, CategoryRelayToken, true, true, []string{
		secretstore.AccountName(secretstore.DomainCluster, "relay-token"),
	}),
	staticRegistration(secretstore.DomainTelegram, CategoryTelegramToken, true, true, telegramcredential.SecretEntries()),
	staticRegistration(secretstore.DomainTypeSafe, CategoryIntegrationKey, true, true, typesafe.SecretEntries()),
	{
		domain: secretstore.DomainLLM, category: CategoryLLMKey, optional: true, runtimeRequired: true,
		enumerate: func(root string) ([]candidate, error) {
			accounts, err := llm.CredentialAccounts(root)
			if err != nil {
				return nil, err
			}
			store := secretstore.New(root)
			result := make([]candidate, 0, len(accounts))
			for _, account := range accounts {
				_, err := store.Get(account)
				switch {
				case err == nil:
					result = append(result, candidate{account: account, configured: true})
				case errors.Is(err, secretstore.ErrNotFound):
					result = append(result, candidate{account: account})
				default:
					return nil, err
				}
			}
			return result, nil
		},
		owns: domainPrefix(secretstore.DomainLLM),
	},
}

func Inventory(root string) ([]Descriptor, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("secret inventory root is required")
	}
	store := secretstore.New(root)
	byAccount := map[string]Descriptor{}
	for _, registered := range registrations {
		items, err := registered.enumerate(root)
		if err != nil {
			return nil, fmt.Errorf("enumerate %s managed secrets: %w", registered.domain, err)
		}
		for _, item := range items {
			account := strings.TrimSpace(item.account)
			if account == "" {
				continue
			}
			present := false
			_, err := store.Get(account)
			switch {
			case err == nil:
				present = true
			case errors.Is(err, secretstore.ErrNotFound):
			default:
				return nil, fmt.Errorf("inspect %s managed secret: %w", registered.domain, err)
			}
			descriptor := Descriptor{
				Account: account, Domain: registered.domain, Category: registered.category,
				Configured: item.configured, Present: present, Optional: registered.optional,
				RuntimeRequired: registered.runtimeRequired, PortablePolicy: PortableExcluded,
			}
			if previous, exists := byAccount[account]; exists && previous != descriptor {
				return nil, fmt.Errorf("managed secret account %q has conflicting inventory owners", account)
			}
			byAccount[account] = descriptor
		}
	}
	result := make([]Descriptor, 0, len(byAccount))
	for _, descriptor := range byAccount {
		result = append(result, descriptor)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Account < result[j].Account })
	return result, nil
}

func Names(descriptors []Descriptor) []string {
	result := make([]string, 0, len(descriptors))
	seen := map[string]struct{}{}
	for _, descriptor := range descriptors {
		account := strings.TrimSpace(descriptor.Account)
		if account == "" {
			continue
		}
		if _, exists := seen[account]; exists {
			continue
		}
		seen[account] = struct{}{}
		result = append(result, account)
	}
	sort.Strings(result)
	return result
}

func RecognizedAccount(account string) bool {
	account = strings.TrimSpace(account)
	if account == "" {
		return false
	}
	for _, registered := range registrations {
		if registered.owns != nil && registered.owns(account) {
			return true
		}
	}
	return false
}

func staticRegistration(domain secretstore.Domain, category Category, optional, runtimeRequired bool, accounts []string) registration {
	return registration{
		domain: domain, category: category, optional: optional, runtimeRequired: runtimeRequired,
		enumerate: func(root string) ([]candidate, error) {
			store := secretstore.New(root)
			result := make([]candidate, 0, len(accounts))
			for _, account := range accounts {
				_, err := store.Get(account)
				switch {
				case err == nil:
					result = append(result, candidate{account: account, configured: true})
				case errors.Is(err, secretstore.ErrNotFound):
					result = append(result, candidate{account: account})
				default:
					return nil, err
				}
			}
			return result, nil
		},
		owns: exactAccounts(accounts...),
	}
}

func configuredNames(enumerate func(string) ([]string, error)) func(string) ([]candidate, error) {
	return func(root string) ([]candidate, error) {
		names, err := enumerate(root)
		if err != nil {
			return nil, err
		}
		result := make([]candidate, 0, len(names))
		for _, name := range names {
			result = append(result, candidate{account: name, configured: true})
		}
		return result, nil
	}
}

func exactAccounts(accounts ...string) func(string) bool {
	owned := map[string]struct{}{}
	for _, account := range accounts {
		owned[account] = struct{}{}
	}
	return func(account string) bool {
		_, ok := owned[account]
		return ok
	}
}

func domainPrefix(domain secretstore.Domain) func(string) bool {
	prefix := secretstore.Name(string(domain)) + "/"
	return func(account string) bool {
		return strings.HasPrefix(account, prefix)
	}
}
