package fanout

import (
	"strings"
	"testing"
)

func TestNormalizeModes(t *testing.T) {
	for _, mode := range []Mode{Auto, Conservative, Aggressive} {
		if value, ok := NormalizeRuntimeMode(" " + strings.ToUpper(string(mode)) + " "); !ok || value != mode {
			t.Fatalf("runtime mode %q => %q %v", mode, value, ok)
		}
		if value, ok := NormalizeMode(string(mode)); !ok || value != mode {
			t.Fatalf("mode %q => %q %v", mode, value, ok)
		}
	}
	if value, ok := NormalizeMode(" OFF "); !ok || value != Off {
		t.Fatalf("off => %q %v", value, ok)
	}
	for _, invalid := range []string{"", "full", "parallel", "max"} {
		if _, ok := NormalizeRuntimeMode(invalid); ok {
			t.Fatalf("runtime mode %q accepted", invalid)
		}
	}
	if _, ok := NormalizeRuntimeMode("off"); ok {
		t.Fatal("off must remain transient runtime state")
	}
}

func TestInstructionsProjectModeSpecificStrategy(t *testing.T) {
	tests := []struct {
		mode Mode
		want string
	}{
		{Auto, "Use balanced delegation."},
		{Conservative, "Use a high delegation threshold."},
		{Aggressive, "Proactively decompose substantial work"},
	}
	for _, test := range tests {
		value := Instructions(test.mode)
		if !strings.HasPrefix(value, "FANOUT MODE ACTIVE — level: "+string(test.mode)) || !strings.Contains(value, test.want) || !strings.Contains(value, "# Fanout") {
			t.Fatalf("%s instructions missing expected content: %q", test.mode, value)
		}
		for _, forbidden := range []string{"agent.max_parallel =", "max_depth =", "claim grants", "claim bypass"} {
			if strings.Contains(value, forbidden) {
				t.Fatalf("%s instructions redefine authority with %q", test.mode, forbidden)
			}
		}
	}
	if Instructions(Off) != "" {
		t.Fatal("off instructions must be empty")
	}
}
