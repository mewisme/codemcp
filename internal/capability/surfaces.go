package capability

import "strings"

const (
	productSurfacePolicyOwner = "capability.product-surface-policy"

	reasonProtocolOnly        = "operation belongs to the agent or protocol projection rather than an operator/reviewer product workflow"
	reasonBootstrapOnly       = "operation changes bootstrap state that the remote product surface depends on"
	reasonHostLocalOnly       = "operation depends on host-local process, filesystem, or transport state that cannot be safely driven by this surface"
	reasonRemovedArchitecture = "operation belongs to architecture that is no longer part of the current product"
)

var remoteBootstrapOperationIDs = idSet(
	ConfigInit,
	ConfigUninit,
	ConfigImport,
	ConfigMigrate,
	ConfigMigrateSecrets,
)

var remoteHostLocalOperationIDs = idSet(
	ServerForeground,
	MCPStdio,
	MCPHTTP,
	TunnelForeground,
	IntegrationBrowserStatus,
	IntegrationBrowserDoctor,
	IntegrationChatGPTWebStatus,
	IntegrationChatGPTWebLogin,
	IntegrationChatGPTWebLogout,
	IntegrationChatGPTWebDoctor,
	ManagedAgentSpawn,
	ManagedAgentList,
	ManagedAgentGet,
	ManagedAgentWait,
	ManagedAgentSend,
	ManagedAgentCancel,
	SkillInventoryList,
	SkillInventoryInfo,
	SkillInstall,
	SkillUpdate,
	SkillRemove,
)

var tuiHostLocalOperationIDs = idSet(
	IntegrationBrowserStatus,
	IntegrationBrowserDoctor,
	IntegrationChatGPTWebStatus,
	IntegrationChatGPTWebLogin,
	IntegrationChatGPTWebLogout,
	IntegrationChatGPTWebDoctor,
	SkillInventoryList,
	SkillInventoryInfo,
	SkillInstall,
	SkillUpdate,
	SkillRemove,
)

var telegramBootstrapOperationIDs = idSet(
	TelegramSetup,
)

// removedArchitectureOperationIDs is intentionally empty for the live catalog.
// Historical/compatibility identifiers must not be reintroduced merely to
// classify them as exempt.
var removedArchitectureOperationIDs = idSet()

func surfaceContracts(spec Spec) []SurfaceContract {
	contracts := make([]SurfaceContract, 0, len(ProductSurfaces))
	for _, surface := range ProductSurfaces {
		if exemption, ok := productSurfaceExemption(spec, surface); ok {
			contracts = append(contracts, exemption)
			continue
		}
		contracts = append(contracts, SurfaceContract{
			Surface: surface,
			State:   SurfaceRequired,
		})
	}
	return contracts
}

func productSurfaceExemption(spec Spec, surface Surface) (SurfaceContract, bool) {
	switch spec.Audience {
	case AudienceAgent, AudienceProtocol:
		return SurfaceContract{
			Surface:         surface,
			State:           SurfaceExempt,
			Exemption:       SurfaceExemptionProtocolOnly,
			Reason:          reasonProtocolOnly,
			ExemptionOwner:  productSurfacePolicyOwner,
			Guard:           SurfaceGuardProtocolAudience,
			SafeAlternative: protocolSafeAlternative(spec),
		}, true
	}
	if ((surface == SurfaceBrowser || surface == SurfaceTelegram) && remoteBootstrapOperationIDs[spec.ID]) ||
		(surface == SurfaceTelegram && telegramBootstrapOperationIDs[spec.ID]) {
		return SurfaceContract{
			Surface:         surface,
			State:           SurfaceExempt,
			Exemption:       SurfaceExemptionSurfaceBootstrap,
			Reason:          reasonBootstrapOnly,
			ExemptionOwner:  productSurfacePolicyOwner,
			Guard:           SurfaceGuardBootstrapOperation,
			SafeAlternative: cliSafeAlternative(spec),
		}, true
	}
	if ((surface == SurfaceBrowser || surface == SurfaceTelegram) && remoteHostLocalOperationIDs[spec.ID]) ||
		(surface == SurfaceTUI && tuiHostLocalOperationIDs[spec.ID]) {
		return SurfaceContract{
			Surface:         surface,
			State:           SurfaceExempt,
			Exemption:       SurfaceExemptionHostLocalPrimitive,
			Reason:          reasonHostLocalOnly,
			ExemptionOwner:  productSurfacePolicyOwner,
			Guard:           SurfaceGuardHostLocalOperation,
			SafeAlternative: cliSafeAlternative(spec),
		}, true
	}
	if removedArchitectureOperationIDs[spec.ID] {
		return SurfaceContract{
			Surface:        surface,
			State:          SurfaceExempt,
			Exemption:      SurfaceExemptionRemovedArchitecture,
			Reason:         reasonRemovedArchitecture,
			ExemptionOwner: productSurfacePolicyOwner,
			Guard:          SurfaceGuardRemovedArchitecture,
		}, true
	}
	return SurfaceContract{}, false
}

func protocolSafeAlternative(spec Spec) string {
	if len(spec.MCPTools) > 0 {
		return "MCP tool " + spec.MCPTools[0]
	}
	if len(spec.PlannedMCPTools) > 0 {
		return "MCP protocol operation " + spec.PlannedMCPTools[0]
	}
	return ""
}

func cliSafeAlternative(spec Spec) string {
	path := NormalizePath(spec.CLI.CanonicalPath)
	if path == "" {
		return ""
	}
	if path == RootPath {
		return "cm"
	}
	return "cm " + path
}

func validSurfaceReason(reason string) bool {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return false
	}
	lower := strings.ToLower(reason)
	for _, forbidden := range []string{
		"not yet implemented",
		"not exposed",
		"no current",
		"not useful",
		"cli only",
		"telegram only",
	} {
		if strings.Contains(lower, forbidden) {
			return false
		}
	}
	return true
}

func validSurfaceExemption(value SurfaceExemptionClass) bool {
	switch value {
	case SurfaceExemptionProtocolOnly,
		SurfaceExemptionSurfaceBootstrap,
		SurfaceExemptionHostLocalPrimitive,
		SurfaceExemptionRemovedArchitecture:
		return true
	default:
		return false
	}
}

func validSurfaceExemptionGuard(value SurfaceExemptionGuard) bool {
	switch value {
	case SurfaceGuardProtocolAudience,
		SurfaceGuardBootstrapOperation,
		SurfaceGuardHostLocalOperation,
		SurfaceGuardRemovedArchitecture:
		return true
	default:
		return false
	}
}

func exemptionGuardMatches(spec Spec, contract SurfaceContract) bool {
	switch contract.Guard {
	case SurfaceGuardProtocolAudience:
		return spec.Audience == AudienceAgent || spec.Audience == AudienceProtocol
	case SurfaceGuardBootstrapOperation:
		return ((contract.Surface == SurfaceBrowser || contract.Surface == SurfaceTelegram) && remoteBootstrapOperationIDs[spec.ID]) ||
			(contract.Surface == SurfaceTelegram && telegramBootstrapOperationIDs[spec.ID])
	case SurfaceGuardHostLocalOperation:
		return ((contract.Surface == SurfaceBrowser || contract.Surface == SurfaceTelegram) && remoteHostLocalOperationIDs[spec.ID]) ||
			(contract.Surface == SurfaceTUI && tuiHostLocalOperationIDs[spec.ID])
	case SurfaceGuardRemovedArchitecture:
		return removedArchitectureOperationIDs[spec.ID]
	default:
		return false
	}
}
