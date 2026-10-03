package page

import "testing"

func TestInstallFormMapsIntegrationOptOut(t *testing.T) {
	options := (&installFormData{Force: true, SkipMissingIntegrations: true}).Options()
	if !options.Force || !options.SkipMissingIntegrations {
		t.Fatalf("options=%#v", options)
	}
	zero := (*installFormData)(nil).Options()
	if zero.Force || zero.SkipMissingIntegrations {
		t.Fatalf("zero options=%#v", zero)
	}
}
