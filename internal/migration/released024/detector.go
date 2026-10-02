package released024

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go.mewis.me/codemcp/internal/install"
	"go.mewis.me/codemcp/internal/migration/bundle024"
	migrationformat "go.mewis.me/codemcp/internal/migration/configformat"
	"go.mewis.me/codemcp/internal/migration/credentials024"
	"go.mewis.me/codemcp/internal/migration/integrations024"
	"go.mewis.me/codemcp/internal/migration/upstream024"
)

const (
	SourceRelease   = "0.2.24"
	ManifestVersion = 1

	legacyRootMarkerName  = ".chatgpt-mcp-root"
	legacyRootMarkerValue = "chatgpt-mcp"
	maxInventoryEntries   = 4096
	maxFingerprintBytes   = 256 << 20
	maxRegularFileBytes   = 64 << 20
	maxLogBytes           = 16 << 20
	maxLauncherEntries    = 256
	maxBundleInputs       = 64
	maxConfigFields       = 512
)

type Classification string

const (
	ClassDurableMigrate         Classification = "durable-migrate"
	ClassTransientDrop          Classification = "transient-drop"
	ClassRegenerate             Classification = "regenerate"
	ClassOptionalSkipWithReport Classification = "optional-skip-with-report"
	ClassUnsupportedFailClosed  Classification = "unsupported/fail-closed"
)

type Ownership string

const (
	OwnershipVerified       Ownership = "verified"
	OwnershipPackageManager Ownership = "package-manager-owned"
	OwnershipAmbiguous      Ownership = "unrelated-or-ambiguous"
	OwnershipAbsent         Ownership = "absent"
)

type Options struct {
	SourceRoot          string
	HomeDir             string
	LocalAppData        string
	BundlePaths         []string
	LookupEnv           func(string) string
	FindInstallations   func(install.Layout, string) ([]install.LegacyInstallation, error)
	FindAliases         func() ([]install.LegacyAlias, error)
	InspectInstallation func(install.Layout, string, string) (install.LegacyInstallation, error)
	InspectAlias        func(string) (install.LegacyAlias, error)
	InspectServices     ServiceInspector
}

type Marker struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Value    string `json:"value"`
	Verified bool   `json:"verified"`
}

type SourceDescriptor struct {
	Release                    string            `json:"release"`
	OperatorHome               string            `json:"operator_home"`
	Root                       string            `json:"root,omitempty"`
	RootSource                 string            `json:"root_source,omitempty"`
	Marker                     Marker            `json:"marker"`
	PlatformDefaultRoot        string            `json:"platform_default_root"`
	PlatformDefaultInstallRoot string            `json:"platform_default_install_root"`
	PlatformDefaultBinDir      string            `json:"platform_default_bin_dir"`
	BinaryName                 string            `json:"binary_name"`
	AliasName                  string            `json:"alias_name"`
	EnvironmentHints           map[string]string `json:"environment_hints"`
	HistoricalServiceIDs       []string          `json:"historical_service_ids"`
}

type ConfigInspection struct {
	Path          string                 `json:"path,omitempty"`
	Format        migrationformat.Format `json:"format,omitempty"`
	Schema        string                 `json:"schema,omitempty"`
	SchemaVersion string                 `json:"schema_version,omitempty"`
	Fields        []string               `json:"fields"`
	Exists        bool                   `json:"exists"`
}

type InstanceInspection struct {
	Path    string `json:"path,omitempty"`
	Version int    `json:"version,omitempty"`
	ID      string `json:"id,omitempty"`
	Exists  bool   `json:"exists"`
	Valid   bool   `json:"valid"`
	Reason  string `json:"reason,omitempty"`
}

type WorkspaceItem struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	HasState bool   `json:"has_state"`
}

type WorkspaceInventory struct {
	Path       string          `json:"path,omitempty"`
	Version    int             `json:"version,omitempty"`
	Workspaces []WorkspaceItem `json:"workspaces"`
	Containers int             `json:"containers"`
}

type Artifact struct {
	Path           string         `json:"path"`
	Classification Classification `json:"classification"`
	Kind           string         `json:"kind"`
	Bytes          int64          `json:"bytes,omitempty"`
	SHA256         string         `json:"sha256,omitempty"`
	Reason         string         `json:"reason"`
}

type Launcher struct {
	Path           string         `json:"path"`
	Target         string         `json:"target,omitempty"`
	Kind           string         `json:"kind"`
	Method         install.Method `json:"method,omitempty"`
	Ownership      Ownership      `json:"ownership"`
	PackageManaged bool           `json:"package_managed"`
	Removable      bool           `json:"removable"`
	Reason         string         `json:"reason"`
}

type ServiceState struct {
	Scope          string    `json:"scope"`
	ID             string    `json:"id"`
	Backend        string    `json:"backend"`
	DefinitionPath string    `json:"definition_path,omitempty"`
	Installed      bool      `json:"installed"`
	Enabled        bool      `json:"enabled"`
	Bootstrapped   bool      `json:"bootstrapped"`
	Running        bool      `json:"running"`
	PID            int       `json:"pid,omitempty"`
	Ownership      Ownership `json:"ownership"`
	Launcher       string    `json:"launcher,omitempty"`
	Binary         string    `json:"binary,omitempty"`
	ConfigRoot     string    `json:"config_root,omitempty"`
	Reason         string    `json:"reason,omitempty"`
}

type ServiceInspector func(context.Context, SourceDescriptor) ([]ServiceState, error)

type BundleInspection struct {
	Path       string               `json:"path"`
	SHA256     string               `json:"sha256"`
	Bytes      int64                `json:"bytes"`
	Inspection bundle024.Inspection `json:"inspection"`
}

type Manifest struct {
	Version      int                        `json:"version"`
	Source       SourceDescriptor           `json:"source"`
	Found        bool                       `json:"found"`
	SourceSHA256 string                     `json:"source_sha256,omitempty"`
	Config       ConfigInspection           `json:"config"`
	Instance     InstanceInspection         `json:"instance"`
	Workspaces   WorkspaceInventory         `json:"workspaces"`
	Credentials  credentials024.Inspection  `json:"credentials"`
	Integrations integrations024.Inspection `json:"integrations"`
	Upstream     upstream024.Inspection     `json:"upstream"`
	Artifacts    []Artifact                 `json:"artifacts"`
	Launchers    []Launcher                 `json:"launchers"`
	Services     []ServiceState             `json:"services"`
	Bundles      []BundleInspection         `json:"bundles"`
	Unsupported  int                        `json:"unsupported"`
}

func Detect(ctx context.Context, options Options) (Manifest, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	descriptor, found, err := resolveSource(options)
	if err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{
		Version:      ManifestVersion,
		Source:       descriptor,
		Found:        found,
		Artifacts:    []Artifact{},
		Launchers:    []Launcher{},
		Services:     []ServiceState{},
		Bundles:      []BundleInspection{},
		Workspaces:   WorkspaceInventory{Workspaces: []WorkspaceItem{}},
		Config:       ConfigInspection{Fields: []string{}},
		Credentials:  credentials024.Inspection{SourceRelease: SourceRelease},
		Integrations: integrations024.Inspection{SourceRelease: SourceRelease},
		Upstream:     upstream024.Inspection{SourceRelease: SourceRelease},
	}
	if found {
		config, err := inspectConfig(descriptor.Root)
		if err != nil {
			return Manifest{}, err
		}
		manifest.Config = config
		if config.Exists {
			integrations, err := integrations024.Inspect(config.Path)
			if err != nil {
				return Manifest{}, fmt.Errorf("inspect released integrations: %w", err)
			}
			manifest.Integrations = integrations
		}
		instance, err := inspectInstance(descriptor.Root)
		if err != nil {
			return Manifest{}, err
		}
		manifest.Instance = instance
		workspaces, err := inspectWorkspaces(descriptor.Root)
		if err != nil {
			return Manifest{}, err
		}
		manifest.Workspaces = workspaces
		credentials, err := credentials024.Inspect(descriptor.Root)
		if err != nil {
			return Manifest{}, fmt.Errorf("inspect released credentials: %w", err)
		}
		manifest.Credentials = credentials
		if upstreamPath, _, exists, err := discoverStructured(descriptor.Root, "upstream"); err != nil {
			return Manifest{}, err
		} else if exists {
			if filepath.Ext(upstreamPath) != ".json" {
				return Manifest{}, errors.New("released upstream state is not JSON; refusing ambiguous upstream schema")
			}
			upstream, err := upstream024.Inspect(upstreamPath)
			if err != nil {
				return Manifest{}, fmt.Errorf("inspect released upstream state: %w", err)
			}
			manifest.Upstream = upstream
		}
		artifacts, sourceSHA, unsupported, err := inventoryRoot(descriptor.Root, instance)
		if err != nil {
			return Manifest{}, err
		}
		manifest.Artifacts, manifest.SourceSHA256, manifest.Unsupported = artifacts, sourceSHA, unsupported
		launchers, err := inspectLaunchers(options, descriptor)
		if err != nil {
			return Manifest{}, err
		}
		manifest.Launchers = launchers
		inspectServices := options.InspectServices
		if inspectServices == nil {
			inspectServices = inspectPlatformServices
		}
		services, err := inspectServices(ctx, descriptor)
		if err != nil {
			return Manifest{}, fmt.Errorf("inspect released managed services: %w", err)
		}
		manifest.Services = normalizeServices(services)
	}
	bundles, err := inspectBundles(options.BundlePaths)
	if err != nil {
		return Manifest{}, err
	}
	manifest.Bundles = bundles
	return manifest, nil
}

func resolveSource(options Options) (SourceDescriptor, bool, error) {
	home := strings.TrimSpace(options.HomeDir)
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return SourceDescriptor{}, false, err
		}
	}
	absoluteHome, err := filepath.Abs(home)
	if err != nil {
		return SourceDescriptor{}, false, err
	}
	home = filepath.Clean(absoluteHome)
	defaults := legacyPlatformDefaults(home, options.LocalAppData)
	lookup := options.LookupEnv
	if lookup == nil {
		lookup = os.Getenv
	}
	hints := map[string]string{}
	for _, key := range []string{"CHATGPT_MCP_CONFIG_DIR", "CHATGPT_MCP_INSTALL_DIR", "CHATGPT_MCP_BIN_DIR"} {
		if value := strings.TrimSpace(lookup(key)); value != "" {
			if absolute, err := filepath.Abs(value); err == nil {
				value = filepath.Clean(absolute)
			}
			hints[key] = value
		}
	}
	descriptor := SourceDescriptor{
		Release:                    SourceRelease,
		OperatorHome:               home,
		PlatformDefaultRoot:        defaults.ConfigRoot,
		PlatformDefaultInstallRoot: defaults.InstallRoot,
		PlatformDefaultBinDir:      defaults.BinDir,
		BinaryName:                 defaults.BinaryName,
		AliasName:                  defaults.AliasName,
		EnvironmentHints:           hints,
		HistoricalServiceIDs: []string{
			historicalServiceID(defaults.ConfigRoot, "user"),
			historicalServiceID(defaults.ConfigRoot, "system"),
		},
		Marker: Marker{Name: legacyRootMarkerName, Value: legacyRootMarkerValue},
	}

	if explicit := strings.TrimSpace(options.SourceRoot); explicit != "" {
		root, err := normalizeRoot(explicit)
		if err != nil {
			return SourceDescriptor{}, false, err
		}
		marker, verified, err := inspectMarker(root)
		if err != nil {
			return SourceDescriptor{}, false, err
		}
		if !verified {
			return SourceDescriptor{}, false, fmt.Errorf("explicit released source does not contain the exact %s marker", legacyRootMarkerName)
		}
		descriptor.Root, descriptor.RootSource, descriptor.Marker = root, "explicit", marker
		descriptor.HistoricalServiceIDs = []string{historicalServiceID(root, "user"), historicalServiceID(root, "system")}
		return descriptor, true, nil
	}

	type candidate struct {
		root   string
		source string
		marker Marker
	}
	candidates := []candidate{}
	seen := map[string]bool{}
	for _, item := range []struct {
		root   string
		source string
	}{
		{root: hints["CHATGPT_MCP_CONFIG_DIR"], source: "legacy-env"},
		{root: defaults.ConfigRoot, source: "platform-default"},
	} {
		if strings.TrimSpace(item.root) == "" {
			continue
		}
		root, err := normalizeRoot(item.root)
		if err != nil {
			return SourceDescriptor{}, false, err
		}
		key := comparablePath(root)
		if seen[key] {
			continue
		}
		seen[key] = true
		marker, verified, err := inspectMarker(root)
		if err != nil {
			return SourceDescriptor{}, false, err
		}
		if verified {
			candidates = append(candidates, candidate{root: root, source: item.source, marker: marker})
		}
	}
	if len(candidates) > 1 {
		paths := make([]string, 0, len(candidates))
		for _, item := range candidates {
			paths = append(paths, item.root)
		}
		sort.Strings(paths)
		return SourceDescriptor{}, false, fmt.Errorf("multiple authoritative released %s roots found: %s", SourceRelease, strings.Join(paths, ", "))
	}
	if len(candidates) == 0 {
		return descriptor, false, nil
	}
	descriptor.Root, descriptor.RootSource, descriptor.Marker = candidates[0].root, candidates[0].source, candidates[0].marker
	descriptor.HistoricalServiceIDs = []string{historicalServiceID(descriptor.Root, "user"), historicalServiceID(descriptor.Root, "system")}
	return descriptor, true, nil
}

func inspectMarker(root string) (Marker, bool, error) {
	path := filepath.Join(root, legacyRootMarkerName)
	marker := Marker{Path: path, Name: legacyRootMarkerName, Value: legacyRootMarkerValue}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return marker, false, nil
	}
	if err != nil {
		return marker, false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return marker, false, fmt.Errorf("released root marker is not a regular non-symlink file: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return marker, false, err
	}
	marker.Verified = strings.TrimSpace(string(data)) == legacyRootMarkerValue
	return marker, marker.Verified, nil
}

func inspectConfig(root string) (ConfigInspection, error) {
	path, format, exists, err := discoverStructured(root, "config")
	if err != nil {
		return ConfigInspection{}, err
	}
	result := ConfigInspection{Path: path, Format: format, Schema: "chatgpt-mcp-0.2.24", SchemaVersion: "unversioned", Fields: []string{}, Exists: exists}
	if !exists {
		return result, nil
	}
	data, err := readBoundedRegular(path, maxRegularFileBytes)
	if err != nil {
		return ConfigInspection{}, err
	}
	raw, err := migrationformat.DecodeGeneric(format, data)
	if err != nil {
		return ConfigInspection{}, fmt.Errorf("decode released config: %w", err)
	}
	object, ok := raw.(map[string]any)
	if !ok {
		return ConfigInspection{}, errors.New("released config must be an object")
	}
	if len(object) > maxConfigFields {
		return ConfigInspection{}, fmt.Errorf("released config field inventory exceeds %d entries", maxConfigFields)
	}
	for key := range object {
		result.Fields = append(result.Fields, key)
	}
	sort.Strings(result.Fields)
	return result, nil
}

func inspectInstance(root string) (InstanceInspection, error) {
	path := filepath.Join(root, "state", "instance.json")
	result := InstanceInspection{Path: path}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return InstanceInspection{}, err
	}
	result.Exists = true
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		result.Reason = "released instance identity is not a regular non-symlink file"
		return result, nil
	}
	data, err := readBoundedRegular(path, maxRegularFileBytes)
	if err != nil {
		return InstanceInspection{}, err
	}
	var stored struct {
		Version  int `json:"version"`
		Identity struct {
			ID        string    `json:"id"`
			Name      string    `json:"name"`
			CreatedAt time.Time `json:"created_at"`
		} `json:"identity"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		result.Reason = "released instance identity is not valid JSON"
		return result, nil
	}
	result.Version = stored.Version
	if stored.Version != 1 {
		result.Reason = fmt.Sprintf("unsupported released instance identity version: %d", stored.Version)
		return result, nil
	}
	id := strings.TrimSpace(stored.Identity.ID)
	if !strings.HasPrefix(id, "inst_") || len(id) != len("inst_")+32 {
		result.Reason = "released instance identity has an invalid id"
		return result, nil
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(id, "inst_")); err != nil {
		result.Reason = "released instance identity has an invalid id"
		return result, nil
	}
	if strings.TrimSpace(stored.Identity.Name) == "" || stored.Identity.CreatedAt.IsZero() {
		result.Reason = "released instance identity is incomplete"
		return result, nil
	}
	result.ID, result.Valid = id, true
	return result, nil
}

func inspectWorkspaces(root string) (WorkspaceInventory, error) {
	path, format, exists, err := discoverStructured(root, "workspaces")
	if err != nil {
		return WorkspaceInventory{}, err
	}
	result := WorkspaceInventory{Path: path, Workspaces: []WorkspaceItem{}}
	if !exists {
		return result, nil
	}
	data, err := readBoundedRegular(path, maxRegularFileBytes)
	if err != nil {
		return WorkspaceInventory{}, err
	}
	var raw struct {
		Version    int `json:"version"`
		Workspaces []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"workspaces"`
		Containers []json.RawMessage `json:"containers"`
	}
	if err := migrationformat.Unmarshal(format, data, &raw); err != nil {
		return WorkspaceInventory{}, fmt.Errorf("decode released workspace registry: %w", err)
	}
	if raw.Version != 4 {
		return WorkspaceInventory{}, fmt.Errorf("unsupported released workspace registry version: %d", raw.Version)
	}
	if len(raw.Workspaces) > maxInventoryEntries || len(raw.Containers) > maxInventoryEntries {
		return WorkspaceInventory{}, fmt.Errorf("released workspace registry exceeds %d entries", maxInventoryEntries)
	}
	result.Version, result.Containers = raw.Version, len(raw.Containers)
	for _, item := range raw.Workspaces {
		id, workspaceRoot := strings.TrimSpace(item.ID), strings.TrimSpace(item.Path)
		if id == "" || workspaceRoot == "" {
			return WorkspaceInventory{}, errors.New("released workspace registry contains an incomplete workspace")
		}
		result.Workspaces = append(result.Workspaces, WorkspaceItem{
			ID:       id,
			Path:     filepath.Clean(workspaceRoot),
			HasState: dirExists(filepath.Join(root, "workspaces", id)),
		})
	}
	sort.Slice(result.Workspaces, func(i, j int) bool { return result.Workspaces[i].ID < result.Workspaces[j].ID })
	return result, nil
}

func inventoryRoot(root string, instance InstanceInspection) ([]Artifact, string, int, error) {
	artifacts := []Artifact{}
	entries := 0
	var fingerprintBytes int64
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		entries++
		if entries > maxInventoryEntries {
			return fmt.Errorf("released state inventory exceeds %d entries", maxInventoryEntries)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			return nil
		}
		artifact := Artifact{Path: relative}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		artifact.Bytes = info.Size()
		if entry.Type()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			artifact.Classification = ClassUnsupportedFailClosed
			artifact.Kind = "special"
			artifact.Reason = "released state contains a non-regular entry"
			artifacts = append(artifacts, artifact)
			return nil
		}
		artifact.Classification, artifact.Kind, artifact.Reason = classifyArtifact(relative, info.Size())
		if relative == "state/instance.json" && !instance.Valid {
			artifact.Classification = ClassRegenerate
			artifact.Reason = "released instance identity is invalid and will be regenerated"
		}
		if artifact.Classification == ClassDurableMigrate && artifact.Kind == "log" && !validReleasedLog(path) {
			artifact.Classification = ClassOptionalSkipWithReport
			artifact.Reason = "released runtime log is invalid and will be skipped with a report"
		}
		if artifact.Classification == ClassDurableMigrate && artifact.Kind == "tui-state" && !validReleasedTUIState(path) {
			artifact.Classification = ClassOptionalSkipWithReport
			artifact.Reason = "released TUI state is invalid and will be skipped with a report"
		}
		if info.Size() > maxRegularFileBytes && artifact.Classification != ClassOptionalSkipWithReport {
			artifact.Classification = ClassUnsupportedFailClosed
			artifact.Reason = "released state file exceeds migration inspection limit"
			artifacts = append(artifacts, artifact)
			return nil
		}
		if ((artifact.Classification == ClassTransientDrop && artifact.Kind == "runtime-state") ||
			(artifact.Classification == ClassRegenerate && artifact.Kind == "service-environment")) && info.Size() <= maxRegularFileBytes {
			fileHash, _, err := hashFile(path, maxRegularFileBytes)
			if err != nil {
				return err
			}
			artifact.SHA256 = fileHash
		} else if artifact.Classification != ClassTransientDrop && artifact.Classification != ClassRegenerate && info.Size() <= maxRegularFileBytes {
			fileHash, bytesRead, err := hashFile(path, maxRegularFileBytes)
			if err != nil {
				return err
			}
			artifact.SHA256 = fileHash
			fingerprintBytes += bytesRead
			if fingerprintBytes > maxFingerprintBytes {
				return fmt.Errorf("released state fingerprint exceeds %d bytes", maxFingerprintBytes)
			}
			_, _ = io.WriteString(h, relative+"\x00"+fileHash+"\x00"+strconv.FormatInt(info.Size(), 10)+"\n")
		}
		artifacts = append(artifacts, artifact)
		return nil
	})
	if err != nil {
		return nil, "", 0, err
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	unsupported := 0
	for _, item := range artifacts {
		if item.Classification == ClassUnsupportedFailClosed {
			unsupported++
		}
	}
	return artifacts, hex.EncodeToString(h.Sum(nil)), unsupported, nil
}

func validReleasedLog(path string) bool {
	data, err := readBoundedRegular(path, maxLogBytes)
	if err != nil {
		return false
	}
	if strings.EqualFold(filepath.Ext(path), ".jsonl") {
		for _, line := range bytes.Split(data, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) > 0 && !json.Valid(line) {
				return false
			}
		}
		return true
	}
	return utf8.Valid(data) && !bytes.Contains(data, []byte{0})
}

func validReleasedTUIState(path string) bool {
	data, err := readBoundedRegular(path, maxRegularFileBytes)
	if err != nil || !json.Valid(bytes.TrimSpace(data)) {
		return false
	}
	var value struct {
		Version       int      `json:"version"`
		RecentActions []string `json:"recent_actions,omitempty"`
	}
	if err := json.Unmarshal(data, &value); err != nil || value.Version != 1 {
		return false
	}
	for _, action := range value.RecentActions {
		if len(action) > 256 {
			return false
		}
	}
	return true
}

func classifyArtifact(relative string, size int64) (Classification, string, string) {
	clean := strings.TrimPrefix(filepath.ToSlash(relative), "./")
	base := filepath.Base(clean)
	switch {
	case clean == legacyRootMarkerName:
		return ClassRegenerate, "root-marker", "CodeMCP writes its own root marker"
	case clean == ".bundle024-source.json":
		return ClassTransientDrop, "migration-metadata", "migration-owned portable bundle materialization metadata is not activated"
	case clean == ".runtime-control.json" || clean == "state/update.json":
		return ClassTransientDrop, "runtime-state", "ephemeral runtime/update state is not migrated"
	case clean == "runtime/environment.json":
		return ClassRegenerate, "service-environment", "managed service environment is regenerated after cutover"
	case strings.HasPrefix(clean, "runtime/"):
		return ClassTransientDrop, "runtime-state", "runtime process/control state is not durable"
	case strings.HasSuffix(strings.ToLower(base), ".pid") || strings.HasSuffix(strings.ToLower(base), ".lock"):
		return ClassTransientDrop, "runtime-state", "PID and lock state is not durable"
	case clean == "config.json" || clean == "config.yaml" || clean == "config.yml" || clean == "config.toml":
		return ClassDurableMigrate, "config", "validated released configuration"
	case clean == "workspaces.json" || clean == "workspaces.yaml" || clean == "workspaces.yml" || clean == "workspaces.toml":
		return ClassDurableMigrate, "workspace-registry", "validated released workspace registry"
	case clean == "upstream.json" || clean == "oauth.json" || clean == "tunnel.json":
		return ClassDurableMigrate, "domain-store", "released domain state has a canonical migration owner"
	case clean == "state/instance.json":
		return ClassDurableMigrate, "instance-identity", "instance identity is durable migration input"
	case strings.HasPrefix(clean, "state/secrets/"):
		return ClassDurableMigrate, "credential-state", "credential files are handled by the canonical credential transformer"
	case clean == "instructions/global.json":
		return ClassDurableMigrate, "instructions-legacy-global", "legacy global instruction settings are preserved as an inactive archive"
	case strings.HasPrefix(clean, "instructions/"):
		return ClassDurableMigrate, "instructions", "instruction source state is durable"
	case clean == "tui-state.json":
		if size > maxRegularFileBytes {
			return ClassOptionalSkipWithReport, "tui-state", "oversize TUI state may be skipped with a report"
		}
		return ClassDurableMigrate, "tui-state", "supported TUI preferences are durable"
	case strings.HasPrefix(clean, "logs/"):
		if size <= maxLogBytes && (strings.HasSuffix(clean, ".jsonl") || strings.HasSuffix(clean, ".log")) {
			return ClassDurableMigrate, "log", "bounded released runtime log is eligible for migration"
		}
		return ClassOptionalSkipWithReport, "log", "unsupported or oversize runtime log is preserved and reported"
	case strings.HasPrefix(clean, "workspaces/"):
		parts := strings.Split(clean, "/")
		if len(parts) >= 3 && (parts[2] == "MEMORY.md" || parts[2] == "shell.json" || parts[2] == "checkpoints") {
			kind := "workspace-state"
			if parts[2] == "checkpoints" {
				kind = "checkpoint"
			}
			return ClassDurableMigrate, kind, "workspace-owned durable state is staged through its canonical transformer"
		}
		return ClassUnsupportedFailClosed, "workspace-state", "unknown released workspace state entry"
	default:
		return ClassUnsupportedFailClosed, "unknown", "unknown released state is preserved and blocks automatic cutover"
	}
}

func inspectLaunchers(options Options, descriptor SourceDescriptor) ([]Launcher, error) {
	currentLayout, err := install.DefaultLayout()
	if err != nil {
		return nil, err
	}
	findInstallations := options.FindInstallations
	if findInstallations == nil {
		findInstallations = install.FindLegacyInstallations
	}
	inspectInstallation := options.InspectInstallation
	if inspectInstallation == nil {
		inspectInstallation = install.InspectLegacyInstallation
	}
	findAliases := options.FindAliases
	if findAliases == nil {
		findAliases = install.FindLegacyAliases
	}
	inspectAlias := options.InspectAlias
	if inspectAlias == nil {
		inspectAlias = install.InspectLegacyAlias
	}
	items := []install.LegacyInstallation{}
	installRoots := []string{descriptor.PlatformDefaultInstallRoot}
	if hinted := strings.TrimSpace(descriptor.EnvironmentHints["CHATGPT_MCP_INSTALL_DIR"]); hinted != "" &&
		comparablePath(hinted) != comparablePath(descriptor.PlatformDefaultInstallRoot) {
		installRoots = append(installRoots, hinted)
	}
	defaultBinary := filepath.Join(descriptor.PlatformDefaultInstallRoot, "current", descriptor.BinaryName)
	for _, installRoot := range installRoots {
		binary := filepath.Join(installRoot, "current", descriptor.BinaryName)
		if info, statErr := os.Lstat(binary); statErr == nil && !info.IsDir() {
			legacyLayout, layoutErr := install.NewLayout(installRoot, descriptor.PlatformDefaultBinDir)
			if layoutErr != nil {
				return nil, layoutErr
			}
			item, err := inspectInstallation(legacyLayout, binary, binary)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return nil, statErr
		}
	}
	discovered, err := findInstallations(currentLayout, defaultBinary)
	if err != nil {
		return nil, err
	}
	items = append(items, discovered...)
	launchers := []Launcher{}
	seen := map[string]bool{}
	for _, item := range items {
		key := comparablePath(item.Path)
		if seen[key] {
			continue
		}
		seen[key] = true
		launchers = append(launchers, Launcher{
			Path: item.Path, Target: item.Target, Kind: "executable", Method: item.Method,
			Ownership:      launcherOwnership(item.Verified, item.PackageManaged),
			PackageManaged: item.PackageManaged, Removable: item.Removable, Reason: item.Reason,
		})
		if len(launchers) > maxLauncherEntries {
			return nil, fmt.Errorf("released launcher inventory exceeds %d entries", maxLauncherEntries)
		}
	}
	aliases, err := findAliases()
	if err != nil {
		return nil, err
	}
	binDirs := []string{descriptor.PlatformDefaultBinDir}
	if hinted := strings.TrimSpace(descriptor.EnvironmentHints["CHATGPT_MCP_BIN_DIR"]); hinted != "" &&
		comparablePath(hinted) != comparablePath(descriptor.PlatformDefaultBinDir) {
		binDirs = append(binDirs, hinted)
	}
	for _, binDir := range binDirs {
		aliasPath := filepath.Join(binDir, descriptor.AliasName)
		if _, statErr := os.Lstat(aliasPath); statErr == nil {
			alias, err := inspectAlias(aliasPath)
			if err != nil {
				return nil, err
			}
			aliases = append(aliases, alias)
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return nil, statErr
		}
	}
	for _, item := range aliases {
		key := comparablePath(item.Path)
		if seen[key] {
			continue
		}
		seen[key] = true
		launchers = append(launchers, Launcher{
			Path: item.Path, Target: item.Target, Kind: "alias",
			Ownership:      launcherOwnership(item.Verified, item.PackageManaged),
			PackageManaged: item.PackageManaged, Removable: item.Removable, Reason: item.Reason,
		})
		if len(launchers) > maxLauncherEntries {
			return nil, fmt.Errorf("released launcher inventory exceeds %d entries", maxLauncherEntries)
		}
	}
	sort.Slice(launchers, func(i, j int) bool { return launchers[i].Path < launchers[j].Path })
	return launchers, nil
}

func launcherOwnership(verified, packageManaged bool) Ownership {
	if packageManaged {
		return OwnershipPackageManager
	}
	if verified {
		return OwnershipVerified
	}
	return OwnershipAmbiguous
}

func inspectBundles(paths []string) ([]BundleInspection, error) {
	if len(paths) > maxBundleInputs {
		return nil, fmt.Errorf("released portable bundle inventory exceeds %d inputs", maxBundleInputs)
	}
	result := make([]BundleInspection, 0, len(paths))
	seen := map[string]bool{}
	for _, value := range paths {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		absolute, err := filepath.Abs(value)
		if err != nil {
			return nil, err
		}
		absolute = filepath.Clean(absolute)
		key := comparablePath(absolute)
		if seen[key] {
			continue
		}
		seen[key] = true
		inspection, err := bundle024.Inspect(absolute)
		if err != nil {
			return nil, fmt.Errorf("inspect released portable config bundle %s: %w", absolute, err)
		}
		sha, size, err := hashFile(absolute, 256<<20)
		if err != nil {
			return nil, err
		}
		result = append(result, BundleInspection{Path: absolute, SHA256: sha, Bytes: size, Inspection: inspection})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

func discoverStructured(root, stem string) (string, migrationformat.Format, bool, error) {
	type match struct {
		path   string
		format migrationformat.Format
	}
	matches := []match{}
	for _, ext := range []string{".json", ".yaml", ".yml", ".toml"} {
		path := filepath.Join(root, stem+ext)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", "", false, err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", "", false, fmt.Errorf("released %s state must be a regular non-symlink file", stem)
		}
		format, err := migrationformat.Detect(path)
		if err != nil {
			return "", "", false, err
		}
		matches = append(matches, match{path: path, format: format})
	}
	if len(matches) == 0 {
		return "", "", false, nil
	}
	if len(matches) > 1 {
		return "", "", false, fmt.Errorf("released %s state is ambiguous across multiple structured files", stem)
	}
	return matches[0].path, matches[0].format, true, nil
}

func historicalServiceID(configRoot, scope string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(configRoot)))
	return "chatgpt-mcp-" + scope + "-" + hex.EncodeToString(sum[:6])
}

func hashFile(path string, limit int64) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	read, err := io.Copy(hash, io.LimitReader(file, limit+1))
	if err != nil {
		return "", read, err
	}
	if read > limit {
		return "", read, fmt.Errorf("released state file exceeds fingerprint limit: %s", path)
	}
	return hex.EncodeToString(hash.Sum(nil)), read, nil
}

func readBoundedRegular(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("released state is not a regular non-symlink file: %s", path)
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("released state file exceeds inspection limit: %s", path)
	}
	return os.ReadFile(path)
}

func normalizeServices(values []ServiceState) []ServiceState {
	result := append([]ServiceState(nil), values...)
	for index := range result {
		if result[index].Ownership == "" {
			if result[index].Installed {
				result[index].Ownership = OwnershipAmbiguous
			} else {
				result[index].Ownership = OwnershipAbsent
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Scope == result[j].Scope {
			return result[i].ID < result[j].ID
		}
		return result[i].Scope < result[j].Scope
	})
	return result
}

func normalizeRoot(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("released source root is required")
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func comparablePath(value string) string {
	absolute, err := filepath.Abs(filepath.Clean(value))
	if err != nil {
		return filepath.Clean(value)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
		absolute = resolved
	}
	absolute = filepath.Clean(absolute)
	if runtime.GOOS == "windows" {
		absolute = strings.ToLower(absolute)
	}
	return absolute
}

func dirExists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}
