package update

import (
	"context"
	"testing"
)

type fakeReleaseSource struct{ release Release }

func (f fakeReleaseSource) Latest(context.Context) (Release, error) { return f.release, nil }

func TestCheckerStatuses(t *testing.T) {
	release := Release{Version: "v1.2.0"}
	checker := Checker{Source: fakeReleaseSource{release: release}}
	for current, want := range map[string]Status{
		"v1.1.9":    StatusAvailable,
		"1.2.0":     StatusUpToDate,
		"v1.3.0":    StatusAhead,
		"dev":       StatusDevelopment,
		"0.0.1-dev": StatusDevelopment,
	} {
		result, err := checker.Check(context.Background(), current)
		if err != nil {
			t.Fatalf("Check(%q): %v", current, err)
		}
		if result.Status != want || result.Latest != "v1.2.0" {
			t.Fatalf("Check(%q) = %+v, want status %q", current, result, want)
		}
	}
}
