package capability

import "testing"

func TestAdminBindingsRemainTransportMetadataNotProductSurfaceContracts(t *testing.T) {
	for _, spec := range All() {
		if _, ok := spec.Surface(SurfaceAdminAPI); ok {
			t.Fatalf("operation %s treats Admin API as a product surface", spec.ID)
		}
		for _, binding := range spec.Admin {
			normalized := normalizeAdminBinding(binding)
			id, ok := ForAdmin(normalized.Method, normalized.Path)
			if !ok || id != spec.ID {
				t.Fatalf("Admin binding %s %s resolves to %s,%t want %s", normalized.Method, normalized.Path, id, ok, spec.ID)
			}
		}
	}
}

func TestAdminBindingDoesNotCreateBrowserProductContract(t *testing.T) {
	spec, ok := Lookup(OAuthCallbackComplete)
	if !ok {
		t.Fatal("OAuth callback operation missing")
	}
	if len(spec.Admin) == 0 {
		t.Fatal("OAuth callback must retain Admin API transport binding")
	}
	contract, ok := spec.Surface(SurfaceBrowser)
	if !ok || contract.State != SurfaceExempt || contract.Exemption != SurfaceExemptionProtocolOnly {
		t.Fatalf("OAuth callback Browser contract=%#v ok=%t", contract, ok)
	}
	if browserFrontendOperationIDs[OAuthCallbackComplete] {
		t.Fatal("Admin transport binding incorrectly created Browser frontend evidence")
	}
}
