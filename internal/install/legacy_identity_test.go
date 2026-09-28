package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspectLegacyInstallationDoesNotTrustDirectLayoutPathAlone(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "current", historicalBinaryName())
	if err := os.MkdirAll(filepath.Dir(bin), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("same-name-unrelated"), 0755); err != nil {
		t.Fatal(err)
	}
	layout, err := NewLayout(root, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, err := InspectLegacyInstallation(layout, bin, bin)
	if err != nil {
		t.Fatal(err)
	}
	if item.Verified || item.Removable || item.Method != MethodUnknown {
		t.Fatalf("unverified direct-layout candidate was claimed: %#v", item)
	}
}
