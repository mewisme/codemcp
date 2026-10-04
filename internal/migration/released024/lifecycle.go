package released024

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/oslock"
	"go.mewis.me/codemcp/internal/service"
)

const (
	cleanupStateDeleting = "deleting"
	cleanupStateCleaned  = "cleaned"
)

type MigrationStatus struct {
	Transactions    int `json:"transactions"`
	Pending         int `json:"pending"`
	Interrupted     int `json:"interrupted"`
	Retired         int `json:"retired"`
	RetainedBackups int `json:"retained_backups"`
	CleanupDue      int `json:"cleanup_due"`
	CleanupRunning  int `json:"cleanup_running"`
	Cleaned         int `json:"cleaned"`
	MissingBackups  int `json:"missing_backups"`
	Invalid         int `json:"invalid"`
}

type CleanupOptions struct {
	JournalPath string
	AllowEarly  bool
	Now         func() time.Time
}

type CleanupResult struct {
	JournalPath   string     `json:"journal_path"`
	BackupRemoved bool       `json:"backup_removed"`
	AlreadyClean  bool       `json:"already_clean"`
	CleanedAt     *time.Time `json:"cleaned_at,omitempty"`
}

type RecoveryOptions struct {
	JournalPath    string
	ServiceControl HistoricalServiceController
	ServiceManager service.Manager
	Now            func() time.Time
}

type RecoveryResult struct {
	JournalPath   string `json:"journal_path"`
	Phase         string `json:"phase"`
	ReadyForRetry bool   `json:"ready_for_retry"`
}

type FreshInstallCleanupOptions struct {
	TargetRoot     string
	SourceOptions  Options
	ServiceManager service.Manager
}

type FreshInstallCleanupResult struct {
	TransactionsRemoved int `json:"transactions_removed"`
	StagesRemoved       int `json:"stages_removed"`
	TargetsRemoved      int `json:"targets_removed"`
	SourcesRemoved      int `json:"sources_removed"`
}

// CleanupForFreshInstall abandons migration-owned state and marker-verified
// released predecessors so a caller can continue with a clean install. Any
// ownership ambiguity is reported while unrelated state is left untouched.
func CleanupForFreshInstall(ctx context.Context, options FreshInstallCleanupOptions) (FreshInstallCleanupResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	result := FreshInstallCleanupResult{}
	var cleanupErr error

	if strings.TrimSpace(options.TargetRoot) != "" {
		transactions, err := cleanupMigrationTransactionsForFreshInstall(ctx, options.TargetRoot, options.ServiceManager)
		result.TransactionsRemoved += transactions.TransactionsRemoved
		result.StagesRemoved += transactions.StagesRemoved
		result.TargetsRemoved += transactions.TargetsRemoved
		cleanupErr = errors.Join(cleanupErr, err)
	}

	sources, err := VerifiedSources(options.SourceOptions)
	if err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("discover released predecessors for fresh install: %w", err))
		return result, cleanupErr
	}
	for _, source := range sources {
		manifest := Manifest{Version: ManifestVersion, Source: source, Found: true}
		if launchers, inspectErr := inspectLaunchers(options.SourceOptions, source); inspectErr != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("inspect released launchers before fresh install: %w", inspectErr))
		} else {
			manifest.Launchers = launchers
		}
		inspectServices := options.SourceOptions.InspectServices
		if inspectServices == nil {
			inspectServices = inspectPlatformServices
		}
		if services, inspectErr := inspectServices(ctx, source); inspectErr != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("inspect released services before fresh install: %w", inspectErr))
		} else {
			manifest.Services = normalizeServices(services)
		}
		discarded, discardErr := DiscardPredecessor(ctx, DiscardOptions{Manifest: manifest, BestEffort: true})
		if discarded.RootRemoved {
			result.SourcesRemoved++
		}
		cleanupErr = errors.Join(cleanupErr, discardErr)
	}
	return result, cleanupErr
}

func cleanupMigrationTransactionsForFreshInstall(ctx context.Context, targetRoot string, manager service.Manager) (FreshInstallCleanupResult, error) {
	result := FreshInstallCleanupResult{}
	targetRoot, err := normalizeStageTarget(targetRoot)
	if err != nil {
		return result, err
	}
	pattern := filepath.Join(filepath.Dir(targetRoot), "."+filepath.Base(targetRoot)+".migration-024-*.journal.json")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return result, err
	}
	if manager == nil {
		manager = service.NewManager()
	}
	var cleanupErr error
	for _, journalPath := range paths {
		journal, exists, readErr := readStageJournal(journalPath)
		if readErr != nil || !exists {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("read migration transaction %s: %w", journalPath, readErr))
			continue
		}
		if !sameComparablePath(journal.TargetRoot, targetRoot) || !sameComparablePath(stageJournalPath(journal), journalPath) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("migration transaction ownership mismatch: %s", journalPath))
			continue
		}
		_, _, lockPath := stagePaths(journal.TargetRoot, journal.SourceSHA256)
		lock, lockErr := oslock.Acquire(lockPath, oslock.Exclusive)
		if lockErr != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("acquire migration cleanup lock: %w", lockErr))
			continue
		}

		transactionErr := cleanupMigrationTransactionForFreshInstall(ctx, journal, manager, &result)
		if transactionErr == nil {
			if removeErr := os.Remove(journalPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				transactionErr = errors.Join(transactionErr, removeErr)
			} else {
				result.TransactionsRemoved++
			}
		}
		_ = lock.Release()
		if transactionErr == nil {
			if removeErr := os.Remove(lockPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				transactionErr = errors.Join(transactionErr, removeErr)
			}
		}
		cleanupErr = errors.Join(cleanupErr, transactionErr)
	}
	return result, cleanupErr
}

func cleanupMigrationTransactionForFreshInstall(ctx context.Context, journal StageJournal, manager service.Manager, result *FreshInstallCleanupResult) error {
	var cleanupErr error
	if pathExists(journal.TargetRoot) && !journal.Rollback.TargetExists {
		cleanupErr = errors.Join(cleanupErr, removeCanonicalMigrationService(ctx, journal, manager))
		if configformat.IsManagedRoot(journal.TargetRoot) {
			if err := os.RemoveAll(journal.TargetRoot); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove migration-owned target root: %w", err))
			} else if result != nil {
				result.TargetsRemoved++
			}
		} else {
			cleanupErr = errors.Join(cleanupErr, errors.New("migration target is no longer a managed CodeMCP root; left untouched"))
		}
	}
	if pathExists(journal.StageRoot) {
		if err := removeOwnedStageRoot(journal.StageRoot, journal); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		} else if result != nil {
			result.StagesRemoved++
		}
	}
	return cleanupErr
}

func removeCanonicalMigrationService(ctx context.Context, journal StageJournal, manager service.Manager) error {
	if manager == nil {
		return nil
	}
	scopes := []service.Scope{service.ScopeUser, service.ScopeSystem}
	if runtime.GOOS == "windows" {
		scopes = []service.Scope{service.ScopeUser}
	}
	if journal.Canonical.Scope != "" {
		scope := service.Scope(journal.Canonical.Scope)
		if scope == service.ScopeUser || scope == service.ScopeSystem {
			scopes = []service.Scope{scope}
		}
	}
	var cleanupErr error
	for _, scope := range scopes {
		account, err := service.InvokingAccountContext(ctx, scope)
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
			continue
		}
		spec := service.Spec{ID: service.ID(journal.TargetRoot, scope), Scope: scope, ConfigRoot: journal.TargetRoot, Account: account}
		if journal.Canonical.Scope == string(scope) {
			if journal.Canonical.ServiceID != "" {
				spec.ID = journal.Canonical.ServiceID
			}
			spec.Binary = journal.Canonical.Binary
			spec.EnvironmentHash = journal.Canonical.EnvironmentHash
		}
		status, err := manager.Status(spec)
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
			continue
		}
		if status.Running {
			if err := service.StopBackend(manager, spec); err != nil {
				cleanupErr = errors.Join(cleanupErr, err)
				continue
			}
		}
		if status.Installed {
			cleanupErr = errors.Join(cleanupErr, manager.Uninstall(spec))
		}
	}
	return cleanupErr
}

func InspectStatus(targetRoot string, now time.Time) (MigrationStatus, error) {
	targetRoot, err := normalizeStageTarget(targetRoot)
	if err != nil {
		return MigrationStatus{}, err
	}
	pattern := filepath.Join(filepath.Dir(targetRoot), "."+filepath.Base(targetRoot)+".migration-024-*.journal.json")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return MigrationStatus{}, err
	}
	status := MigrationStatus{}
	for _, path := range paths {
		journal, exists, readErr := readStageJournal(path)
		if readErr != nil || !exists || !sameComparablePath(journal.TargetRoot, targetRoot) || !sameComparablePath(stageJournalPath(journal), path) {
			status.Invalid++
			continue
		}
		status.Transactions++
		switch journal.Phase {
		case stagePhaseStaged, stagePhaseCommitted:
			status.Pending++
		case stagePhaseQuiescing, stagePhaseStaging, stagePhaseActivating, stagePhaseActivationFailed, stagePhaseRetiring, stagePhaseRetirementFailed, stagePhaseFailed:
			status.Interrupted++
		case stagePhaseRetired:
			status.Retired++
		}
		if journal.CleanupState == cleanupStateDeleting {
			status.CleanupRunning++
		}
		if journal.CleanupState == cleanupStateCleaned || journal.CleanedAt != nil {
			status.Cleaned++
			continue
		}
		if journal.Phase != stagePhaseRetired {
			continue
		}
		if pathExists(journal.SourceRoot) {
			status.RetainedBackups++
			if journal.RetainUntil != nil && !now.Before(*journal.RetainUntil) {
				status.CleanupDue++
			}
		} else {
			status.MissingBackups++
		}
	}
	return status, nil
}

func CleanupRetainedBackup(ctx context.Context, options CleanupOptions) (CleanupResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return CleanupResult{}, err
	}
	journalPath := strings.TrimSpace(options.JournalPath)
	if journalPath == "" {
		return CleanupResult{}, errors.New("migration cleanup journal path is required")
	}
	journal, exists, err := readStageJournal(journalPath)
	if err != nil {
		return CleanupResult{}, err
	}
	if !exists {
		return CleanupResult{}, errors.New("migration cleanup journal does not exist")
	}
	if !sameComparablePath(stageJournalPath(journal), journalPath) {
		return CleanupResult{}, errors.New("migration cleanup journal path does not match migration transaction")
	}
	_, _, lockPath := stagePaths(journal.TargetRoot, journal.SourceSHA256)
	lock, err := oslock.Acquire(lockPath, oslock.Exclusive)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("acquire migration cleanup lock: %w", err)
	}
	defer lock.Release()

	journal, exists, err = readStageJournal(journalPath)
	if err != nil || !exists {
		if err == nil {
			err = errors.New("migration cleanup journal disappeared")
		}
		return CleanupResult{}, err
	}
	if journal.CleanupState == cleanupStateCleaned || journal.CleanedAt != nil {
		return CleanupResult{JournalPath: journalPath, AlreadyClean: true, CleanedAt: journal.CleanedAt}, nil
	}
	if journal.Phase != stagePhaseRetired {
		return CleanupResult{}, fmt.Errorf("retained backup cleanup requires retired migration, got %q", journal.Phase)
	}
	if strings.TrimSpace(journal.RetainedSHA256) == "" {
		return CleanupResult{}, errors.New("retained backup has no retirement fingerprint; refusing cleanup")
	}
	now := time.Now
	if options.Now != nil {
		now = options.Now
	}
	current := now().UTC()
	if journal.RetainUntil == nil {
		return CleanupResult{}, errors.New("retained backup has no retention deadline; refusing cleanup")
	}
	if current.Before(*journal.RetainUntil) && !options.AllowEarly {
		return CleanupResult{}, fmt.Errorf("retained backup is protected until %s", journal.RetainUntil.Format(time.RFC3339))
	}
	cleanSource := filepath.Clean(journal.SourceRoot)
	volumeRoot := filepath.VolumeName(cleanSource) + string(filepath.Separator)
	if sameComparablePath(journal.SourceRoot, journal.TargetRoot) ||
		withinPath(journal.SourceRoot, journal.TargetRoot) ||
		withinPath(journal.TargetRoot, journal.SourceRoot) ||
		cleanSource == "." || cleanSource == string(filepath.Separator) || cleanSource == volumeRoot {
		return CleanupResult{}, errors.New("retained backup path is unsafe")
	}

	resumingDelete := journal.CleanupState == cleanupStateDeleting
	if !pathExists(journal.SourceRoot) && !resumingDelete {
		return CleanupResult{}, errors.New("retained backup is missing without cleanup ownership evidence")
	}
	if !resumingDelete {
		started := current
		journal.CleanupState = cleanupStateDeleting
		journal.CleanupStartedAt = &started
		journal.UpdatedAt = current
		if err := writeStageJournal(journalPath, journal); err != nil {
			return CleanupResult{}, err
		}
	}
	if pathExists(journal.SourceRoot) {
		hash, err := fingerprintRetainedTree(journal.SourceRoot)
		if err != nil {
			return CleanupResult{}, fmt.Errorf("fingerprint retained backup before cleanup: %w", err)
		}
		if hash != journal.RetainedSHA256 {
			return CleanupResult{}, errors.New("retained backup changed after retirement; refusing cleanup")
		}
		if err := os.RemoveAll(journal.SourceRoot); err != nil {
			return CleanupResult{}, fmt.Errorf("remove retained backup: %w", err)
		}
	}
	cleaned := current
	journal.CleanupState = cleanupStateCleaned
	journal.CleanedAt = &cleaned
	journal.UpdatedAt = cleaned
	if err := writeStageJournal(journalPath, journal); err != nil {
		return CleanupResult{}, err
	}
	return CleanupResult{JournalPath: journalPath, BackupRemoved: true, CleanedAt: &cleaned}, nil
}

func RecoverInterrupted(ctx context.Context, options RecoveryOptions) (RecoveryResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	journalPath := strings.TrimSpace(options.JournalPath)
	if journalPath == "" {
		return RecoveryResult{}, errors.New("migration recovery journal path is required")
	}
	journal, exists, err := readStageJournal(journalPath)
	if err != nil {
		return RecoveryResult{}, err
	}
	if !exists {
		return RecoveryResult{}, errors.New("migration recovery journal does not exist")
	}
	if !sameComparablePath(stageJournalPath(journal), journalPath) {
		return RecoveryResult{}, errors.New("migration recovery journal path does not match migration transaction")
	}
	_, _, lockPath := stagePaths(journal.TargetRoot, journal.SourceSHA256)
	lock, err := oslock.Acquire(lockPath, oslock.Exclusive)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("acquire migration recovery lock: %w", err)
	}
	defer lock.Release()
	journal, exists, err = readStageJournal(journalPath)
	if err != nil || !exists {
		if err == nil {
			err = errors.New("migration recovery journal disappeared")
		}
		return RecoveryResult{}, err
	}

	now := time.Now
	if options.Now != nil {
		now = options.Now
	}
	controller := options.ServiceControl
	if controller == nil {
		controller = platformHistoricalServiceController{}
	}
	source := SourceDescriptor{Release: SourceRelease, Root: journal.SourceRoot, OperatorHome: journal.Rollback.OperatorHome}

	switch journal.Phase {
	case stagePhaseQuiescing, stagePhaseStaging, stagePhaseFailed:
		if pathExists(journal.TargetRoot) {
			return RecoveryResult{}, errors.New("staging recovery found canonical target state; refusing destructive reset")
		}
		if pathExists(journal.StageRoot) {
			if err := removeOwnedStageRoot(journal.StageRoot, journal); err != nil {
				return RecoveryResult{}, err
			}
		}
		if err := restoreHistoricalServices(ctx, controller, source, journal.Services); err != nil {
			return RecoveryResult{}, fmt.Errorf("restore historical services during staging recovery: %w", err)
		}
		journal.Phase = stagePhaseFailed
		journal.FailureStage = "manual-recovery"
		journal.UpdatedAt = now().UTC()
		if err := writeStageJournal(journalPath, journal); err != nil {
			return RecoveryResult{}, err
		}
		return RecoveryResult{JournalPath: journalPath, Phase: journal.Phase, ReadyForRetry: true}, nil

	case stagePhaseActivating, stagePhaseActivationFailed:
		if pathExists(journal.TargetRoot) {
			return RecoveryResult{}, errors.New("activation recovery requires canonical target root to be absent")
		}
		if !dirExists(journal.StageRoot) {
			return RecoveryResult{}, errors.New("activation recovery requires intact staged state")
		}
		hash, err := fingerprintStageTree(journal.StageRoot)
		if err != nil || hash != journal.StagedSHA256 {
			if err == nil {
				err = errors.New("staged state fingerprint does not match migration journal")
			}
			return RecoveryResult{}, err
		}
		manager := options.ServiceManager
		if manager == nil {
			manager = service.NewManager()
		}
		for _, scope := range []service.Scope{service.ScopeUser, service.ScopeSystem} {
			if scope == service.ScopeSystem && runtime.GOOS == "windows" {
				continue
			}
			spec := service.Spec{ID: service.ID(journal.TargetRoot, scope), Scope: scope, ConfigRoot: journal.TargetRoot}
			status, statusErr := manager.Status(spec)
			if statusErr != nil {
				return RecoveryResult{}, fmt.Errorf("inspect canonical %s service during recovery: %w", scope, statusErr)
			}
			if status.Installed || status.Running {
				return RecoveryResult{}, fmt.Errorf("canonical %s service still exists; remove it before recovery", scope)
			}
		}
		if err := restoreHistoricalServices(ctx, controller, source, journal.Services); err != nil {
			return RecoveryResult{}, fmt.Errorf("restore historical services during activation recovery: %w", err)
		}
		journal.Phase = stagePhaseStaged
		journal.FailureStage = ""
		journal.Activation = append(journal.Activation, ActivationOutcome{Stage: "recovery", State: "success", Detail: "operator-confirmed activation baseline restored"})
		journal.UpdatedAt = now().UTC()
		if err := writeStageJournal(journalPath, journal); err != nil {
			return RecoveryResult{}, err
		}
		return RecoveryResult{JournalPath: journalPath, Phase: journal.Phase, ReadyForRetry: true}, nil
	default:
		return RecoveryResult{}, fmt.Errorf("migration phase %q does not require manual recovery", journal.Phase)
	}
}
