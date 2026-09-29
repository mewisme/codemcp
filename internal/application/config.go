package application

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"go.mewis.me/codemcp/internal/auth"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configbundle"
	"go.mewis.me/codemcp/internal/configformat"
	mcpoauth "go.mewis.me/codemcp/internal/oauth"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	"go.mewis.me/codemcp/internal/secretinventory"
	"go.mewis.me/codemcp/internal/secretstore"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/upstream"
)

var (
	ErrConfigurationExists          = errors.New("configuration already exists; use --force to rotate tokens")
	ErrRuntimeImportActive          = errors.New("runtime is running; stop it before importing configuration")
	ErrConfigMutationRolledBack     = errors.New("persisted configuration rolled back")
	ErrConfigReconciliationRequired = errors.New("manual reconciliation required")
)

type ConfigOverview struct {
	Config         config.Config
	Source         configformat.Source
	Root           string
	RuntimeRunning bool
	RuntimeSync    ConfigRuntimeSync
}

type ConfigRuntimeSyncState string

const (
	ConfigRuntimeStopped     ConfigRuntimeSyncState = "stopped"
	ConfigRuntimeCurrent     ConfigRuntimeSyncState = "current"
	ConfigRuntimePending     ConfigRuntimeSyncState = "changes pending"
	ConfigRuntimeUnavailable ConfigRuntimeSyncState = "unavailable"
)

type ConfigRuntimeSync struct {
	State                ConfigRuntimeSyncState
	PersistedFingerprint string
	RuntimeFingerprint   string
	Error                string
}

type ConfigMutationResult struct {
	Config          config.Config
	RuntimeReloaded bool
}

type configReloadResult = runtimecontrol.ReloadResult

type InitOptions struct {
	Context context.Context
	Force   bool
}

type InitResult struct {
	Config     config.Config
	ConfigPath string
	Format     configformat.Format
	MCPToken   string
	AdminToken string
}

func Initialize(options InitOptions) (result InitResult, resultErr error) {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	span := tracepkg.Start(ctx, "CONFIG", "config.initialize", "Initializing configuration", tracepkg.Bool("force", options.Force))
	defer func() {
		if resultErr != nil {
			span.FailMessage("Configuration initialization failed", resultErr)
			return
		}
		span.EndMessage("Configuration initialized", tracepkg.String("path", result.ConfigPath), tracepkg.String("format", string(result.Format)), tracepkg.Bool("mcp_token_generated", result.MCPToken != ""), tracepkg.Bool("admin_token_generated", result.AdminToken != ""))
	}()
	sourceSpan := tracepkg.Start(ctx, "CONFIG", "config.source.resolve", "Resolving configuration source", tracepkg.String("root", config.RootPath()))
	source, err := config.Source()
	if err != nil {
		sourceSpan.FailMessage("Configuration source resolution failed", err)
		return InitResult{}, err
	}
	sourceSpan.EndMessage("Configuration source resolved", tracepkg.String("path", source.Path), tracepkg.String("format", string(source.Format)), tracepkg.Bool("exists", source.Exists))
	if source.Exists && !options.Force {
		return InitResult{}, ErrConfigurationExists
	}
	cfg := config.Default()
	if source.Exists {
		cfg, err = config.Load()
		if err != nil {
			return InitResult{}, fmt.Errorf("load existing configuration: %w", err)
		}
	}
	tokenSpan := tracepkg.Start(ctx, "AUTH", "auth.tokens.generate", "Generating initial authentication tokens", tracepkg.String("kinds", "mcp,admin"))
	mcpToken := auth.GenerateToken("mcp")
	adminToken := auth.GenerateToken("admin")
	tokenSpan.EndMessage("Initial authentication tokens generated", tracepkg.Bool("mcp_generated", true), tracepkg.Bool("admin_generated", true))
	hashSpan := tracepkg.Start(ctx, "AUTH", "auth.tokens.hash", "Hashing initial authentication tokens", tracepkg.Int("count", 2))
	cfg.Auth.MCPTokenHash = auth.HashToken(mcpToken)
	cfg.Auth.AdminTokenHash = auth.HashToken(adminToken)
	hashSpan.EndMessage("Initial authentication tokens hashed", tracepkg.Int("count", 2))
	validateSpan := tracepkg.Start(ctx, "CONFIG", "config.validate", "Validating initial configuration")
	if err := config.Validate(cfg); err != nil {
		validateSpan.FailMessage("Initial configuration validation failed", err)
		return InitResult{}, err
	}
	validateSpan.EndMessage("Initial configuration validated")
	rollbackSecrets, err := replaceAuthSecrets(config.RootPath(), map[string]string{"mcp": mcpToken, "admin": adminToken})
	if err != nil {
		return InitResult{}, fmt.Errorf("store initial authentication tokens: %w", err)
	}
	path := source.Path
	persistSpan := tracepkg.Start(ctx, "CONFIG", "config.persist", "Persisting initial configuration", tracepkg.String("path", path), tracepkg.String("format", string(configformat.JSON)), tracepkg.Bool("replace", source.Exists), tracepkg.Bool("atomic", true))
	if err := config.Save(cfg); err != nil {
		err = errors.Join(err, rollbackSecrets())
		persistSpan.FailMessage("Initial configuration persistence failed", err)
		return InitResult{}, err
	}
	path = config.Path()
	persistFields := []tracepkg.Field{tracepkg.String("path", path), tracepkg.String("format", string(configformat.JSON))}
	if info, statErr := os.Stat(path); statErr == nil {
		persistFields = append(persistFields, tracepkg.Int64("bytes", info.Size()))
	}
	persistSpan.EndMessage("Initial configuration persisted", persistFields...)
	result = InitResult{Config: cfg, ConfigPath: path, Format: configformat.JSON, MCPToken: mcpToken, AdminToken: adminToken}
	return result, nil
}

func Uninitialize(root string) error {
	return UninitializeContext(context.Background(), root)
}

func UninitializeContext(ctx context.Context, root string) error {
	span := tracepkg.Start(ctx, "CONFIG", "config.uninitialize", "Removing local configuration and state", tracepkg.String("root", root))
	if err := PurgeStoredSecretsContext(ctx, root); err != nil {
		span.FailMessage("Local configuration secret purge failed", err, tracepkg.String("root", root))
		return err
	}
	if err := RemoveConfigRootContext(ctx, root); err != nil {
		span.FailMessage("Local configuration root removal failed", err, tracepkg.String("root", root))
		return err
	}
	span.EndMessage("Local configuration and state removed", tracepkg.String("root", root))
	return nil
}

func PurgeStoredSecrets(root string) error {
	return PurgeStoredSecretsContext(context.Background(), root)
}

func PurgeStoredSecretsContext(ctx context.Context, root string) error {
	span := tracepkg.Start(ctx, "CONFIG", "config.secrets.purge", "Purging stored configuration secrets", tracepkg.String("root", root))
	inventory, err := secretinventory.Inventory(root)
	if err != nil {
		span.FailMessage("Managed secret inventory failed", err)
		return err
	}
	entries := secretinventory.Names(inventory)
	changes := make([]secretstore.Change, 0, len(entries))
	for _, entry := range entries {
		changes = append(changes, secretstore.Change{Name: entry})
	}
	if err := secretstore.New(root).Apply(changes); err != nil {
		span.FailMessage("Stored configuration secret purge failed", err, tracepkg.Int("secret_count", len(entries)))
		return err
	}
	span.EndMessage("Stored configuration secrets purged", tracepkg.Int("secret_count", len(entries)))
	return nil
}

func RemoveConfigRoot(root string) error {
	return RemoveConfigRootContext(context.Background(), root)
}

func RemoveConfigRootContext(ctx context.Context, root string) error {
	clean := filepath.Clean(root)
	span := tracepkg.Start(ctx, "CONFIG", "config.root.remove", "Removing configuration root", tracepkg.String("path", clean))
	if clean == "." || clean == string(filepath.Separator) {
		err := fmt.Errorf("refusing to remove unsafe config root: %s", clean)
		span.FailMessage("Configuration root removal refused", err, tracepkg.String("path", clean))
		return err
	}
	volume := filepath.VolumeName(clean)
	if clean == volume+string(filepath.Separator) {
		err := fmt.Errorf("refusing to remove volume root: %s", clean)
		span.FailMessage("Configuration root removal refused", err, tracepkg.String("path", clean))
		return err
	}
	if clean != filepath.Clean(configformat.DefaultRootPath()) && !configformat.IsManagedRoot(clean) {
		err := fmt.Errorf("refusing to remove unmanaged config root: %s", clean)
		span.FailMessage("Configuration root removal refused", err, tracepkg.String("path", clean))
		return err
	}
	if err := removeOwnedConfigRootEntries(clean); err != nil {
		span.FailMessage("Configuration root removal failed", err, tracepkg.String("path", clean))
		return err
	}
	if err := configformat.RemoveRootMarker(clean); err != nil {
		span.FailMessage("Configuration root marker removal failed", err, tracepkg.String("path", clean))
		return err
	}
	entries, err := os.ReadDir(clean)
	if err != nil && !os.IsNotExist(err) {
		span.FailMessage("Configuration root inspection failed", err, tracepkg.String("path", clean))
		return err
	}
	if err == nil && len(entries) == 0 {
		if err := os.Remove(clean); err != nil && !os.IsNotExist(err) {
			span.FailMessage("Configuration root removal failed", err, tracepkg.String("path", clean))
			return err
		}
	}
	span.EndMessage("Configuration root removed", tracepkg.String("path", clean))
	return nil
}

func removeOwnedConfigRootEntries(root string) error {
	for _, name := range []string{"state", "logs", "instructions", "workspaces", "tunnels"} {
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			return err
		}
	}
	for _, name := range []string{"tui-state.json"} {
		if err := removeIfExists(filepath.Join(root, name)); err != nil {
			return err
		}
	}
	for _, stem := range []string{"config", "tunnel", "workspaces", "upstream", "upstreams", "oauth"} {
		for _, extension := range []string{".json", ".yaml", ".yml", ".toml"} {
			if err := removeIfExists(filepath.Join(root, stem+extension)); err != nil {
				return err
			}
		}
	}
	return nil
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func MigrateLegacySecrets() error {
	return MigrateLegacySecretsContext(context.Background())
}

func MigrateLegacySecretsContext(ctx context.Context) error {
	span := tracepkg.Start(ctx, "CONFIG", "config.secrets.migrate", "Migrating legacy stored secrets")
	if _, err := LoadConfig(ctx); err != nil {
		span.FailMessage("Configuration secret migration failed", err, tracepkg.String("stage", "config"))
		return err
	}
	upstreamStore := upstream.NewStore(upstream.Path())
	if _, err := upstreamStore.Load(); err != nil {
		span.FailMessage("Configuration secret migration failed", err, tracepkg.String("stage", "upstream"), tracepkg.String("path", upstream.Path()))
		return err
	}
	if err := mcpoauth.NewStore(mcpoauth.Path()).Migrate(); err != nil {
		span.FailMessage("Configuration secret migration failed", err, tracepkg.String("stage", "oauth"), tracepkg.String("path", mcpoauth.Path()))
		return err
	}
	span.EndMessage("Legacy stored secrets migrated", tracepkg.String("upstream_path", upstream.Path()), tracepkg.String("oauth_path", mcpoauth.Path()))
	return nil
}

func MigrateSecretEnvelopes() (int, error) {
	return MigrateSecretEnvelopesContext(context.Background())
}

func MigrateSecretEnvelopesContext(ctx context.Context) (int, error) {
	root := config.RootPath()
	span := tracepkg.Start(ctx, "CONFIG", "config.secrets.envelope.migrate", "Migrating legacy secret files to encrypted JSON envelopes", tracepkg.String("root", root))
	migrated, err := secretstore.New(root).MigrateLegacyFiles()
	if err != nil {
		span.FailMessage("Secret envelope migration failed", err, tracepkg.String("root", root))
		return 0, err
	}
	span.EndMessage("Legacy secret files migrated to encrypted JSON envelopes", tracepkg.String("root", root), tracepkg.Int("migrated", migrated))
	return migrated, nil
}

func LoadConfigOverview(ctx context.Context) (ConfigOverview, error) {
	source, err := config.Source()
	if err != nil {
		return ConfigOverview{}, err
	}
	cfg, err := config.Load()
	if err != nil {
		return ConfigOverview{}, err
	}
	persistedFingerprint, err := config.RuntimeFingerprint(cfg)
	if err != nil {
		return ConfigOverview{}, err
	}
	status, running, statusErr := RuntimeStatus(ctx)
	sync := ConfigRuntimeSync{State: ConfigRuntimeStopped, PersistedFingerprint: persistedFingerprint}
	if statusErr != nil {
		sync.State, sync.Error = ConfigRuntimeUnavailable, statusErr.Error()
	} else if running {
		sync.RuntimeFingerprint = strings.TrimSpace(status.ConfigFingerprint)
		switch {
		case sync.RuntimeFingerprint == "":
			sync.State = ConfigRuntimeUnavailable
			sync.Error = "running runtime does not expose configuration state"
		case sync.RuntimeFingerprint == sync.PersistedFingerprint:
			sync.State = ConfigRuntimeCurrent
		default:
			sync.State = ConfigRuntimePending
		}
	}
	return ConfigOverview{Config: cfg, Source: source, Root: config.RootPath(), RuntimeRunning: running, RuntimeSync: sync}, nil
}

func SetConfigField(ctx context.Context, key, raw string) (ConfigMutationResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	mutationSpan := tracepkg.Start(ctx, "CONFIG", "config.field.mutate", "Mutating configuration field", tracepkg.String("key", key))
	applied, err := NewSettingService().Apply(ctx, []SettingChange{{Key: key, Value: raw}})
	if err != nil {
		mutationSpan.FailMessage("Configuration field mutation failed", err, tracepkg.String("key", key))
		return ConfigMutationResult{}, err
	}
	mutationSpan.EndMessage("Configuration field mutated", tracepkg.String("key", key), tracepkg.Bool("runtime_reloaded", applied.RuntimeReloaded))
	return ConfigMutationResult{Config: applied.Config, RuntimeReloaded: applied.RuntimeReloaded}, nil
}

func VerifyConfig() (config.VerifyResult, error) { return VerifyConfigContext(context.Background()) }

func VerifyConfigContext(ctx context.Context) (config.VerifyResult, error) {
	span := tracepkg.Start(ctx, "CONFIG", "config.verify", "Verifying configuration and state", tracepkg.String("root", config.RootPath()))
	result, err := config.Verify()
	if err != nil {
		span.FailMessage("Configuration verification failed", err, tracepkg.String("root", config.RootPath()))
		return config.VerifyResult{}, err
	}
	span.EndMessage("Configuration and state verified", tracepkg.String("root", config.RootPath()), tracepkg.String("format", string(result.Format)), tracepkg.Int("files", result.Files))
	return result, nil
}

func ExportConfig(destination string, force bool) (configbundle.ExportResult, error) {
	return ExportConfigContext(context.Background(), destination, force)
}

func ExportConfigContext(ctx context.Context, destination string, force bool) (configbundle.ExportResult, error) {
	span := tracepkg.Start(ctx, "CONFIG", "config.export", "Exporting portable configuration envelope", tracepkg.String("source_root", config.RootPath()), tracepkg.String("destination", destination), tracepkg.Bool("force", force))
	result, err := configbundle.Export(config.RootPath(), destination, configbundle.ExportOptions{Force: force})
	if err != nil {
		span.FailMessage("Configuration envelope export failed", err, tracepkg.String("destination", destination))
		return configbundle.ExportResult{}, err
	}
	span.EndMessage("Configuration envelope exported", tracepkg.String("destination", result.Path), tracepkg.Int("files", result.Files), tracepkg.Int("skipped_files", result.SkippedFiles), tracepkg.String("source_platform", result.Source.OS+"/"+result.Source.Arch))
	return result, nil
}

func ImportConfig(ctx context.Context, source string, force bool) (configbundle.ImportResult, error) {
	span := tracepkg.Start(ctx, "CONFIG", "config.import", "Importing portable configuration envelope", tracepkg.String("source", source), tracepkg.String("destination_root", config.RootPath()), tracepkg.Bool("force", force))
	running, err := RuntimeRunning(ctx)
	if err != nil {
		span.FailMessage("Configuration envelope import runtime check failed", err)
		return configbundle.ImportResult{}, err
	}
	if running {
		err := ErrRuntimeImportActive
		span.FailMessage("Configuration envelope import refused", err, tracepkg.Bool("runtime_running", true))
		return configbundle.ImportResult{}, err
	}
	result, err := configbundle.Import(config.RootPath(), source, configbundle.ImportOptions{Force: force})
	if err != nil {
		span.FailMessage("Configuration envelope import failed", err, tracepkg.String("source", source))
		return configbundle.ImportResult{}, err
	}
	span.EndMessage("Configuration envelope imported", tracepkg.String("source", source), tracepkg.String("destination_root", config.RootPath()), tracepkg.Int("files", result.Files), tracepkg.Int("skipped_paths", result.SkippedPaths), tracepkg.Int("skipped_files", result.SkippedFiles), tracepkg.Bool("backup_created", result.BackupPath != ""), tracepkg.String("source_platform", result.Source.OS+"/"+result.Source.Arch), tracepkg.String("target_platform", result.Target.OS+"/"+result.Target.Arch))
	return result, nil
}

func reloadConfig(ctx context.Context) (configReloadResult, error) {
	var result configReloadResult
	state, err := runtimecontrol.Request(ctx, http.MethodPost, "/reload", nil, &result)
	if err != nil {
		return configReloadResult{}, err
	}
	if err := runtimecontrol.ValidatePID(ctx, state.PID, result.PID, "reload"); err != nil {
		return configReloadResult{}, err
	}
	return result, nil
}

func reloadPersistedConfigIfRunning(ctx context.Context) (configReloadResult, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	span := tracepkg.Start(ctx, "CONFIG", "config.runtime.reload", "Reloading persisted configuration into runtime")
	result, err := reloadConfig(ctx)
	if err != nil {
		if runtimecontrol.IsUnavailable(err) {
			span.EndMessage("Runtime reload skipped", tracepkg.Bool("runtime_running", false), tracepkg.Bool("runtime_reloaded", false))
			return configReloadResult{}, false, nil
		}
		span.FailMessage("Runtime configuration reload failed", err)
		return configReloadResult{}, false, err
	}
	span.EndMessage("Runtime configuration reloaded", tracepkg.Bool("runtime_running", true), tracepkg.Bool("runtime_reloaded", true), tracepkg.Int("pid", result.PID), tracepkg.Bool("network_restarted", result.NetworkRestarted), tracepkg.Bool("server_enabled", result.ServerEnabled), tracepkg.Int("server_port", result.ServerPort), tracepkg.Bool("admin_enabled", result.AdminEnabled), tracepkg.Int("admin_port", result.AdminPort), tracepkg.String("exposure", string(result.Exposure)))
	return result, true, nil
}

func saveConfigMutation(ctx context.Context, previous, next config.Config) (configReloadResult, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	source, _ := config.Source()
	persistSpan := tracepkg.Start(ctx, "CONFIG", "config.persist", "Persisting configuration", tracepkg.String("path", source.Path), tracepkg.String("format", string(source.Format)), tracepkg.Bool("atomic", true))
	if err := config.Save(next); err != nil {
		persistSpan.FailMessage("Configuration persistence failed", err)
		return configReloadResult{}, false, err
	}
	persistFields := []tracepkg.Field{tracepkg.String("path", source.Path), tracepkg.String("format", string(source.Format))}
	if info, statErr := os.Stat(source.Path); statErr == nil {
		persistFields = append(persistFields, tracepkg.Int64("bytes", info.Size()))
	}
	if fingerprint, fingerprintErr := config.RuntimeFingerprint(next); fingerprintErr == nil {
		persistFields = append(persistFields, tracepkg.String("fingerprint", fingerprint))
	}
	persistSpan.EndMessage("Configuration persisted", persistFields...)
	result, reloaded, err := reloadPersistedConfigIfRunning(ctx)
	if err == nil {
		return result, reloaded, nil
	}
	rollbackSpan := tracepkg.Start(ctx, "CONFIG", "config.persist.rollback", "Rolling back persisted configuration", tracepkg.String("path", source.Path), tracepkg.String("format", string(source.Format)), tracepkg.Bool("atomic", true))
	rollbackErr := config.Save(previous)
	if rollbackErr != nil {
		rollbackSpan.FailMessage("Persisted configuration rollback failed", rollbackErr)
		return configReloadResult{}, false, errors.Join(
			fmt.Errorf("reload running configuration: %w", err),
			fmt.Errorf("rollback persisted configuration: %w", rollbackErr),
			ErrConfigReconciliationRequired,
		)
	}
	rollbackFields := []tracepkg.Field{tracepkg.String("path", source.Path)}
	if info, statErr := os.Stat(source.Path); statErr == nil {
		rollbackFields = append(rollbackFields, tracepkg.Int64("bytes", info.Size()))
	}
	rollbackSpan.EndMessage("Persisted configuration rolled back", rollbackFields...)
	return configReloadResult{}, false, errors.Join(
		fmt.Errorf("reload running configuration: %w", err),
		ErrConfigMutationRolledBack,
	)
}

func saveConfigMutationWithoutRollback(ctx context.Context, next config.Config) (configReloadResult, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	source, _ := config.Source()
	persistSpan := tracepkg.Start(ctx, "CONFIG", "config.persist", "Persisting configuration", tracepkg.String("path", source.Path), tracepkg.String("format", string(source.Format)), tracepkg.Bool("atomic", true), tracepkg.Bool("rollback", false))
	if err := config.Save(next); err != nil {
		persistSpan.FailMessage("Configuration persistence failed", err)
		return configReloadResult{}, false, err
	}
	persistFields := []tracepkg.Field{tracepkg.String("path", source.Path), tracepkg.String("format", string(source.Format))}
	if info, statErr := os.Stat(source.Path); statErr == nil {
		persistFields = append(persistFields, tracepkg.Int64("bytes", info.Size()))
	}
	persistSpan.EndMessage("Configuration persisted", persistFields...)
	result, reloaded, err := reloadPersistedConfigIfRunning(ctx)
	if err != nil {
		return configReloadResult{}, false, fmt.Errorf("reload running configuration: %w", err)
	}
	return result, reloaded, nil
}

func loadConfigTraced(ctx context.Context, name, message string) (config.Config, configformat.Source, error) {
	return loadConfigWithTracedLoader(ctx, name, message, config.Load)
}

func LoadConfig(ctx context.Context) (config.Config, error) {
	cfg, _, err := loadConfigTraced(ctx, "config.load", "Loading configuration")
	return cfg, err
}

func ConfigSource(ctx context.Context) (configformat.Source, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	span := tracepkg.Start(ctx, "CONFIG", "config.source.inspect", "Inspecting active configuration source", tracepkg.String("root", config.RootPath()))
	source, err := config.Source()
	if err != nil {
		span.FailMessage("Active configuration source inspection failed", err, tracepkg.String("root", config.RootPath()))
		return configformat.Source{}, err
	}
	fields := []tracepkg.Field{tracepkg.String("root", config.RootPath()), tracepkg.String("path", source.Path), tracepkg.String("format", string(source.Format)), tracepkg.Bool("exists", source.Exists)}
	if info, statErr := os.Stat(source.Path); statErr == nil {
		fields = append(fields, tracepkg.Int64("bytes", info.Size()))
	}
	span.EndMessage("Active configuration source inspected", fields...)
	return source, nil
}

func loadConfigWithTracedLoader(ctx context.Context, name, message string, load func() (config.Config, error)) (config.Config, configformat.Source, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if load == nil {
		load = config.Load
	}
	sourceSpan := tracepkg.Start(ctx, "CONFIG", "config.source.resolve", "Resolving configuration source", tracepkg.String("root", config.RootPath()))
	source, err := config.Source()
	if err != nil {
		sourceSpan.FailMessage("Configuration source resolution failed", err)
		return config.Config{}, configformat.Source{}, err
	}
	sourceSpan.EndMessage("Configuration source resolved", tracepkg.String("path", source.Path), tracepkg.String("format", string(source.Format)), tracepkg.Bool("exists", source.Exists))
	span := tracepkg.Start(ctx, "CONFIG", name, message, tracepkg.String("path", source.Path), tracepkg.String("format", string(source.Format)), tracepkg.Bool("exists", source.Exists))
	cfg, err := load()
	if err != nil {
		span.FailMessage(message+" failed", err)
		return config.Config{}, source, err
	}
	fields := []tracepkg.Field{tracepkg.String("path", source.Path), tracepkg.String("format", string(source.Format))}
	if info, statErr := os.Stat(source.Path); statErr == nil {
		fields = append(fields, tracepkg.Int64("bytes", info.Size()))
	}
	span.EndMessage(message+" completed", fields...)
	return cfg, source, nil
}

func ReloadWorkspaces(ctx context.Context) (runtimecontrol.WorkspaceReloadResult, bool, error) {
	var result runtimecontrol.WorkspaceReloadResult
	state, err := runtimecontrol.Request(ctx, http.MethodPost, "/workspaces/reload", nil, &result)
	if err != nil {
		if runtimecontrol.IsUnavailable(err) {
			return runtimecontrol.WorkspaceReloadResult{}, false, nil
		}
		return runtimecontrol.WorkspaceReloadResult{}, true, err
	}
	if err := runtimecontrol.ValidatePID(ctx, state.PID, result.PID, "workspaces-reload"); err != nil {
		return runtimecontrol.WorkspaceReloadResult{}, true, err
	}
	return result, true, nil
}

func RuntimeRunning(ctx context.Context) (bool, error) {
	var status struct {
		PID int `json:"pid"`
	}
	state, err := runtimecontrol.Request(ctx, http.MethodGet, "/status", nil, &status)
	if err != nil {
		if runtimecontrol.IsUnavailable(err) {
			return false, nil
		}
		return false, err
	}
	if err := runtimecontrol.ValidatePID(ctx, state.PID, status.PID, "status"); err != nil {
		return false, err
	}
	return true, nil
}

func ConfigOperationNotice(reloaded bool) string {
	if reloaded {
		return "Saved and applied to the running runtime."
	}
	return "Saved. The next runtime start will use this configuration."
}

func ConfigFieldGuidance(key string) string {
	spec, ok := config.FieldByKey(strings.TrimSpace(key))
	if !ok {
		return ""
	}
	return spec.Guidance
}
