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
		cli, ok := spec.Surface(SurfaceCLI)
		if id == RequestExplain || id == RequestExplainStatus {
			if !ok || cli.State != SurfaceRequired || cli.Exemption != "" {
				t.Fatalf("%s/cli surface=%#v ok=%t", id, cli, ok)
			}
		} else if !ok || cli.State != SurfaceExempt || cli.Exemption != SurfaceExemptionDeferred {
			t.Fatalf("%s/cli surface=%#v ok=%t", id, cli, ok)
		}
		for _, surface := range []Surface{SurfaceBrowser, SurfaceAdminAPI} {
			contract, ok := spec.Surface(surface)
			if !ok || contract.State != SurfaceRequired || contract.Exemption != "" {
				t.Fatalf("%s/%s surface=%#v ok=%t", id, surface, contract, ok)
			}
		}
		tui, ok := spec.Surface(SurfaceTUI)
		if !ok || tui.State != SurfaceRequired || tui.Exemption != "" {
			t.Fatalf("%s/tui surface=%#v ok=%t", id, tui, ok)
		}
		telegram, ok := spec.Surface(SurfaceTelegram)
		if !ok || telegram.State != SurfaceRequired || telegram.Exemption != "" {
			t.Fatalf("%s/telegram surface=%#v ok=%t", id, telegram, ok)
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
