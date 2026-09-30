package capability

const (
	reasonAgentOnly                 = "agent-only operation is exposed through MCP tools"
	reasonProtocolOnly              = "protocol-only operation is not a human interface action"
	reasonNoCLI                     = "no current CLI command owns this operation"
	reasonNoTUI                     = "no current TUI action owns this operation"
	reasonNoBrowser                 = "no current Browser workflow owns this operation"
	reasonNoAdminAPI                = "no current Admin API route owns this operation"
	reasonNoMCP                     = "operation has no MCP tool binding"
	reasonMCPPending                = "MCP tool binding is defined but not active yet"
	reasonTelegramExcluded          = "operation is outside Telegram administration scope"
	reasonSurfaceLocalOnly          = "operation requires local process or filesystem ownership on this surface"
	reasonTelegramManagedCollection = "Telegram exposes the active single-tunnel lifecycle and does not expose managed tunnel collection operations"
	reasonNotApplicable             = "surface is not applicable"
	reasonLLMSurfaceDeferred        = "LLM administration is deferred to the dedicated interface rollout"
	reasonLLMOperatorOnly           = "LLM provider administration is operator-only and is not exposed to requesting MCP agents"
	reasonApprovalExplainDeferred   = "approval explanation review is deferred to the dedicated interface rollout"
	reasonApprovalExplainReviewer   = "approval explanation is reviewer-only and is not exposed to requesting MCP agents"
)

var knownSurfaceReasons = map[string]struct{}{
	reasonAgentOnly: {}, reasonProtocolOnly: {}, reasonNoCLI: {}, reasonNoTUI: {},
	reasonNoBrowser: {}, reasonNoAdminAPI: {}, reasonNoMCP: {}, reasonMCPPending: {},
	reasonTelegramExcluded: {}, reasonSurfaceLocalOnly: {}, reasonTelegramManagedCollection: {}, reasonNotApplicable: {},
	reasonLLMSurfaceDeferred: {}, reasonLLMOperatorOnly: {},
	reasonApprovalExplainDeferred: {}, reasonApprovalExplainReviewer: {},
}

var llmOperationIDs = idSet(
	LLMStatus,
	LLMProviderList,
	LLMProviderGet,
	LLMProviderAdd,
	LLMProviderConfigure,
	LLMProviderRemove,
	LLMProviderSelect,
	LLMProviderModels,
	LLMProviderProbe,
	LLMProviderCredentialSet,
	LLMProviderCredentialClear,
)

var approvalExplainOperationIDs = idSet(
	RequestExplain,
	RequestExplanationView,
	RequestExplainStatus,
)

var remoteLocalOnlyExemptIDs = idSet(
	ServerForeground,
	ConfigInit,
	ConfigUninit,
	ConfigExport,
	ConfigImport,
	ConfigMigrate,
	ConfigMigrateSecrets,
	MCPStdio,
	MCPHTTP,
	TunnelForeground,
)

var tuiExplicitExemptIDs = idSet(
	InstructionSettingsRead,
	InstructionSettingsWrite,
	ProjectContextRead,
	TunnelList,
	TunnelGet,
	TunnelUse,
	TunnelCreate,
	TunnelUpdate,
	TunnelDelete,
)

func surfaceContracts(spec Spec) []SurfaceContract {
	contracts := make([]SurfaceContract, 0, len(AllSurfaces))
	for _, surface := range AllSurfaces {
		contract := SurfaceContract{Surface: surface}
		if llmOperationIDs[spec.ID] || approvalExplainOperationIDs[spec.ID] {
			if surface == SurfaceTUI || surface == SurfaceTelegram {
				contract.State = SurfaceRequired
				contracts = append(contracts, contract)
				continue
			}
			if surface == SurfaceCLI && spec.HasCLI() {
				contract.State = SurfaceRequired
				contracts = append(contracts, contract)
				continue
			}
			if surface == SurfaceBrowser && browserRequiredIDs[spec.ID] {
				contract.State = SurfaceRequired
				contracts = append(contracts, contract)
				continue
			}
			if surface == SurfaceAdminAPI && adminRequired(spec.ID) {
				contract.State = SurfaceRequired
				contracts = append(contracts, contract)
				continue
			}
			contract.State = SurfaceExempt
			if surface == SurfaceMCP {
				contract.Exemption = SurfaceExemptionSurfaceSpecific
				if approvalExplainOperationIDs[spec.ID] {
					contract.Reason = reasonApprovalExplainReviewer
				} else {
					contract.Reason = reasonLLMOperatorOnly
				}
			} else {
				contract.Exemption = SurfaceExemptionDeferred
				if approvalExplainOperationIDs[spec.ID] {
					contract.Reason = reasonApprovalExplainDeferred
				} else {
					contract.Reason = reasonLLMSurfaceDeferred
				}
			}
			contracts = append(contracts, contract)
			continue
		}
		switch surface {
		case SurfaceCLI:
			if spec.HasCLI() {
				contract.State = SurfaceRequired
			} else {
				contract.State = SurfaceExempt
				contract.Exemption, contract.Reason = surfaceExemption(spec, surface)
			}
		case SurfaceTUI:
			if tuiExplicitExemptIDs[spec.ID] {
				contract.State = SurfaceExempt
				contract.Exemption, contract.Reason = surfaceExemption(spec, surface)
			} else if spec.HasCLI() && spec.Audience != AudienceAgent && spec.Audience != AudienceProtocol {
				contract.State = SurfaceRequired
			} else {
				contract.State = SurfaceExempt
				contract.Exemption, contract.Reason = surfaceExemption(spec, surface)
			}
		case SurfaceBrowser:
			if browserRequiredIDs[spec.ID] {
				contract.State = SurfaceRequired
			} else {
				contract.State = SurfaceExempt
				contract.Exemption, contract.Reason = surfaceExemption(spec, surface)
			}
		case SurfaceAdminAPI:
			if adminRequired(spec.ID) {
				contract.State = SurfaceRequired
			} else {
				contract.State = SurfaceExempt
				contract.Exemption, contract.Reason = surfaceExemption(spec, surface)
			}
		case SurfaceMCP:
			if len(spec.MCPTools) > 0 {
				contract.State = SurfaceRequired
			} else {
				contract.State = SurfaceExempt
				contract.Exemption, contract.Reason = surfaceExemption(spec, surface)
			}
		case SurfaceTelegram:
			if telegramRequiredOperations[spec.ID] {
				contract.State = SurfaceRequired
			} else {
				contract.State = SurfaceExempt
				contract.Exemption, contract.Reason = surfaceExemption(spec, surface)
			}
		}
		contracts = append(contracts, contract)
	}
	return contracts
}

func surfaceExemption(spec Spec, surface Surface) (SurfaceExemptionClass, string) {
	if surface == SurfaceMCP && len(spec.PlannedMCPTools) > 0 {
		return SurfaceExemptionDeferred, reasonMCPPending
	}
	switch spec.Audience {
	case AudienceAgent:
		return SurfaceExemptionAgentOnly, reasonAgentOnly
	case AudienceProtocol:
		return SurfaceExemptionProtocolOnly, reasonProtocolOnly
	}
	switch surface {
	case SurfaceCLI:
		return SurfaceExemptionSurfaceSpecific, reasonNoCLI
	case SurfaceTUI:
		return SurfaceExemptionSurfaceSpecific, reasonNoTUI
	case SurfaceBrowser:
		if reason := browserExemptionReason(spec.ID); reason != "" {
			if spec.ID == WorkspaceRelocate {
				return SurfaceExemptionUnsupportedRemote, reason
			}
			return SurfaceExemptionSurfaceSpecific, reason
		}
		if remoteLocalOnlyExemptIDs[spec.ID] {
			return SurfaceExemptionLocalOnly, reasonSurfaceLocalOnly
		}
		return SurfaceExemptionSurfaceSpecific, reasonNoBrowser
	case SurfaceAdminAPI:
		if reason := adminExemptionReason(spec.ID); reason != "" {
			if remoteLocalOnlyExemptIDs[spec.ID] {
				return SurfaceExemptionLocalOnly, reason
			}
			return SurfaceExemptionSurfaceSpecific, reason
		}
		if remoteLocalOnlyExemptIDs[spec.ID] {
			return SurfaceExemptionLocalOnly, reasonSurfaceLocalOnly
		}
		return SurfaceExemptionSurfaceSpecific, reasonNoAdminAPI
	case SurfaceMCP:
		return SurfaceExemptionSurfaceSpecific, reasonNoMCP
	case SurfaceTelegram:
		if telegramLocalOnlyOperations[spec.ID] {
			return SurfaceExemptionLocalOnly, reasonSurfaceLocalOnly
		}
		if telegramManagedTunnelCollectionOperations[spec.ID] {
			return SurfaceExemptionUnsupportedRemote, reasonTelegramManagedCollection
		}
		return SurfaceExemptionSurfaceSpecific, reasonTelegramExcluded
	default:
		return SurfaceExemptionSurfaceSpecific, reasonNotApplicable
	}
}

func validSurfaceReason(reason string) bool {
	if _, ok := knownSurfaceReasons[reason]; ok {
		return true
	}
	for _, value := range adminExemptionReasons {
		if reason == value {
			return true
		}
	}
	for _, value := range browserExemptionReasons {
		if reason == value {
			return true
		}
	}
	return false
}

func validSurfaceExemption(value SurfaceExemptionClass) bool {
	switch value {
	case SurfaceExemptionLocalOnly,
		SurfaceExemptionAgentOnly,
		SurfaceExemptionProtocolOnly,
		SurfaceExemptionUnsupportedRemote,
		SurfaceExemptionSurfaceSpecific,
		SurfaceExemptionDeferred:
		return true
	default:
		return false
	}
}
