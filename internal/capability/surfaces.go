package capability

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
			if spec.HasCLI() && spec.Audience != AudienceAgent && spec.Audience != AudienceProtocol {
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
			if len(spec.Admin) > 0 {
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
				contract.Reason = "Telegram interface is not implemented yet"
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
	switch spec.Audience {
	case AudienceAgent:
		return "agent-only operation is exposed through MCP tools"
	case AudienceProtocol:
		return "protocol-only operation is not a human interface action"
	}
	switch surface {
	case SurfaceCLI:
		return "no current CLI command owns this operation"
	case SurfaceTUI:
		return "no current TUI action owns this operation"
	case SurfaceBrowser:
		return "no current Browser workflow owns this operation"
	case SurfaceAdminAPI:
		return "no current Admin API route owns this operation"
	case SurfaceMCP:
		return "operation has no MCP tool binding"
	case SurfaceTelegram:
		return "operation is outside Telegram administration scope"
	default:
		return "surface is not applicable"
	}
}
