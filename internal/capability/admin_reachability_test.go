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

func TestBrowserProductRequirementCannotBeSatisfiedByAdminBindingAlone(t *testing.T) {
	spec, ok := Lookup(WorkspaceRelocate)
	if !ok {
		t.Fatal("workspace relocate operation missing")
	}
	if len(spec.Admin) == 0 {
		t.Fatal("workspace relocate must retain Admin API transport binding")
	}
	contract, ok := spec.Surface(SurfaceBrowser)
	if !ok || contract.State != SurfaceRequired {
		t.Fatalf("workspace relocate Browser contract=%#v ok=%t", contract, ok)
	}
	mapping, ok := parityMapping(ProductParityReportSnapshot(), WorkspaceRelocate, SurfaceBrowser)
	if !ok || mapping.Reachable {
		t.Fatalf("Admin binding incorrectly counted as Browser reachability: %#v ok=%t", mapping, ok)
	}
}
