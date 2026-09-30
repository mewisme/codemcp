package capability

import "testing"

func TestLLMCapabilitiesEncodeCanonicalSecurityAndSurfaceContracts(t *testing.T) {
	reads := []ID{LLMStatus, LLMProviderList, LLMProviderGet}
	for _, id := range reads {
		spec, ok := Lookup(id)
		if !ok || spec.Kind != KindQuery || !spec.Effects.ReadOnly || spec.Risk != RiskNone {
			t.Fatalf("LLM read %s spec=%#v ok=%t", id, spec, ok)
		}
	}
	for _, id := range []ID{LLMProviderModels, LLMProviderProbe} {
		spec, ok := Lookup(id)
		if !ok || spec.Kind != KindQuery || !spec.Effects.ReadOnly || !spec.Effects.OpenWorld {
			t.Fatalf("LLM network read %s spec=%#v ok=%t", id, spec, ok)
		}
	}
	for _, id := range []ID{LLMProviderAdd, LLMProviderConfigure, LLMProviderSelect} {
		spec, ok := Lookup(id)
		if !ok || spec.Kind != KindMutation || spec.Risk != RiskState || spec.Confirmation.Mode != ConfirmationNone {
			t.Fatalf("LLM mutation %s spec=%#v ok=%t", id, spec, ok)
		}
	}
	remove, ok := Lookup(LLMProviderRemove)
	if !ok || remove.Kind != KindMutation || remove.Risk != RiskDestructive || !remove.Effects.Destructive || remove.Confirmation.Mode != ConfirmationRequired {
		t.Fatalf("LLM remove spec=%#v ok=%t", remove, ok)
	}
	for _, id := range []ID{LLMProviderCredentialSet, LLMProviderCredentialClear} {
		spec, ok := Lookup(id)
		if !ok || spec.Kind != KindMutation || spec.Risk != RiskSensitive || spec.Effects.ReadOnly {
			t.Fatalf("LLM credential mutation %s spec=%#v ok=%t", id, spec, ok)
		}
	}
	for _, id := range []ID{
		LLMStatus, LLMProviderList, LLMProviderGet, LLMProviderAdd, LLMProviderConfigure, LLMProviderRemove,
		LLMProviderSelect, LLMProviderModels, LLMProviderProbe, LLMProviderCredentialSet, LLMProviderCredentialClear,
	} {
		spec, ok := Lookup(id)
		if !ok {
			t.Fatalf("LLM operation %s missing", id)
		}
		cli, ok := spec.Surface(SurfaceCLI)
		if !ok || cli.State != SurfaceRequired || cli.Exemption != "" {
			t.Fatalf("LLM operation %s/cli contract=%#v ok=%t", id, cli, ok)
		}
		for _, surface := range []Surface{SurfaceBrowser, SurfaceAdminAPI} {
			contract, ok := spec.Surface(surface)
			if !ok || contract.State != SurfaceRequired || contract.Exemption != "" {
				t.Fatalf("LLM operation %s/%s contract=%#v ok=%t", id, surface, contract, ok)
			}
		}
		tui, ok := spec.Surface(SurfaceTUI)
		if !ok || tui.State != SurfaceRequired || tui.Exemption != "" {
			t.Fatalf("LLM operation %s/tui contract=%#v ok=%t", id, tui, ok)
		}
		telegram, ok := spec.Surface(SurfaceTelegram)
		if !ok || telegram.State != SurfaceExempt || telegram.Exemption != SurfaceExemptionDeferred {
			t.Fatalf("LLM operation %s/telegram contract=%#v ok=%t", id, telegram, ok)
		}
		mcp, ok := spec.Surface(SurfaceMCP)
		if !ok || mcp.State != SurfaceExempt || mcp.Exemption != SurfaceExemptionSurfaceSpecific {
			t.Fatalf("LLM operation %s MCP contract=%#v ok=%t", id, mcp, ok)
		}
	}
}
