package capability

import "testing"

func TestApprovalExplainCapabilitiesAreReviewerOnlyAndNeverMCPAgentTools(t *testing.T) {
	ids := []ID{RequestExplain, RequestExplanationView, RequestExplainStatus}
	for _, id := range ids {
		spec, ok := Lookup(id)
		if !ok {
			t.Fatalf("missing capability %s", id)
		}
		if spec.Audience != AudienceReviewer || spec.Authorization != AuthorizationReviewer {
			t.Fatalf("%s audience/auth=%s/%s", id, spec.Audience, spec.Authorization)
		}
		if len(spec.MCPTools) != 0 || len(spec.PlannedMCPTools) != 0 {
			t.Fatalf("%s exposed to requesting MCP agent: %#v", id, spec.MCPTools)
		}
		mcp, ok := spec.Surface(SurfaceMCP)
		if !ok || mcp.State != SurfaceExempt || mcp.Exemption != SurfaceExemptionSurfaceSpecific {
			t.Fatalf("%s MCP surface=%#v ok=%t", id, mcp, ok)
		}
		for _, surface := range []Surface{SurfaceCLI, SurfaceTUI, SurfaceBrowser, SurfaceAdminAPI, SurfaceTelegram} {
			contract, ok := spec.Surface(surface)
			if !ok || contract.State != SurfaceExempt || contract.Exemption != SurfaceExemptionDeferred {
				t.Fatalf("%s/%s surface=%#v ok=%t", id, surface, contract, ok)
			}
		}
	}
	explain, _ := Lookup(RequestExplain)
	if explain.Kind != KindRuntime || explain.Risk != RiskSensitive || !explain.Effects.OpenWorld || explain.Confirmation.Mode != ConfirmationNone {
		t.Fatalf("request.explain semantics=%#v", explain)
	}
	for _, id := range []ID{RequestExplanationView, RequestExplainStatus} {
		spec, _ := Lookup(id)
		if spec.Kind != KindQuery || !spec.Effects.ReadOnly || spec.Risk != RiskNone {
			t.Fatalf("%s semantics=%#v", id, spec)
		}
	}
}
