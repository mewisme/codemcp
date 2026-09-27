package capability

import "testing"

func TestRequiredAdminOperationsHaveBindings(t *testing.T) {
	for _, spec := range All() {
		required := false
		for _, contract := range spec.Surfaces {
			if contract.Surface == SurfaceAdminAPI && contract.State == SurfaceRequired {
				required = true
				break
			}
		}
		if required && len(spec.Admin) == 0 {
			t.Errorf("required Admin API operation %s has no route binding", spec.ID)
		}
	}
}

func TestExplicitAdminExemptionsCarryReasons(t *testing.T) {
	for id, reason := range adminExemptionReasons {
		spec, ok := Lookup(id)
		if !ok {
			t.Fatalf("explicit Admin exemption references unknown operation %s", id)
		}
		found := false
		for _, contract := range spec.Surfaces {
			if contract.Surface != SurfaceAdminAPI {
				continue
			}
			found = true
			if contract.State != SurfaceExempt || contract.Reason != reason {
				t.Errorf("Admin exemption for %s = %#v, want reason %q", id, contract, reason)
			}
		}
		if !found {
			t.Errorf("operation %s has no Admin surface contract", id)
		}
	}
}
