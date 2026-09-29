package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type UninstallResult struct {
	Layout                  Layout   `json:"layout"`
	DirectRemoved           bool     `json:"direct_removed"`
	CanonicalRemoved        bool     `json:"canonical_removed"`
	LegacyRemoved           []string `json:"legacy_removed,omitempty"`
	LegacyPreserved         []string `json:"legacy_preserved,omitempty"`
	ConfigRootPreserved     bool     `json:"config_root_preserved"`
	ExternalCleanupRequired bool     `json:"external_cleanup_required,omitempty"`
}

type UninstallOptions struct {
	Layout             Layout
	PreserveBinaryTree bool
}

type uninstallDependencies struct {
	FindLegacyAliases       func() ([]LegacyAlias, error)
	FindLegacyInstallations func(Layout, string) ([]LegacyInstallation, error)
}

func defaultUninstallDependencies() uninstallDependencies {
	return uninstallDependencies{
		FindLegacyAliases:       FindLegacyAliases,
		FindLegacyInstallations: FindLegacyInstallations,
	}
}

// UninstallOwned removes only installer-owned executable state. The install
// root is shared with CodeMCP configuration/runtime state, so unknown and
// user-authored entries are intentionally left in place.
func UninstallOwned(layout Layout) (UninstallResult, error) {
	return UninstallOwnedWithOptions(UninstallOptions{Layout: layout})
}

func UninstallOwnedWithOptions(options UninstallOptions) (UninstallResult, error) {
	return uninstallOwnedWithDependencies(options, defaultUninstallDependencies())
}

func uninstallOwnedWithDependencies(options UninstallOptions, deps uninstallDependencies) (UninstallResult, error) {
	layout := options.Layout
	if layout.Root == "" {
		var err error
		layout, err = DefaultLayout()
		if err != nil {
			return UninstallResult{}, err
		}
	}
	result := UninstallResult{Layout: layout, ConfigRootPreserved: true}
	metadata, err := ReadMetadata(layout.Metadata)
	if err != nil && !errors.Is(err, ErrMetadataNotFound) {
		return result, err
	}
	if err == nil {
		if metadata.Method != MethodDirect {
			return result, fmt.Errorf("refusing to remove non-direct installation metadata method %q", metadata.Method)
		}
		binDir := metadata.BinDir
		if binDir == "" {
			defaults, defaultErr := DefaultLayout()
			if defaultErr != nil {
				return result, defaultErr
			}
			binDir = defaults.BinDir
		}
		ownedLayout, layoutErr := NewLayout(metadata.InstallDir, binDir)
		if layoutErr != nil {
			return result, layoutErr
		}
		if !samePath(ownedLayout.Root, layout.Root) || !samePath(ownedLayout.BinDir, layout.BinDir) {
			return result, errors.New("install metadata ownership does not match selected layout")
		}
		status, statusErr := StatusCanonical(layout)
		if statusErr != nil {
			return result, statusErr
		}
		if status.State == CanonicalConflict {
			return result, fmt.Errorf("%w: %s", ErrCanonicalConflict, status.Path)
		}
		if status.State == CanonicalInstalled && !options.PreserveBinaryTree {
			if _, removeErr := RemoveCanonical(layout); removeErr != nil {
				return result, removeErr
			}
			result.CanonicalRemoved = true
		}
		if options.PreserveBinaryTree {
			result.ExternalCleanupRequired = true
		} else {
			for _, path := range []string{layout.Current, layout.Versions} {
				if removeErr := os.RemoveAll(path); removeErr != nil {
					return result, fmt.Errorf("remove installer-owned path %s: %w", path, removeErr)
				}
			}
		}
		if !options.PreserveBinaryTree {
			if removeErr := os.Remove(layout.Metadata); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return result, removeErr
			}
			if removeErr := os.Remove(layout.UpdateCache); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return result, removeErr
			}
			removeEmptyDirectory(layout.State)
			removeEmptyDirectory(layout.Root)
		}
		result.DirectRemoved = !options.PreserveBinaryTree
	}

	aliases, err := deps.FindLegacyAliases()
	if err != nil {
		return result, err
	}
	for _, item := range aliases {
		if item.Removable && item.Verified && !item.PackageManaged {
			removed, removeErr := RemoveLegacyAlias(item)
			if removeErr != nil {
				return result, removeErr
			}
			if removed {
				result.LegacyRemoved = append(result.LegacyRemoved, item.Path)
				continue
			}
		}
		result.LegacyPreserved = append(result.LegacyPreserved, item.Path)
	}
	installations, err := deps.FindLegacyInstallations(layout, "")
	if err != nil {
		return result, err
	}
	for _, item := range installations {
		if item.Removable && item.Verified && !item.PackageManaged {
			removed, removeErr := RemoveLegacyInstallation(item)
			if removeErr != nil {
				return result, removeErr
			}
			if removed {
				result.LegacyRemoved = append(result.LegacyRemoved, item.Path)
				continue
			}
		}
		result.LegacyPreserved = append(result.LegacyPreserved, item.Path)
	}
	return result, nil
}

func removeEmptyDirectory(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(filepath.Clean(path))
}
