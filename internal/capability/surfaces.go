package capability

const (
	reasonAgentOnly        = "agent-only operation is exposed through MCP tools"
	reasonProtocolOnly     = "protocol-only operation is not a human interface action"
	reasonNoCLI            = "no current CLI command owns this operation"
	reasonNoTUI            = "no current TUI action owns this operation"
	reasonNoBrowser        = "no current Browser workflow owns this operation"
	reasonNoAdminAPI       = "no current Admin API route owns this operation"
	reasonNoMCP            = "operation has no MCP tool binding"
	reasonMCPPending       = "MCP tool binding is defined but not active yet"
	reasonTelegramPending  = "Telegram administration coverage is staged and not globally active yet"
	reasonTelegramExcluded = "operation is outside Telegram administration scope"
	reasonNotApplicable    = "surface is not applicable"
)

var knownSurfaceReasons = map[string]struct{}{
	reasonAgentOnly: {}, reasonProtocolOnly: {}, reasonNoCLI: {}, reasonNoTUI: {},
	reasonNoBrowser: {}, reasonNoAdminAPI: {}, reasonNoMCP: {}, reasonMCPPending: {}, reasonTelegramPending: {},
	reasonTelegramExcluded: {}, reasonNotApplicable: {},
}

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
		switch surface {
		case SurfaceCLI:
			if spec.HasCLI() {
				contract.State = SurfaceRequired
			} else {
				contract.State = SurfaceExempt
				contract.Reason = exemptionReason(spec, surface)
			}
		case SurfaceTUI:
			if tuiExplicitExemptIDs[spec.ID] {
				contract.State = SurfaceExempt
				contract.Reason = reasonNoTUI
			} else if spec.HasCLI() && spec.Audience != AudienceAgent && spec.Audience != AudienceProtocol {
				contract.State = SurfaceRequired
			} else {
				contract.State = SurfaceExempt
				contract.Reason = exemptionReason(spec, surface)
			}
		case SurfaceBrowser:
			if browserRequiredIDs[spec.ID] {
				contract.State = SurfaceRequired
			} else {
				contract.State = SurfaceExempt
				contract.Reason = exemptionReason(spec, surface)
			}
		case SurfaceAdminAPI:
			if adminRequired(spec.ID) {
				contract.State = SurfaceRequired
			} else {
				contract.State = SurfaceExempt
				contract.Reason = exemptionReason(spec, surface)
			}
		case SurfaceMCP:
			if len(spec.MCPTools) > 0 {
				contract.State = SurfaceRequired
			} else {
				contract.State = SurfaceExempt
				contract.Reason = exemptionReason(spec, surface)
			}
		case SurfaceTelegram:
			if spec.Audience == AudienceOperator || spec.Audience == AudienceReviewer {
				contract.State = SurfacePlanned
				contract.Reason = reasonTelegramPending
			} else {
				contract.State = SurfaceExempt
				contract.Reason = exemptionReason(spec, surface)
			}
		}
		contracts = append(contracts, contract)
	}
	return contracts
}

func exemptionReason(spec Spec, surface Surface) string {
	if surface == SurfaceMCP && len(spec.PlannedMCPTools) > 0 {
		return reasonMCPPending
	}
	switch spec.Audience {
	case AudienceAgent:
		return reasonAgentOnly
	case AudienceProtocol:
		return reasonProtocolOnly
	}
	switch surface {
	case SurfaceCLI:
		return reasonNoCLI
	case SurfaceTUI:
		return reasonNoTUI
	case SurfaceBrowser:
		return reasonNoBrowser
	case SurfaceAdminAPI:
		if reason := adminExemptionReason(spec.ID); reason != "" {
			return reason
		}
		return reasonNoAdminAPI
	case SurfaceMCP:
		return reasonNoMCP
	case SurfaceTelegram:
		return reasonTelegramExcluded
	default:
		return reasonNotApplicable
	}
}

func validSurfaceReason(reason string) bool {
	_, ok := knownSurfaceReasons[reason]
	if ok {
		return true
	}
	for _, value := range adminExemptionReasons {
		if reason == value {
			return true
		}
	}
	return false
}
