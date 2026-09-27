package capability

import "testing"

func TestDoctorCapabilityIsCanonicalReadOnlyOperatorQuery(t *testing.T) {
	spec, ok := Lookup(DoctorRead)
	if !ok {
		t.Fatal("doctor capability missing")
	}
	if spec.CLI.CanonicalPath != "doctor" || !spec.Effects.ReadOnly || !spec.Effects.Idempotent || spec.Effects.Destructive {
		t.Fatalf("doctor capability=%#v", spec)
	}
	if id, ok := ForPath("doctor"); !ok || id != DoctorRead {
		t.Fatalf("doctor path resolution=%q ok=%t", id, ok)
	}
}
