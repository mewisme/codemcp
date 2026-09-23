package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

var ErrDevelopmentBuild = errors.New("development build cannot be installed as a release without --force")

type Options struct {
	Context       context.Context
	Layout        Layout
	Version       string
	Source        string
	NoAlias       bool
	Force         bool
	MigrateLegacy bool
}

type Result struct {
	Layout           Layout
	Version          string
	Source           string
	Staged           Staged
	Activation       Activation
	PreviousMetadata *Metadata
	Canonical        CanonicalStatus
	Alias            AliasStatus
	AliasInstalled   bool
	AlreadyInstalled bool
	Legacy           LegacyCleanupResult
}

func Install(options Options) (result Result, resultErr error) {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	version := normalizeInstallVersion(options.Version)
	installSpan := tracepkg.Start(ctx, "INSTALL", "install.apply", "Applying managed installation", tracepkg.String("version", version), tracepkg.Bool("no_alias", options.NoAlias), tracepkg.Bool("force", options.Force), tracepkg.Bool("migrate_legacy", options.MigrateLegacy))
	defer func() {
		if resultErr != nil {
			installSpan.FailMessage("Managed installation failed", resultErr)
			return
		}
		installSpan.EndMessage("Managed installation applied", tracepkg.String("version", result.Version), tracepkg.String("binary", result.Staged.Binary), tracepkg.String("canonical", result.Canonical.Path), tracepkg.Bool("alias_installed", result.AliasInstalled), tracepkg.Bool("already_installed", result.AlreadyInstalled))
	}()
	if version == "" {
		return Result{}, errors.New("install version is required")
	}
	development := isDevelopmentVersion(version)
	tracepkg.Emit(ctx, "INSTALL", "install.development-policy", "Resolved development-build installation policy", tracepkg.String("version", version), tracepkg.Bool("development_build", development), tracepkg.Bool("force", options.Force), tracepkg.Bool("allowed", !development || options.Force))
	if development && !options.Force {
		return Result{}, ErrDevelopmentBuild
	}
	layout := options.Layout
	if strings.TrimSpace(layout.Root) == "" {
		span := tracepkg.Start(ctx, "INSTALL", "install.layout.resolve", "Resolving installation layout")
		var err error
		layout, err = DefaultLayout()
		if err != nil {
			span.FailMessage("Installation layout resolution failed", err)
			return Result{}, err
		}
		span.EndMessage("Installation layout resolved", tracepkg.String("root", layout.Root), tracepkg.String("bin_dir", layout.BinDir), tracepkg.String("versions", layout.Versions), tracepkg.String("metadata", layout.Metadata))
	} else {
		tracepkg.Emit(ctx, "INSTALL", "install.layout.selected", "Installation layout selected", tracepkg.String("root", layout.Root), tracepkg.String("bin_dir", layout.BinDir), tracepkg.String("versions", layout.Versions), tracepkg.String("metadata", layout.Metadata))
	}
	source := strings.TrimSpace(options.Source)
	if source == "" {
		span := tracepkg.Start(ctx, "INSTALL", "install.source.resolve", "Resolving current executable")
		var err error
		source, err = os.Executable()
		if err != nil {
			span.FailMessage("Current executable resolution failed", err)
			return Result{}, err
		}
		span.EndMessage("Current executable resolved", tracepkg.String("source", source))
	}
	resolveSpan := tracepkg.Start(ctx, "INSTALL", "install.source.validate", "Validating source binary", tracepkg.String("source", source))
	resolvedSource, err := resolveSourceBinary(source)
	if err != nil {
		resolveSpan.FailMessage("Source binary validation failed", err)
		return Result{}, err
	}
	resolveFields := []tracepkg.Field{tracepkg.String("source", resolvedSource)}
	if info, statErr := os.Stat(resolvedSource); statErr == nil {
		resolveFields = append(resolveFields, tracepkg.Int64("bytes", info.Size()))
	}
	resolveSpan.EndMessage("Source binary validated", resolveFields...)
	legacyItems := []LegacyInstallation{}
	if options.MigrateLegacy {
		span := tracepkg.Start(ctx, "INSTALL", "install.legacy.discover", "Discovering legacy installations", tracepkg.String("source", resolvedSource))
		legacyItems, err = FindLegacyInstallations(layout, resolvedSource)
		if err != nil {
			span.FailMessage("Legacy installation discovery failed", err)
			return Result{}, err
		}
		removable, managed, preserved, conflicting := 0, 0, 0, 0
		for _, item := range legacyItems {
			if item.Removable {
				removable++
			} else {
				preserved++
			}
			if item.PackageManaged {
				managed++
			}
			if !item.Removable && !item.PackageManaged && item.Method == MethodUnknown {
				conflicting++
			}
		}
		span.EndMessage("Legacy installations discovered", tracepkg.Int("count", len(legacyItems)), tracepkg.Int("removable", removable), tracepkg.Int("preserved", preserved), tracepkg.Int("conflicting", conflicting), tracepkg.Int("package_managed", managed))
	}
	canonicalSpan := tracepkg.Start(ctx, "INSTALL", "install.canonical.inspect", "Inspecting canonical command", tracepkg.String("path", layout.CanonicalBinary))
	canonicalBefore, err := StatusCanonical(layout)
	if err != nil {
		canonicalSpan.FailMessage("Canonical command inspection failed", err)
		return Result{}, err
	}
	canonicalSpan.EndMessage("Canonical command inspected", tracepkg.String("state", string(canonicalBefore.State)), tracepkg.String("path", canonicalBefore.Path), tracepkg.String("target", canonicalBefore.Target))
	var canonicalLegacy *LegacyInstallation
	if canonicalBefore.State == CanonicalConflict {
		if options.MigrateLegacy {
			span := tracepkg.Start(ctx, "INSTALL", "install.canonical.legacy.inspect", "Inspecting canonical conflict as legacy installation", tracepkg.String("path", canonicalBefore.Path))
			canonicalLegacy, err = inspectCanonicalLegacy(layout, resolvedSource)
			if err != nil {
				span.FailMessage("Canonical legacy inspection failed", err)
				return Result{}, err
			}
			fields := []tracepkg.Field{tracepkg.String("path", canonicalBefore.Path), tracepkg.String("state", string(canonicalBefore.State)), tracepkg.Bool("recognized", canonicalLegacy != nil), tracepkg.Bool("removable", canonicalLegacy != nil && canonicalLegacy.Removable)}
			if canonicalLegacy != nil {
				fields = append(fields, tracepkg.String("method", string(canonicalLegacy.Method)), tracepkg.Bool("verified", canonicalLegacy.Verified), tracepkg.Bool("package_managed", canonicalLegacy.PackageManaged), tracepkg.String("reason", canonicalLegacy.Reason))
			}
			span.EndMessage("Canonical legacy inspection completed", fields...)
		}
		if canonicalLegacy == nil || !canonicalLegacy.Removable {
			if canonicalLegacy != nil && canonicalLegacy.Reason != "" {
				return Result{}, fmt.Errorf("%w: %s (%s)", ErrCanonicalConflict, canonicalBefore.Path, canonicalLegacy.Reason)
			}
			return Result{}, fmt.Errorf("%w: %s", ErrCanonicalConflict, canonicalBefore.Path)
		}
		if removableLegacyAt(legacyItems, canonicalLegacy.Path) == nil {
			legacyItems = append(legacyItems, *canonicalLegacy)
		}
	}
	aliasBefore := AliasStatus{State: AliasMissing, Path: layout.AliasPath, Target: layout.CurrentBinary}
	migrateAlias := false
	if !options.NoAlias {
		aliasSpan := tracepkg.Start(ctx, "INSTALL", "install.alias.inspect", "Inspecting command alias", tracepkg.String("path", layout.AliasPath))
		aliasBefore, err = StatusAlias(layout)
		if err != nil {
			aliasSpan.FailMessage("Command alias inspection failed", err)
			return Result{}, err
		}
		aliasSpan.EndMessage("Command alias inspected", tracepkg.String("state", string(aliasBefore.State)), tracepkg.String("path", aliasBefore.Path), tracepkg.String("target", aliasBefore.Target))
		if aliasBefore.State == AliasConflict {
			if options.MigrateLegacy {
				migrateAlias, err = legacyAliasMatchesAny(layout, legacyItems)
				if err != nil {
					return Result{}, err
				}
			}
			if !migrateAlias {
				return Result{}, fmt.Errorf("%w: %s", ErrAliasConflict, aliasBefore.Path)
			}
		}
	}
	metadataSpan := tracepkg.Start(ctx, "INSTALL", "install.metadata.read", "Reading installation metadata", tracepkg.String("path", layout.Metadata))
	previousMetadata, err := existingMetadata(layout.Metadata)
	if err != nil {
		metadataSpan.FailMessage("Installation metadata read failed", err)
		return Result{}, err
	}
	metadataFields := []tracepkg.Field{tracepkg.Bool("exists", previousMetadata != nil)}
	if previousMetadata != nil {
		metadataFields = append(metadataFields, tracepkg.String("version", previousMetadata.Version), tracepkg.String("method", string(previousMetadata.Method)))
	}
	metadataSpan.EndMessage("Installation metadata read", metadataFields...)
	metadataMatches := metadataMatches(previousMetadata, layout, version)
	matchFields := []tracepkg.Field{tracepkg.Bool("matches", metadataMatches), tracepkg.Bool("metadata_exists", previousMetadata != nil), tracepkg.String("expected_version", version), tracepkg.String("expected_method", string(MethodDirect))}
	if previousMetadata != nil {
		matchFields = append(matchFields, tracepkg.String("metadata_version", previousMetadata.Version), tracepkg.String("metadata_method", string(previousMetadata.Method)))
	}
	tracepkg.Emit(ctx, "INSTALL", "install.metadata.match", "Resolved installation metadata match decision", matchFields...)
	stageSpan := tracepkg.Start(ctx, "INSTALL", "install.stage", "Staging installation binary", tracepkg.String("version", version), tracepkg.String("source", resolvedSource))
	staged, err := Stage(layout, version, resolvedSource)
	if err != nil {
		stageSpan.FailMessage("Installation binary staging failed", err)
		return Result{}, err
	}
	stageFields := []tracepkg.Field{tracepkg.String("directory", staged.Dir), tracepkg.String("binary", staged.Binary), tracepkg.Bool("reused", staged.Reused)}
	if info, statErr := os.Stat(staged.Binary); statErr == nil {
		stageFields = append(stageFields, tracepkg.Int64("bytes", info.Size()))
	}
	stageSpan.EndMessage("Installation binary staged", stageFields...)
	activateSpan := tracepkg.Start(ctx, "INSTALL", "install.activate", "Activating installation version", tracepkg.String("version", version), tracepkg.String("target", staged.Dir))
	activation, err := Activate(staged)
	if err != nil {
		activateSpan.FailMessage("Installation activation failed", err)
		return Result{}, err
	}
	activateSpan.EndMessage("Installation version activated", tracepkg.String("previous_version", activation.PreviousVersion), tracepkg.String("previous_target", activation.PreviousTarget), tracepkg.String("current_target", activation.CurrentTarget), tracepkg.Bool("changed", activation.PreviousVersion != version))
	result = Result{Layout: layout, Version: version, Source: resolvedSource, Staged: staged, Activation: activation, PreviousMetadata: previousMetadata}
	backups := []legacyBackup{}
	rollback := func(cause error) (Result, error) {
		rollbackSpan := tracepkg.Start(ctx, "INSTALL", "install.rollback", "Rolling back installation", tracepkg.String("version", version), tracepkg.String("cause", cause.Error()), tracepkg.Int("legacy_backups", len(backups)))
		activationErr := Rollback(activation)
		rollbackErr := activationErr
		canonicalAttempted, canonicalOK := canonicalBefore.State == CanonicalMissing || canonicalLegacy != nil, true
		if canonicalBefore.State == CanonicalMissing || canonicalLegacy != nil {
			if _, removeErr := RemoveCanonical(layout); removeErr != nil {
				canonicalOK = false
				rollbackErr = errors.Join(rollbackErr, removeErr)
			}
		}
		aliasAttempted, aliasOK := !options.NoAlias && (aliasBefore.State == AliasMissing || migrateAlias), true
		if !options.NoAlias && (aliasBefore.State == AliasMissing || migrateAlias) {
			if _, removeErr := RemoveAlias(layout); removeErr != nil {
				aliasOK = false
				rollbackErr = errors.Join(rollbackErr, removeErr)
			}
		}
		restoreErr := restoreLegacyBackups(backups)
		if restoreErr != nil {
			rollbackErr = errors.Join(rollbackErr, restoreErr)
		}
		rollbackFields := []tracepkg.Field{
			tracepkg.Bool("activation_rollback_ok", activationErr == nil),
			tracepkg.Bool("canonical_cleanup_attempted", canonicalAttempted), tracepkg.Bool("canonical_cleanup_ok", canonicalOK),
			tracepkg.Bool("alias_cleanup_attempted", aliasAttempted), tracepkg.Bool("alias_cleanup_ok", aliasOK),
			tracepkg.Bool("legacy_restore_attempted", len(backups) > 0), tracepkg.Bool("legacy_restore_ok", restoreErr == nil),
		}
		if rollbackErr != nil {
			combined := fmt.Errorf("%w; rollback failed: %v", cause, rollbackErr)
			rollbackSpan.FailMessage("Installation rollback failed", combined, rollbackFields...)
			return Result{}, combined
		}
		rollbackSpan.EndMessage("Installation rolled back", rollbackFields...)
		return Result{}, cause
	}
	if canonicalLegacy != nil {
		span := tracepkg.Start(ctx, "INSTALL", "install.legacy.backup", "Backing up canonical legacy installation", tracepkg.String("path", canonicalLegacy.Path))
		backup, err := backupLegacyPath(canonicalLegacy.Path)
		if err != nil {
			span.FailMessage("Canonical legacy backup failed", err)
			return rollback(err)
		}
		span.EndMessage("Canonical legacy installation backed up", tracepkg.String("backup", backup.Backup))
		backups = append(backups, backup)
	}
	if migrateAlias {
		span := tracepkg.Start(ctx, "INSTALL", "install.alias.legacy.backup", "Backing up legacy alias", tracepkg.String("path", layout.AliasPath))
		backup, err := backupLegacyPath(layout.AliasPath)
		if err != nil {
			span.FailMessage("Legacy alias backup failed", err)
			return rollback(err)
		}
		span.EndMessage("Legacy alias backed up", tracepkg.String("backup", backup.Backup))
		backups = append(backups, backup)
	}
	canonicalInstallSpan := tracepkg.Start(ctx, "INSTALL", "install.canonical.install", "Installing canonical command", tracepkg.String("path", layout.CanonicalBinary), tracepkg.String("target", layout.CurrentBinary))
	canonical, err := InstallCanonical(layout)
	if err != nil {
		canonicalInstallSpan.FailMessage("Canonical command installation failed", err)
		return rollback(err)
	}
	canonicalInstallSpan.EndMessage("Canonical command installed", tracepkg.String("state", string(canonical.State)), tracepkg.String("path", canonical.Path), tracepkg.String("target", canonical.Target))
	result.Canonical = canonical
	if !options.NoAlias {
		aliasInstallSpan := tracepkg.Start(ctx, "INSTALL", "install.alias.install", "Installing command alias", tracepkg.String("path", layout.AliasPath), tracepkg.String("target", layout.CurrentBinary))
		alias, err := InstallAlias(layout)
		if err != nil {
			aliasInstallSpan.FailMessage("Command alias installation failed", err)
			return rollback(err)
		}
		aliasInstallSpan.EndMessage("Command alias installed", tracepkg.String("state", string(alias.State)), tracepkg.String("path", alias.Path), tracepkg.String("target", alias.Target))
		result.Alias = alias
		result.AliasInstalled = alias.State == AliasInstalled
	}
	metadata := Metadata{Schema: MetadataSchema, Method: MethodDirect, Version: version, InstallDir: layout.Root, BinDir: layout.BinDir}
	metadataWriteSpan := tracepkg.Start(ctx, "INSTALL", "install.metadata.write", "Writing installation metadata", tracepkg.String("path", layout.Metadata), tracepkg.String("version", version), tracepkg.String("method", string(MethodDirect)), tracepkg.Bool("atomic", true))
	if err := WriteMetadata(layout.Metadata, metadata); err != nil {
		metadataWriteSpan.FailMessage("Installation metadata write failed", err)
		return rollback(err)
	}
	metadataWriteSpan.EndMessage("Installation metadata written", tracepkg.String("path", layout.Metadata), tracepkg.Bool("atomic", true))
	if canonicalLegacy != nil {
		result.Legacy.Removed = append(result.Legacy.Removed, *canonicalLegacy)
	}
	if migrateAlias {
		result.Legacy.RemovedAliases = append(result.Legacy.RemovedAliases, layout.AliasPath)
	}
	discardLegacyBackups(backups, &result.Legacy)
	if options.MigrateLegacy {
		cleanup, cleanupErr := CleanupLegacyInstallations(LegacyCleanupOptions{Context: ctx, Layout: layout, Source: resolvedSource})
		if cleanupErr != nil {
			result.Legacy.Failed = append(result.Legacy.Failed, LegacyCleanupFailure{Path: "PATH", Err: cleanupErr})
		} else {
			result.Legacy.Removed = append(result.Legacy.Removed, cleanup.Removed...)
			result.Legacy.RemovedAliases = append(result.Legacy.RemovedAliases, cleanup.RemovedAliases...)
			result.Legacy.Preserved = append(result.Legacy.Preserved, cleanup.Preserved...)
			result.Legacy.Failed = append(result.Legacy.Failed, cleanup.Failed...)
		}
	}
	result.AlreadyInstalled = metadataMatches && staged.Reused && activation.PreviousVersion == version && canonicalBefore.State == CanonicalInstalled && (options.NoAlias || aliasBefore.State == AliasInstalled)
	return result, nil
}

func normalizeInstallVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || isDevelopmentVersion(version) {
		return version
	}
	if version[0] == 'V' {
		return "v" + version[1:]
	}
	if version[0] != 'v' {
		return "v" + version
	}
	return version
}

func existingMetadata(path string) (*Metadata, error) {
	metadata, err := ReadMetadata(path)
	if errors.Is(err, ErrMetadataNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &metadata, nil
}

func metadataMatches(metadata *Metadata, layout Layout, version string) bool {
	return metadata != nil && metadata.Method == MethodDirect && metadata.Version == version && samePath(metadata.InstallDir, layout.Root) && samePath(metadata.BinDir, layout.BinDir)
}

func isDevelopmentVersion(version string) bool {
	version = strings.ToLower(strings.TrimSpace(version))
	return version == "dev" || version == "(devel)" || strings.HasPrefix(version, "dev-")
}
