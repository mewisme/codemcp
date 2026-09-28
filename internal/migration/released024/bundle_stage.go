package released024

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/install"
	"go.mewis.me/codemcp/internal/migration/bundle024"
	statepkg "go.mewis.me/codemcp/internal/state"
)

const bundleSourceMetadataName = ".bundle024-source.json"
const maxBundleSourceMetadataBytes = 64 << 10

type BundleStageOptions struct {
	BundlePath    string
	TargetRoot    string
	Now           func() time.Time
	InjectFailure func(string) error
}

type bundleSourceMetadata struct {
	Version          int
	BundlePath       string
	BundleSHA256     string
	SourceTreeSHA256 string
}

func StageBundle(ctx context.Context, options BundleStageOptions) (StageResult, error) {
	bundlePath := strings.TrimSpace(options.BundlePath)
	if bundlePath == "" {
		return StageResult{}, errors.New("released portable config bundle path is required")
	}
	absoluteBundle, err := filepath.Abs(bundlePath)
	if err != nil {
		return StageResult{}, err
	}
	absoluteBundle = filepath.Clean(absoluteBundle)
	bundleSHA, _, err := hashFile(absoluteBundle, 256<<20)
	if err != nil {
		return StageResult{}, err
	}
	if _, err := bundle024.Inspect(absoluteBundle); err != nil {
		return StageResult{}, err
	}
	targetRoot, err := normalizeStageTarget(options.TargetRoot)
	if err != nil {
		return StageResult{}, err
	}
	sourceRoot := bundleMaterializedRoot(targetRoot, bundleSHA)
	if err := prepareBundleSource(absoluteBundle, bundleSHA, sourceRoot); err != nil {
		return StageResult{}, err
	}

	home, _ := os.UserHomeDir()
	detect := func(ctx context.Context, _ Manifest) (Manifest, error) {
		return Detect(ctx, Options{
			SourceRoot:  sourceRoot,
			HomeDir:     home,
			BundlePaths: []string{absoluteBundle},
			LookupEnv:   func(string) string { return "" },
			FindInstallations: func(install.Layout, string) ([]install.LegacyInstallation, error) {
				return nil, nil
			},
			FindAliases: func() ([]install.LegacyAlias, error) { return nil, nil },
			InspectServices: func(context.Context, SourceDescriptor) ([]ServiceState, error) {
				return []ServiceState{}, nil
			},
		})
	}
	manifest, err := detect(ctx, Manifest{})
	if err != nil {
		return StageResult{}, err
	}
	return Stage(ctx, StageOptions{
		Manifest: manifest, TargetRoot: targetRoot,
		BundleSourcePath: absoluteBundle, BundleSHA256: bundleSHA,
		RefreshManifest: detect,
		ServiceControl:  noHistoricalServiceController{},
		Now:             options.Now, InjectFailure: options.InjectFailure,
	})
}

type noHistoricalServiceController struct{}

func (noHistoricalServiceController) Quiesce(context.Context, SourceDescriptor, ServiceState) error {
	return errors.New("portable bundle staging does not control historical services")
}

func (noHistoricalServiceController) Restore(context.Context, SourceDescriptor, ServiceState) error {
	return nil
}

func bundleMaterializedRoot(targetRoot, bundleSHA string) string {
	suffix := bundleSHA
	if len(suffix) > 12 {
		suffix = suffix[:12]
	}
	return filepath.Join(filepath.Dir(targetRoot), "."+filepath.Base(targetRoot)+".migration-024-bundle-"+suffix+".source")
}

func prepareBundleSource(bundlePath, bundleSHA, sourceRoot string) error {
	metadataPath := filepath.Join(sourceRoot, bundleSourceMetadataName)
	if pathExists(sourceRoot) {
		metadata, err := readBundleSourceMetadata(metadataPath)
		if err != nil {
			return fmt.Errorf("verify existing portable bundle materialization: %w", err)
		}
		if metadata.Version != 1 || metadata.BundleSHA256 != bundleSHA || !sameComparablePath(metadata.BundlePath, bundlePath) {
			return errors.New("existing portable bundle materialization belongs to different source")
		}
		treeSHA, err := fingerprintBundleSource(sourceRoot)
		if err != nil {
			return err
		}
		if treeSHA != metadata.SourceTreeSHA256 {
			return errors.New("existing portable bundle materialization changed after creation")
		}
		return nil
	}
	if _, err := bundle024.Materialize(bundlePath, sourceRoot); err != nil {
		return err
	}
	treeSHA, err := fingerprintBundleSource(sourceRoot)
	if err != nil {
		_ = os.RemoveAll(sourceRoot)
		return err
	}
	metadata := bundleSourceMetadata{Version: 1, BundlePath: bundlePath, BundleSHA256: bundleSHA, SourceTreeSHA256: treeSHA}
	data, err := statepkg.MarshalJSON(metadata)
	if err != nil {
		_ = os.RemoveAll(sourceRoot)
		return err
	}
	if err := statepkg.WriteFileAtomic(metadataPath, data, 0600); err != nil {
		_ = os.RemoveAll(sourceRoot)
		return err
	}
	return nil
}

func readBundleSourceMetadata(path string) (bundleSourceMetadata, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return bundleSourceMetadata{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return bundleSourceMetadata{}, errors.New("portable bundle materialization metadata is not a regular non-symlink file")
	}
	if info.Size() > maxBundleSourceMetadataBytes {
		return bundleSourceMetadata{}, errors.New("portable bundle materialization metadata exceeds size limit")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return bundleSourceMetadata{}, err
	}
	var metadata bundleSourceMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return bundleSourceMetadata{}, err
	}
	return metadata, nil
}

func fingerprintBundleSource(root string) (string, error) {
	type record struct {
		path string
		sum  string
	}
	records := []record{}
	entries := 0
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		entries++
		if entries > maxInventoryEntries {
			return fmt.Errorf("portable bundle materialization exceeds %d entries", maxInventoryEntries)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == bundleSourceMetadataName {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("portable bundle materialization contains symlink: %s", relative)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("portable bundle materialization contains non-regular entry: %s", relative)
		}
		if info.Size() > maxRegularFileBytes {
			return fmt.Errorf("portable bundle materialization file exceeds fingerprint limit: %s", relative)
		}
		total += info.Size()
		if total > maxFingerprintBytes {
			return fmt.Errorf("portable bundle materialization exceeds %d fingerprint bytes", maxFingerprintBytes)
		}
		sum, _, err := hashFile(path, maxRegularFileBytes)
		if err != nil {
			return err
		}
		records = append(records, record{path: relative, sum: sum})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(records, func(i, j int) bool { return records[i].path < records[j].path })
	hash := sha256.New()
	for _, record := range records {
		_, _ = io.WriteString(hash, record.path+"\x00"+record.sum+"\n")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
