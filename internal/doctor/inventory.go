package doctor

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type SourceKind string

const (
	SourceDomainReadModel SourceKind = "domain_read_model"
	SourceDiagnosticHook  SourceKind = "diagnostic_hook"
	SourceDeferred        SourceKind = "deferred"
)

type Definition struct {
	ID      ComponentID   `json:"id"`
	Domain  string        `json:"domain"`
	Owner   string        `json:"owner"`
	Probe   ProbeKind     `json:"probe"`
	Source  SourceKind    `json:"source"`
	Timeout time.Duration `json:"-"`
}

const (
	ComponentInstallCurrent       ComponentID = "install.current"
	ComponentConfigOverview       ComponentID = "config.overview"
	ComponentWorkspaceRegistry    ComponentID = "workspace.registry"
	ComponentWorkspaceLocalState  ComponentID = "workspace.local_state"
	ComponentSecretInventory      ComponentID = "storage.secrets"
	ComponentServiceUser          ComponentID = "service.user"
	ComponentServiceSystem        ComponentID = "service.system"
	ComponentRuntimeControl       ComponentID = "runtime.control"
	ComponentRuntimeListeners     ComponentID = "runtime.listeners"
	ComponentShellProvider        ComponentID = "shell.provider"
	ComponentIntegrationRTK       ComponentID = "integration.rtk"
	ComponentIntegrationCodeGraph ComponentID = "integration.codegraph"
	ComponentIntegrationTypeSafe  ComponentID = "integration.typesafe"
	ComponentUpstreamHealth       ComponentID = "upstream.health"
	ComponentOAuthStatus          ComponentID = "oauth.status"
	ComponentNetworkExposure      ComponentID = "network.exposure"
	ComponentMCPRegistry          ComponentID = "mcp.registry"
	ComponentMCPOpenAIProfile     ComponentID = "mcp.openai_profile"
	ComponentTunnelSecureMCP      ComponentID = "tunnel.secure_mcp"
	ComponentCheckpointHistory    ComponentID = "checkpoint.history"
	ComponentCompletionHistory    ComponentID = "completion.history"
	ComponentApprovalLifecycle    ComponentID = "approval.lifecycle"
	ComponentBackgroundDelivery   ComponentID = "background.delivery"
	ComponentNotificationsHealth  ComponentID = "notifications.health"
	ComponentTelegramHealth       ComponentID = "telegram.health"
	ComponentUpdateAvailability   ComponentID = "update.availability"
	ComponentMigrationReadiness   ComponentID = "migration.readiness"
)

var canonicalInventory = []Definition{
	{ID: ComponentInstallCurrent, Domain: "install", Owner: "application.install", Probe: ProbeLocalRead, Source: SourceDomainReadModel},
	{ID: ComponentConfigOverview, Domain: "config", Owner: "application.config", Probe: ProbeLocalRead, Source: SourceDomainReadModel},
	{ID: ComponentWorkspaceRegistry, Domain: "workspace", Owner: "workspace", Probe: ProbeLocalRead, Source: SourceDomainReadModel},
	{ID: ComponentWorkspaceLocalState, Domain: "workspace", Owner: "workspace", Probe: ProbeLocalRead, Source: SourceDomainReadModel},
	{ID: ComponentSecretInventory, Domain: "storage", Owner: "secretstore", Probe: ProbeLocalRead, Source: SourceDiagnosticHook},
	{ID: ComponentServiceUser, Domain: "runtime", Owner: "service", Probe: ProbeLocalRead, Source: SourceDomainReadModel},
	{ID: ComponentServiceSystem, Domain: "runtime", Owner: "service", Probe: ProbeLocalRead, Source: SourceDomainReadModel},
	{ID: ComponentRuntimeControl, Domain: "runtime", Owner: "runtime.control", Probe: ProbeLocalRead, Source: SourceDomainReadModel},
	{ID: ComponentRuntimeListeners, Domain: "runtime", Owner: "application.runtime", Probe: ProbeLocalRead, Source: SourceDomainReadModel},
	{ID: ComponentShellProvider, Domain: "shell", Owner: "runtime.shell", Probe: ProbeLocalRead, Source: SourceDiagnosticHook},
	{ID: ComponentIntegrationRTK, Domain: "integrations", Owner: "integrations.rtk", Probe: ProbeLocalRead, Source: SourceDomainReadModel},
	{ID: ComponentIntegrationCodeGraph, Domain: "integrations", Owner: "integrations.codegraph", Probe: ProbeLocalRead, Source: SourceDomainReadModel},
	{ID: ComponentIntegrationTypeSafe, Domain: "integrations", Owner: "application.typesafe", Probe: ProbeLocalRead, Source: SourceDomainReadModel},
	{ID: ComponentUpstreamHealth, Domain: "upstream", Owner: "upstream", Probe: ProbeBoundedNetworkRead, Source: SourceDomainReadModel, Timeout: 8 * time.Second},
	{ID: ComponentOAuthStatus, Domain: "oauth", Owner: "oauth", Probe: ProbeLocalRead, Source: SourceDomainReadModel},
	{ID: ComponentNetworkExposure, Domain: "network", Owner: "network", Probe: ProbeLocalRead, Source: SourceDomainReadModel},
	{ID: ComponentMCPRegistry, Domain: "mcp", Owner: "mcp.registry", Probe: ProbeLocalRead, Source: SourceDomainReadModel},
	{ID: ComponentMCPOpenAIProfile, Domain: "mcp", Owner: "mcp.profile", Probe: ProbeLocalRead, Source: SourceDomainReadModel},
	{ID: ComponentTunnelSecureMCP, Domain: "tunnel", Owner: "tunnel", Probe: ProbeLocalRead, Source: SourceDomainReadModel},
	{ID: ComponentCheckpointHistory, Domain: "history", Owner: "checkpoint", Probe: ProbeLocalRead, Source: SourceDomainReadModel},
	{ID: ComponentCompletionHistory, Domain: "history", Owner: "history.completion", Probe: ProbeLocalRead, Source: SourceDomainReadModel},
	{ID: ComponentApprovalLifecycle, Domain: "approval", Owner: "approval", Probe: ProbeLocalRead, Source: SourceDiagnosticHook},
	{ID: ComponentBackgroundDelivery, Domain: "background", Owner: "backgrounddelivery", Probe: ProbeLocalRead, Source: SourceDiagnosticHook},
	{ID: ComponentNotificationsHealth, Domain: "notifications", Owner: "notification", Probe: ProbeLocalRead, Source: SourceDomainReadModel},
	{ID: ComponentTelegramHealth, Domain: "telegram", Owner: "telegram", Probe: ProbeLocalRead, Source: SourceDomainReadModel},
	{ID: ComponentUpdateAvailability, Domain: "update", Owner: "update", Probe: ProbeBoundedNetworkRead, Source: SourceDomainReadModel, Timeout: 4 * time.Second},
	{ID: ComponentMigrationReadiness, Domain: "migration", Owner: "migration", Probe: ProbeLocalRead, Source: SourceDeferred},
}

func Inventory() []Definition {
	result := make([]Definition, len(canonicalInventory))
	copy(result, canonicalInventory)
	return result
}

func DefinitionFor(id ComponentID) (Definition, bool) {
	for _, definition := range canonicalInventory {
		if definition.ID == id {
			return definition, true
		}
	}
	return Definition{}, false
}

func (definition Definition) ProviderSpec() ProviderSpec {
	timeout := definition.Timeout
	if timeout <= 0 {
		timeout = defaultProviderTimeout
	}
	return ProviderSpec{
		ID: definition.ID, Domain: definition.Domain, Owner: definition.Owner,
		Probe: definition.Probe, Timeout: timeout,
	}
}

func ValidateInventoryCoverage(providers []Provider) error {
	registered := map[ComponentID]ProviderSpec{}
	for _, provider := range providers {
		if provider == nil {
			return fmt.Errorf("diagnostic provider is nil")
		}
		spec := normalizedSpec(provider.Spec())
		definition, ok := DefinitionFor(spec.ID)
		if !ok {
			return fmt.Errorf("diagnostic provider %q is not declared in canonical inventory", spec.ID)
		}
		if _, exists := registered[spec.ID]; exists {
			return fmt.Errorf("duplicate diagnostic provider %q", spec.ID)
		}
		expected := definition.ProviderSpec()
		if spec.Domain != expected.Domain || spec.Owner != expected.Owner || spec.Probe != expected.Probe {
			return fmt.Errorf("diagnostic provider %q does not match canonical inventory", spec.ID)
		}
		registered[spec.ID] = spec
	}

	missing := []string{}
	for _, definition := range canonicalInventory {
		if definition.Source == SourceDeferred {
			continue
		}
		if _, ok := registered[definition.ID]; !ok {
			missing = append(missing, string(definition.ID))
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing required diagnostic providers: %s", strings.Join(missing, ", "))
	}
	return nil
}
