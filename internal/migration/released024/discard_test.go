package released024

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscardUnsupportedPredecessorRemovesVerifiedLegacyState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "released")
	writeFixture(t, filepath.Join(root, legacyRootMarkerName), legacyRootMarkerValue+"\n")
	writeFixture(t, filepath.Join(root, "unknown.bin"), "legacy")
	marker, verified, err := inspectMarker(root)
	if err != nil || !verified {
		t.Fatalf("marker verified=%t err=%v", verified, err)
	}
	serviceCalls, launcherCalls := 0, 0
	manifest := Manifest{
		Found: true, Unsupported: 1,
		Source:    SourceDescriptor{Release: SourceRelease, Root: root, Marker: marker},
		Services:  []ServiceState{{ID: "svc_old", Ownership: OwnershipVerified, Installed: true}},
		Launchers: []Launcher{{Path: filepath.Join(t.TempDir(), "old-cm"), Ownership: OwnershipVerified, Removable: true}},
	}
	result, err := DiscardUnsupportedPredecessor(t.Context(), DiscardOptions{
		Manifest: manifest,
		ServiceRetirer: historicalRetirerFunc(func(context.Context, SourceDescriptor, ServiceState) error {
			serviceCalls++
			return nil
		}),
		RemoveLauncher: func(Launcher) (bool, error) {
			launcherCalls++
			return true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.RootRemoved || result.ServicesRetired != 1 || result.LaunchersRemoved != 1 || serviceCalls != 1 || launcherCalls != 1 {
		t.Fatalf("result=%#v serviceCalls=%d launcherCalls=%d", result, serviceCalls, launcherCalls)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("released root still exists: %v", err)
	}
}

func TestDiscardUnsupportedPredecessorRefusesUnverifiedOwnership(t *testing.T) {
	root := filepath.Join(t.TempDir(), "released")
	writeFixture(t, filepath.Join(root, legacyRootMarkerName), legacyRootMarkerValue+"\n")
	marker, verified, err := inspectMarker(root)
	if err != nil || !verified {
		t.Fatalf("marker verified=%t err=%v", verified, err)
	}
	manifest := Manifest{
		Found: true, Unsupported: 1,
		Source:   SourceDescriptor{Release: SourceRelease, Root: root, Marker: marker},
		Services: []ServiceState{{ID: "svc_old", Ownership: OwnershipAmbiguous, Installed: true}},
	}
	if _, err := DiscardUnsupportedPredecessor(t.Context(), DiscardOptions{Manifest: manifest}); err == nil {
		t.Fatal("discard accepted ambiguous historical service ownership")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("released root changed after refused discard: %v", err)
	}
}

type historicalRetirerFunc func(context.Context, SourceDescriptor, ServiceState) error

func (fn historicalRetirerFunc) Retire(ctx context.Context, source SourceDescriptor, state ServiceState) error {
	return fn(ctx, source, state)
}
