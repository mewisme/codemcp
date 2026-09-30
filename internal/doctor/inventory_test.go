package doctor

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestCanonicalInventoryIsUniqueBoundedAndDomainOwned(t *testing.T) {
	inventory := Inventory()
	if len(inventory) == 0 {
		t.Fatal("diagnostic inventory is empty")
	}
	seen := map[ComponentID]struct{}{}
	domains := map[string]bool{}
	for _, definition := range inventory {
		if _, ok := seen[definition.ID]; ok {
			t.Fatalf("duplicate component id %q", definition.ID)
		}
		seen[definition.ID] = struct{}{}
		domains[definition.Domain] = true
		if err := validateSpec(definition.ProviderSpec()); err != nil {
			t.Fatalf("%s: %v", definition.ID, err)
		}
		switch definition.Source {
		case SourceDomainReadModel, SourceDiagnosticHook, SourceDeferred:
		default:
			t.Fatalf("%s has invalid source %q", definition.ID, definition.Source)
		}
	}
	for _, domain := range []string{
		"install", "config", "workspace", "storage", "runtime", "shell", "integrations",
		"upstream", "oauth", "network", "mcp", "tunnel", "history", "approval", "llm",
		"background", "notifications", "telegram", "update", "migration",
	} {
		if !domains[domain] {
			t.Fatalf("diagnostic inventory is missing domain %q", domain)
		}
	}
}

func TestCanonicalInventoryDeclaresOnlyExplicitBoundedNetworkProbes(t *testing.T) {
	network := map[ComponentID]bool{}
	for _, definition := range Inventory() {
		if definition.Probe == ProbeBoundedNetworkRead {
			network[definition.ID] = true
		}
	}
	for _, id := range []ComponentID{ComponentUpstreamHealth, ComponentUpdateAvailability} {
		if !network[id] {
			t.Fatalf("expected bounded network probe %q", id)
		}
		delete(network, id)
	}
	if len(network) != 0 {
		t.Fatalf("unexpected network diagnostic probes: %#v", network)
	}
}

func TestCanonicalInventoryDeclaresSafeHooksAndMigrationReadModel(t *testing.T) {
	for _, id := range []ComponentID{
		ComponentSecretInventory, ComponentShellProvider, ComponentApprovalLifecycle, ComponentBackgroundDelivery,
	} {
		definition, ok := DefinitionFor(id)
		if !ok || definition.Source != SourceDiagnosticHook || definition.Probe != ProbeLocalRead {
			t.Fatalf("%s definition=%#v ok=%t", id, definition, ok)
		}
	}
	migration, ok := DefinitionFor(ComponentMigrationReadiness)
	if !ok || migration.Source != SourceDomainReadModel || migration.Probe != ProbeLocalRead {
		t.Fatalf("migration definition=%#v ok=%t", migration, ok)
	}
	llmProvider, ok := DefinitionFor(ComponentLLMProvider)
	if !ok || llmProvider.Source != SourceDomainReadModel || llmProvider.Probe != ProbeLocalRead {
		t.Fatalf("LLM definition=%#v ok=%t", llmProvider, ok)
	}
}

func TestCanonicalInventoryDoesNotReintroduceRemovedTunnelModels(t *testing.T) {
	var text strings.Builder
	for _, definition := range Inventory() {
		fmt.Fprintf(&text, "%s %s %s\n", definition.ID, definition.Domain, definition.Owner)
	}
	normalized := strings.ToLower(text.String())
	for _, forbidden := range []string{"cfquick", "cf_quick", "admin_profile", "tunnel.instance", "tunnel_instance"} {
		if strings.Contains(normalized, forbidden) {
			t.Fatalf("removed tunnel model %q reappeared in inventory:\n%s", forbidden, normalized)
		}
	}
}

func TestInventoryCoverageRequiresEveryNonDeferredProvider(t *testing.T) {
	providers := make([]Provider, 0, len(Inventory()))
	for _, definition := range Inventory() {
		if definition.Source == SourceDeferred {
			continue
		}
		definition := definition
		providers = append(providers, ProviderFunc{
			Definition: definition.ProviderSpec(),
			Run: func(context.Context) (Component, error) {
				return Component{State: StateHealthy, Severity: SeverityInfo, Summary: "healthy"}, nil
			},
		})
	}
	if err := ValidateInventoryCoverage(providers); err != nil {
		t.Fatalf("complete inventory rejected: %v", err)
	}

	filtered := make([]Provider, 0, len(providers)-1)
	for _, provider := range providers {
		if provider.Spec().ID != ComponentRuntimeControl {
			filtered = append(filtered, provider)
		}
	}
	err := ValidateInventoryCoverage(filtered)
	if err == nil || !strings.Contains(err.Error(), string(ComponentRuntimeControl)) {
		t.Fatalf("missing required provider was not rejected: %v", err)
	}
}

func TestInventoryCoverageTreatsDeferredDefinitionsAsExplicitExemptions(t *testing.T) {
	providers := make([]Provider, 0, len(Inventory()))
	for _, definition := range Inventory() {
		if definition.Source == SourceDeferred {
			continue
		}
		providers = append(providers, ProviderFunc{
			Definition: definition.ProviderSpec(),
			Run: func(context.Context) (Component, error) {
				return Component{State: StateHealthy, Severity: SeverityInfo, Summary: "healthy"}, nil
			},
		})
	}
	if err := ValidateInventoryCoverage(providers); err != nil {
		t.Fatalf("deferred inventory exemption rejected: %v", err)
	}
}
