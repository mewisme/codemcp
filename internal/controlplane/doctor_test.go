package controlplane

import "testing"

func TestDoctorIsClassifiedReadOnly(t *testing.T) {
	if !IsReadOnlyPath("doctor") {
		t.Fatal("doctor must remain read-only under control-plane policy")
	}
}
