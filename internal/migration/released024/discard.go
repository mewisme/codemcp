package released024

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type DiscardOptions struct {
	Manifest       Manifest
	ServiceRetirer HistoricalServiceRetirer
	RemoveLauncher LauncherRemovalFunc
}

type DiscardResult struct {
	SourceRoot       string `json:"source_root"`
	ServicesRetired  int    `json:"services_retired"`
	LaunchersRemoved int    `json:"launchers_removed"`
	RootRemoved      bool   `json:"root_removed"`
}

func DiscardUnsupportedPredecessor(ctx context.Context, options DiscardOptions) (DiscardResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	manifest := options.Manifest
	if !manifest.Found {
		return DiscardResult{}, errors.New("released predecessor was not detected")
	}
	if manifest.Unsupported <= 0 {
		return DiscardResult{}, errors.New("released predecessor discard requires unsupported migration state")
	}
	if manifest.Source.Release != SourceRelease || !manifest.Source.Marker.Verified {
		return DiscardResult{}, errors.New("released predecessor ownership is not verified")
	}
	root, err := normalizeRoot(manifest.Source.Root)
	if err != nil {
		return DiscardResult{}, err
	}
	if filepath.Dir(root) == root {
		return DiscardResult{}, errors.New("refusing to discard filesystem root")
	}
	marker, verified, err := inspectMarker(root)
	if err != nil {
		return DiscardResult{}, fmt.Errorf("recheck released predecessor marker: %w", err)
	}
	if !verified || !sameComparablePath(marker.Path, manifest.Source.Marker.Path) {
		return DiscardResult{}, errors.New("released predecessor marker changed before discard")
	}

	result := DiscardResult{SourceRoot: root}
	retirer := options.ServiceRetirer
	if retirer == nil {
		retirer = platformHistoricalServiceController{}
	}
	for _, state := range manifest.Services {
		if !state.Installed && !state.Bootstrapped && !state.Running {
			continue
		}
		if state.Ownership != OwnershipVerified {
			return DiscardResult{}, fmt.Errorf("historical service %s ownership is not verified", state.ID)
		}
		if err := retirer.Retire(ctx, manifest.Source, state); err != nil {
			return DiscardResult{}, fmt.Errorf("retire historical service %s before discard: %w", state.ID, err)
		}
		result.ServicesRetired++
	}

	removeLauncher := options.RemoveLauncher
	if removeLauncher == nil {
		removeLauncher = removeHistoricalLauncher
	}
	for _, launcher := range manifest.Launchers {
		if launcher.Ownership == OwnershipAbsent {
			continue
		}
		if launcher.PackageManaged || launcher.Ownership != OwnershipVerified || !launcher.Removable {
			continue
		}
		removed, err := removeLauncher(launcher)
		if err != nil {
			return DiscardResult{}, fmt.Errorf("remove historical launcher %s before discard: %w", launcher.Path, err)
		}
		if removed {
			result.LaunchersRemoved++
		}
	}

	marker, verified, err = inspectMarker(root)
	if err != nil {
		return DiscardResult{}, fmt.Errorf("final released predecessor marker check: %w", err)
	}
	if !verified || strings.TrimSpace(marker.Value) != legacyRootMarkerValue {
		return DiscardResult{}, errors.New("released predecessor marker changed during discard")
	}
	info, err := os.Lstat(root)
	if err != nil {
		return DiscardResult{}, fmt.Errorf("inspect released predecessor root before discard: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return DiscardResult{}, errors.New("released predecessor root is not a regular directory")
	}
	if err := os.RemoveAll(root); err != nil {
		return DiscardResult{}, fmt.Errorf("discard released predecessor root: %w", err)
	}
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return DiscardResult{}, errors.New("released predecessor root still exists after discard")
		}
		return DiscardResult{}, fmt.Errorf("verify released predecessor discard: %w", err)
	}
	result.RootRemoved = true
	return result, nil
}
