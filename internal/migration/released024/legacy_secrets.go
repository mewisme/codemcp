package released024

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.mewis.me/codemcp/internal/migration/credentials024"
)

type LegacySecretAccessor struct {
	physicalRoot string
	logicalRoot  string
	sourceSHA256 string
}

func OpenLegacySecretAccessor(manifest Manifest, physicalRoot string) (*LegacySecretAccessor, error) {
	if err := validateStageManifest(manifest); err != nil {
		return nil, err
	}
	physicalRoot = strings.TrimSpace(physicalRoot)
	if physicalRoot == "" {
		return nil, errors.New("retained released credential root is required")
	}
	absolute, err := filepath.Abs(physicalRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve retained released credential root: %w", err)
	}
	absolute = filepath.Clean(absolute)
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, fmt.Errorf("inspect retained released credential root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("retained released credential root must be a real directory")
	}
	marker, verified, err := inspectMarker(absolute)
	if err != nil {
		return nil, err
	}
	if !verified || marker.Value != legacyRootMarkerValue {
		return nil, errors.New("retained released credential root is not authoritative")
	}
	instance, err := inspectInstance(absolute)
	if err != nil {
		return nil, err
	}
	_, fingerprint, unsupported, err := inventoryRoot(absolute, instance)
	if err != nil {
		return nil, err
	}
	if unsupported != manifest.Unsupported || fingerprint != manifest.SourceSHA256 {
		return nil, errors.New("retained released credential root does not match the verified migration manifest")
	}
	return &LegacySecretAccessor{
		physicalRoot: absolute,
		logicalRoot:  filepath.Clean(manifest.Source.Root),
		sourceSHA256: manifest.SourceSHA256,
	}, nil
}

func (a *LegacySecretAccessor) Migrate(destinationRoot string) (credentials024.Result, error) {
	if a == nil || strings.TrimSpace(a.physicalRoot) == "" || strings.TrimSpace(a.logicalRoot) == "" || strings.TrimSpace(a.sourceSHA256) == "" {
		return credentials024.Result{}, errors.New("released credential accessor is not initialized")
	}
	return credentials024.Transform(credentials024.Input{
		SourceRoot:        a.physicalRoot,
		LogicalSourceRoot: a.logicalRoot,
		DestinationRoot:   destinationRoot,
	})
}
