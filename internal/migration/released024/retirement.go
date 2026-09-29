package released024

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/install"
	"go.mewis.me/codemcp/internal/oslock"
	"go.mewis.me/codemcp/internal/service"
)

const defaultRollbackRetention = 7 * 24 * time.Hour

type RetirementEvent struct {
	Stage   string `json:"stage"`
	State   string `json:"state"`
	Message string `json:"message"`
	Child   bool   `json:"child,omitempty"`
}

type LauncherRemovalFunc func(Launcher) (bool, error)

type RetireOptions struct {
	JournalPath    string
	ServiceManager service.Manager
	RuntimeProbe   service.RuntimeProbe
	ServiceRetirer HistoricalServiceRetirer
	RemoveLauncher LauncherRemovalFunc
	Retention      time.Duration
	Observe        func(RetirementEvent)
	InjectFailure  func(string) error
	Now            func() time.Time
}

type RetirementResult struct {
	TargetRoot     string              `json:"target_root"`
	Outcomes       []RetirementOutcome `json:"outcomes"`
	RetainUntil    *time.Time          `json:"retain_until,omitempty"`
	RetainedSHA256 string              `json:"retained_sha256,omitempty"`
	Retired        bool                `json:"retired"`
}

func Retire(ctx context.Context, options RetireOptions) (RetirementResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	journalPath := strings.TrimSpace(options.JournalPath)
	if journalPath == "" {
		return RetirementResult{}, errors.New("migration retirement journal path is required")
	}
	journal, exists, err := readStageJournal(journalPath)
	if err != nil {
		return RetirementResult{}, err
	}
	if !exists {
		return RetirementResult{}, errors.New("migration retirement journal does not exist")
	}
	if expected := stageJournalPath(journal); !sameComparablePath(expected, journalPath) {
		return RetirementResult{}, errors.New("migration retirement journal path does not match migration transaction")
	}
	_, _, lockPath := stagePaths(journal.TargetRoot, journal.SourceSHA256)
	lock, err := oslock.Acquire(lockPath, oslock.Exclusive)
	if err != nil {
		return RetirementResult{}, fmt.Errorf("acquire migration retirement lock: %w", err)
	}
	defer lock.Release()
	journal, exists, err = readStageJournal(journalPath)
	if err != nil || !exists {
		if err == nil {
			err = errors.New("migration retirement journal disappeared")
		}
		return RetirementResult{}, err
	}
	if journal.Phase == stagePhaseRetired {
		return retirementResultFromJournal(journal), nil
	}
	if journal.Phase != stagePhaseCommitted && journal.Phase != stagePhaseRetiring && journal.Phase != stagePhaseRetirementFailed {
		return RetirementResult{}, fmt.Errorf("historical retirement requires committed migration, got %q", journal.Phase)
	}

	now := time.Now
	if options.Now != nil {
		now = options.Now
	}
	emit := func(stage, state, message string, child bool) {
		if options.Observe != nil {
			options.Observe(RetirementEvent{Stage: stage, State: state, Message: message, Child: child})
		}
	}
	record := func(kind, target, state, detail string) {
		journal.Retirement = append(journal.Retirement, RetirementOutcome{Kind: kind, Target: target, State: state, Detail: detail})
	}
	inject := func(point string) error {
		if options.InjectFailure == nil {
			return nil
		}
		return options.InjectFailure(point)
	}
	fail := func(stage string, cause error) (RetirementResult, error) {
		record(stage, "", "failed", cause.Error())
		emit(stage, "failed", cause.Error(), false)
		journal.Phase = stagePhaseRetirementFailed
		journal.FailureStage = stage
		journal.UpdatedAt = now().UTC()
		return RetirementResult{}, errors.Join(cause, writeStageJournal(journalPath, journal))
	}

	manager := options.ServiceManager
	if manager == nil {
		manager = service.NewManager()
	}
	probe := options.RuntimeProbe
	if probe == nil {
		probe = runtimeProbeAt(journal.TargetRoot)
	}
	emit("canonical", "running", "Verifying canonical CodeMCP runtime before historical retirement", false)
	if err := verifyCanonicalForRetirement(ctx, journal, manager, probe); err != nil {
		emit("canonical", "failed", err.Error(), false)
		return RetirementResult{}, err
	}

	journal.Phase = stagePhaseRetiring
	journal.Retirement = nil
	journal.FailureStage = ""
	record("canonical", journal.Canonical.ServiceID, "success", "canonical service definition and runtime ownership verified")
	emit("canonical", "success", "Canonical CodeMCP runtime verified", false)
	journal.UpdatedAt = now().UTC()
	if err := writeStageJournal(journalPath, journal); err != nil {
		return RetirementResult{}, err
	}

	retirer := options.ServiceRetirer
	if retirer == nil {
		retirer = platformHistoricalServiceController{}
	}
	source := SourceDescriptor{Release: SourceRelease, Root: journal.SourceRoot, OperatorHome: journal.Rollback.OperatorHome}
	emit("services", "running", "Retiring verified historical managed services", false)
	for _, state := range journal.Services {
		if state.Ownership != OwnershipVerified {
			if state.Ownership != OwnershipAbsent || state.Installed || state.Bootstrapped || state.Running {
				record("service", state.ID, "warning", "historical service ownership is not verified; left untouched")
				emit("services", "warning", state.ID+" · ownership not verified; left untouched", true)
			}
			continue
		}
		if err := retirer.Retire(ctx, source, state); err != nil {
			return fail("services", fmt.Errorf("retire historical service %s: %w", state.ID, err))
		}
		record("service", state.ID, "success", "historical service registration retired")
		emit("services", "success", state.ID+" · retired", true)
	}
	emit("services", "success", "Verified historical managed services retired", false)
	if err := inject("services-retired"); err != nil {
		return fail("services", err)
	}

	removeLauncher := options.RemoveLauncher
	if removeLauncher == nil {
		removeLauncher = removeHistoricalLauncher
	}
	emit("launchers", "running", "Removing installer-owned historical executable identities", false)
	for _, launcher := range journal.Launchers {
		if launcher.Ownership != OwnershipVerified || launcher.PackageManaged || !launcher.Removable {
			if launcher.Ownership != OwnershipAbsent {
				record("launcher", launcher.Path, "warning", "launcher is not installer-owned removable state; left untouched")
				emit("launchers", "warning", launcher.Path+" · left untouched", true)
			}
			continue
		}
		removed, err := removeLauncher(launcher)
		if err != nil {
			return fail("launchers", fmt.Errorf("remove historical launcher %s: %w", launcher.Path, err))
		}
		detail := "historical launcher already absent"
		if removed {
			detail = "historical launcher removed"
		}
		record("launcher", launcher.Path, "success", detail)
		emit("launchers", "success", launcher.Path+" · "+detail, true)
	}
	emit("launchers", "success", "Installer-owned historical executable identities retired", false)
	if err := inject("launchers-retired"); err != nil {
		return fail("launchers", err)
	}

	emit("runtime-metadata", "running", "Removing verified historical runtime metadata", false)
	for _, artifact := range journal.Artifacts {
		if !retirementRuntimeArtifact(artifact) {
			continue
		}
		removed, err := removeHistoricalRuntimeArtifact(journal.SourceRoot, artifact)
		if err != nil {
			record("runtime-metadata", artifact.Path, "warning", err.Error())
			emit("runtime-metadata", "warning", artifact.Path+" · "+err.Error(), true)
			continue
		}
		detail := "runtime metadata already absent"
		if removed {
			detail = "runtime metadata removed"
		}
		record("runtime-metadata", artifact.Path, "success", detail)
		emit("runtime-metadata", "success", artifact.Path+" · "+detail, true)
	}
	emit("runtime-metadata", "success", "Historical runtime metadata cleanup complete", false)

	retainedSHA256, err := fingerprintExactTree(journal.SourceRoot)
	if err != nil {
		return fail("retention", fmt.Errorf("fingerprint retained released backup: %w", err))
	}
	journal.RetainedSHA256 = retainedSHA256
	retention := options.Retention
	if retention <= 0 {
		retention = defaultRollbackRetention
	}
	retainUntil := now().UTC().Add(retention)
	journal.RetainUntil = &retainUntil
	record("retention", journal.SourceRoot, "success", "released data snapshot retained for bounded rollback window")
	emit("retention", "success", "Released data snapshot retained until "+retainUntil.Format(time.RFC3339), false)

	emit("readiness", "running", "Rechecking canonical runtime after historical cleanup", false)
	if err := verifyCanonicalForRetirement(ctx, journal, manager, probe); err != nil {
		return fail("readiness", err)
	}
	record("readiness", journal.Canonical.ServiceID, "success", "canonical runtime remains ready after cleanup")
	emit("readiness", "success", "Canonical runtime remains ready", false)
	if err := inject("readiness"); err != nil {
		return fail("readiness", err)
	}

	retiredAt := now().UTC()
	journal.Phase = stagePhaseRetired
	journal.RetiredAt = &retiredAt
	journal.FailureStage = ""
	journal.UpdatedAt = retiredAt
	if err := writeStageJournal(journalPath, journal); err != nil {
		return RetirementResult{}, err
	}
	emit("commit", "success", "Historical executable and service identities retired", false)
	return retirementResultFromJournal(journal), nil
}

func verifyCanonicalForRetirement(ctx context.Context, journal StageJournal, manager service.Manager, probe service.RuntimeProbe) error {
	ref := journal.Canonical
	if strings.TrimSpace(ref.ServiceID) == "" || strings.TrimSpace(ref.Scope) == "" || strings.TrimSpace(ref.Binary) == "" ||
		strings.TrimSpace(ref.EnvironmentHash) == "" || strings.TrimSpace(ref.ConfigRoot) == "" {
		return errors.New("migration journal is missing canonical activation evidence")
	}
	if !sameComparablePath(ref.ConfigRoot, journal.TargetRoot) || sameComparablePath(ref.ConfigRoot, journal.SourceRoot) {
		return errors.New("canonical activation evidence points at the wrong config root")
	}
	if !configformat.IsManagedRoot(journal.TargetRoot) {
		return errors.New("canonical CodeMCP root marker is missing")
	}
	scope := service.Scope(ref.Scope)
	if scope != service.ScopeUser && scope != service.ScopeSystem {
		return fmt.Errorf("unsupported canonical service scope %q", ref.Scope)
	}
	if ref.ServiceID != service.ID(journal.TargetRoot, scope) {
		return errors.New("canonical service identity does not match target root")
	}
	account, err := service.InvokingAccountContext(ctx, scope)
	if err != nil {
		return err
	}
	spec := service.Spec{ID: ref.ServiceID, Scope: scope, ConfigRoot: journal.TargetRoot, Binary: ref.Binary, EnvironmentHash: ref.EnvironmentHash, Account: account}
	status, err := manager.Status(spec)
	if err != nil {
		return err
	}
	if !status.Installed {
		return errors.New("canonical CodeMCP service is not installed")
	}
	matches, err := manager.DefinitionMatches(spec)
	if err != nil {
		return err
	}
	if !matches {
		return errors.New("canonical CodeMCP service definition changed before historical retirement")
	}
	runtimeStatus, found, err := probe(ctx)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("canonical CodeMCP runtime is not ready")
	}
	if !runtimeStatus.Managed || runtimeStatus.ServiceID != ref.ServiceID || runtimeStatus.ServiceScope != ref.Scope ||
		!sameComparablePath(runtimeStatus.ConfigRoot, journal.TargetRoot) {
		return errors.New("canonical runtime ownership does not match committed migration")
	}
	return nil
}

func removeHistoricalLauncher(launcher Launcher) (bool, error) {
	switch launcher.Kind {
	case "executable":
		return install.RemoveLegacyInstallation(install.LegacyInstallation{
			Path: launcher.Path, Target: launcher.Target, Method: launcher.Method,
			Verified: launcher.Ownership == OwnershipVerified, PackageManaged: launcher.PackageManaged, Removable: launcher.Removable,
		})
	case "alias":
		return install.RemoveLegacyAlias(install.LegacyAlias{
			Path: launcher.Path, Target: launcher.Target,
			Verified: launcher.Ownership == OwnershipVerified, PackageManaged: launcher.PackageManaged, Removable: launcher.Removable,
		})
	default:
		return false, fmt.Errorf("unsupported historical launcher kind %q", launcher.Kind)
	}
}

func removeHistoricalRuntimeArtifact(sourceRoot string, artifact Artifact) (bool, error) {
	if !retirementRuntimeArtifact(artifact) {
		return false, errors.New("artifact is not migration-owned runtime metadata")
	}
	relative := filepath.Clean(filepath.FromSlash(artifact.Path))
	if relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false, errors.New("runtime metadata path is unsafe")
	}
	path := filepath.Join(sourceRoot, relative)
	if !withinPath(sourceRoot, path) {
		return false, errors.New("runtime metadata escapes released root")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("runtime metadata ownership changed to non-regular entry")
	}
	if strings.TrimSpace(artifact.SHA256) == "" {
		return false, errors.New("runtime metadata has no quiesced fingerprint; left untouched")
	}
	hash, _, err := hashFile(path, maxRegularFileBytes)
	if err != nil {
		return false, err
	}
	if hash != artifact.SHA256 {
		return false, errors.New("runtime metadata changed after quiescence; left untouched")
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	return true, nil
}

func retirementRuntimeArtifact(artifact Artifact) bool {
	return artifact.Classification == ClassTransientDrop && artifact.Kind == "runtime-state" ||
		artifact.Classification == ClassRegenerate && artifact.Kind == "service-environment"
}

func retirementResultFromJournal(journal StageJournal) RetirementResult {
	return RetirementResult{
		TargetRoot:     journal.TargetRoot,
		Outcomes:       append([]RetirementOutcome(nil), journal.Retirement...),
		RetainUntil:    journal.RetainUntil,
		RetainedSHA256: journal.RetainedSHA256,
		Retired:        journal.Phase == stagePhaseRetired,
	}
}
