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
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/instance"
	"go.mewis.me/codemcp/internal/migration/credentials024"
	"go.mewis.me/codemcp/internal/migration/integrations024"
	"go.mewis.me/codemcp/internal/migration/upstream024"
	"go.mewis.me/codemcp/internal/migration/workspace024"
	"go.mewis.me/codemcp/internal/oauth"
	"go.mewis.me/codemcp/internal/oslock"
	"go.mewis.me/codemcp/internal/secretstore"
	"go.mewis.me/codemcp/internal/service"
	statepkg "go.mewis.me/codemcp/internal/state"
	"go.mewis.me/codemcp/internal/tunnel"
	"go.mewis.me/codemcp/internal/workspace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

const (
	StageJournalVersion        = 1
	stagePhaseQuiescing        = "quiescing"
	stagePhaseStaging          = "staging"
	stagePhaseStaged           = "staged"
	stagePhaseActivating       = "activating"
	stagePhaseActivationFailed = "activation-failed"
	stagePhaseCommitted        = "committed"
	stagePhaseFailed           = "failed"
	maxStageJournalBytes       = 2 << 20
)

type StageOptions struct {
	Manifest         Manifest
	TargetRoot       string
	BundleSourcePath string
	BundleSHA256     string
	RefreshManifest  func(context.Context, Manifest) (Manifest, error)
	ServiceControl   HistoricalServiceController
	Now              func() time.Time
	InjectFailure    func(string) error
}

type HistoricalServiceController interface {
	Quiesce(context.Context, SourceDescriptor, ServiceState) error
	Restore(context.Context, SourceDescriptor, ServiceState) error
}

type DomainOutcome struct {
	Domain string `json:"domain"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

type WorkspaceStageOutcome struct {
	ID            string `json:"id"`
	LegacyID      string `json:"legacy_id,omitempty"`
	WorkspaceRoot string `json:"workspace_root"`
	Destination   string `json:"destination"`
	StagePath     string `json:"stage_path,omitempty"`
	State         string `json:"state"`
	Detail        string `json:"detail,omitempty"`
	SourceSHA256  string `json:"source_sha256,omitempty"`
}

type RollbackReference struct {
	SourceRoot   string `json:"source_root"`
	SourceSHA256 string `json:"source_sha256"`
	OperatorHome string `json:"operator_home,omitempty"`
	TargetRoot   string `json:"target_root"`
	TargetExists bool   `json:"target_exists"`
	BundlePath   string `json:"bundle_path,omitempty"`
	BundleSHA256 string `json:"bundle_sha256,omitempty"`
}

type StageJournal struct {
	Version       int                     `json:"version"`
	SourceRelease string                  `json:"source_release"`
	Phase         string                  `json:"phase"`
	SourceRoot    string                  `json:"source_root"`
	SourceSHA256  string                  `json:"source_sha256"`
	TargetRoot    string                  `json:"target_root"`
	StageRoot     string                  `json:"stage_root"`
	Rollback      RollbackReference       `json:"rollback"`
	Services      []ServiceState          `json:"services"`
	Domains       []DomainOutcome         `json:"domains"`
	Workspaces    []WorkspaceStageOutcome `json:"workspaces"`
	StagedSHA256  string                  `json:"staged_sha256,omitempty"`
	Activation    []ActivationOutcome     `json:"activation,omitempty"`
	Health        []HealthOutcome         `json:"health,omitempty"`
	CreatedAt     time.Time               `json:"created_at"`
	UpdatedAt     time.Time               `json:"updated_at"`
	CommittedAt   *time.Time              `json:"committed_at,omitempty"`
	FailureStage  string                  `json:"failure_stage,omitempty"`
}

type StageResult struct {
	StageRoot     string                  `json:"stage_root"`
	JournalPath   string                  `json:"journal_path"`
	SourceSHA256  string                  `json:"source_sha256"`
	StagedSHA256  string                  `json:"staged_sha256"`
	TargetRoot    string                  `json:"target_root"`
	Domains       []DomainOutcome         `json:"domains"`
	Workspaces    []WorkspaceStageOutcome `json:"workspaces"`
	Services      []ServiceState          `json:"services"`
	AlreadyStaged bool                    `json:"already_staged"`
}

func Stage(ctx context.Context, options StageOptions) (result StageResult, retErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	manifest := options.Manifest
	preServices := append([]ServiceState(nil), manifest.Services...)
	if err := validateStageManifest(manifest); err != nil {
		return StageResult{}, err
	}
	targetRoot, err := normalizeStageTarget(options.TargetRoot)
	if err != nil {
		return StageResult{}, err
	}
	stageRoot, journalPath, lockPath := stagePaths(targetRoot, manifest.SourceSHA256)
	if sameComparablePath(stageRoot, manifest.Source.Root) || withinPath(manifest.Source.Root, stageRoot) {
		return StageResult{}, errors.New("migration staging root overlaps released source")
	}
	lock, err := oslock.Acquire(lockPath, oslock.Exclusive)
	if err != nil {
		return StageResult{}, fmt.Errorf("acquire released migration lock: %w", err)
	}
	defer lock.Release()

	previous, journalExists, err := readStageJournal(journalPath)
	if err != nil {
		return StageResult{}, fmt.Errorf("read migration journal: %w", err)
	}
	if journalExists {
		if previous.SourceSHA256 != manifest.SourceSHA256 || !sameComparablePath(previous.SourceRoot, manifest.Source.Root) ||
			!sameComparablePath(previous.TargetRoot, targetRoot) || !sameComparablePath(previous.StageRoot, stageRoot) {
			return StageResult{}, errors.New("existing migration journal belongs to different released state")
		}
		if previous.Phase == stagePhaseStaged {
			if _, err := config.VerifyAt(stageRoot); err != nil {
				return StageResult{}, fmt.Errorf("verify existing staged state: %w", err)
			}
			hash, err := fingerprintStageTree(stageRoot)
			if err != nil {
				return StageResult{}, err
			}
			if hash != previous.StagedSHA256 {
				return StageResult{}, errors.New("existing staged state fingerprint does not match migration journal")
			}
			return stageResultFromJournal(previous, true), nil
		}
		if previous.Phase == stagePhaseCommitted {
			return StageResult{}, errors.New("released migration is already committed")
		}
		if previous.Phase == stagePhaseActivating || previous.Phase == stagePhaseActivationFailed {
			return StageResult{}, errors.New("released migration activation requires explicit recovery")
		}
		if previous.Phase == stagePhaseFailed {
			if err := removeOwnedStageRoot(stageRoot, previous); err != nil {
				return StageResult{}, err
			}
		} else {
			return StageResult{}, errors.New("incomplete migration stage requires explicit recovery")
		}
	} else if pathExists(stageRoot) {
		return StageResult{}, errors.New("migration staging path already exists without an ownership journal")
	}

	quiesceTargets := []ServiceState{}
	for _, state := range manifest.Services {
		if !serviceNeedsQuiesce(state) {
			continue
		}
		if state.Ownership != OwnershipVerified {
			return StageResult{}, fmt.Errorf("historical service %s may write released state but ownership is not verified", state.ID)
		}
		if strings.TrimSpace(state.Binary) == "" && state.Backend != "task-scheduler" {
			return StageResult{}, fmt.Errorf("historical service %s executable is not verified", state.ID)
		}
		quiesceTargets = append(quiesceTargets, state)
	}

	now := time.Now
	if options.Now != nil {
		now = options.Now
	}
	journal := StageJournal{
		Version: StageJournalVersion, SourceRelease: SourceRelease, Phase: stagePhaseQuiescing,
		SourceRoot: manifest.Source.Root, SourceSHA256: manifest.SourceSHA256,
		TargetRoot: targetRoot, StageRoot: stageRoot,
		Rollback: RollbackReference{SourceRoot: manifest.Source.Root, SourceSHA256: manifest.SourceSHA256, OperatorHome: manifest.Source.OperatorHome, TargetRoot: targetRoot, TargetExists: pathExists(targetRoot)},
		Services: preServices,
		Domains:  []DomainOutcome{}, Workspaces: []WorkspaceStageOutcome{},
		CreatedAt: now().UTC(), UpdatedAt: now().UTC(),
	}
	journal.Rollback.BundlePath = strings.TrimSpace(options.BundleSourcePath)
	journal.Rollback.BundleSHA256 = strings.TrimSpace(options.BundleSHA256)
	if err := writeStageJournal(journalPath, journal); err != nil {
		return StageResult{}, err
	}
	fail := func(point string, cause error) (StageResult, error) {
		journal.Phase = stagePhaseFailed
		journal.FailureStage = point
		journal.UpdatedAt = now().UTC()
		journalErr := writeStageJournal(journalPath, journal)
		cleanupErr := os.RemoveAll(stageRoot)
		return StageResult{}, errors.Join(cause, journalErr, cleanupErr)
	}

	controller := options.ServiceControl
	if controller == nil {
		controller = platformHistoricalServiceController{}
	}
	quiesced := []ServiceState{}
	for _, state := range quiesceTargets {
		quiesced = append(quiesced, state)
		if err := controller.Quiesce(ctx, manifest.Source, state); err != nil {
			_, failed := fail("quiesce", fmt.Errorf("quiesce historical service %s: %w", state.ID, err))
			_ = restoreHistoricalServices(ctx, controller, manifest.Source, quiesced)
			return StageResult{}, failed
		}
	}
	restoreOnFailure := true
	defer func() {
		if retErr == nil || !restoreOnFailure {
			return
		}
		if err := restoreHistoricalServices(ctx, controller, manifest.Source, quiesced); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("restore historical service after staging failure: %w", err))
		}
	}()

	fresh, err := refreshStageManifest(ctx, options, manifest)
	if err != nil {
		return fail("source-recheck", err)
	}
	if fresh.SourceSHA256 != manifest.SourceSHA256 {
		return fail("source-recheck", errors.New("released source changed between detection and quiescence"))
	}
	if fresh.Unsupported != 0 {
		return fail("source-recheck", errors.New("released source gained unsupported state after quiescence"))
	}
	manifest = fresh

	journal.Phase = stagePhaseStaging
	journal.UpdatedAt = now().UTC()
	if err := writeStageJournal(journalPath, journal); err != nil {
		return fail("journal", err)
	}
	inject := func(point string) error {
		if options.InjectFailure == nil {
			return nil
		}
		return options.InjectFailure(point)
	}

	if err := os.MkdirAll(stageRoot, 0700); err != nil {
		return fail("stage-root", err)
	}
	if err := configformat.MarkRoot(stageRoot); err != nil {
		return fail("root-marker", err)
	}
	if err := inject("root-marker"); err != nil {
		return fail("root-marker", err)
	}

	if err := stageConfiguration(manifest, stageRoot, &journal); err != nil {
		return fail("config", err)
	}
	if err := inject("config"); err != nil {
		return fail("config", err)
	}

	legacySecrets, err := OpenLegacySecretAccessor(manifest, manifest.Source.Root)
	if err != nil {
		return fail("credentials", err)
	}
	credentials, err := legacySecrets.Migrate(stageRoot)
	if err != nil {
		return fail("credentials", err)
	}
	journal.Domains = append(journal.Domains, DomainOutcome{Domain: "secrets", State: "staged", Detail: fmt.Sprintf("%d credential(s)", credentials.Migrated)})
	if err := canonicalizeStagedTunnel(stageRoot, credentials); err != nil {
		return fail("credentials", err)
	}
	if err := inject("credentials"); err != nil {
		return fail("credentials", err)
	}

	if manifest.Upstream.Servers > 0 {
		sourceUpstream, _, exists, err := discoverStructured(manifest.Source.Root, "upstream")
		if err != nil {
			return fail("upstream", err)
		}
		if !exists {
			return fail("upstream", errors.New("released upstream inventory disappeared after quiescence"))
		}
		_, err = upstream024.Transform(upstream024.Input{
			SourcePath: sourceUpstream, DestinationPath: filepath.Join(stageRoot, "upstream.json"),
			PreserveSource: true, StagedSecretRoot: stageRoot,
		})
		if err != nil {
			return fail("upstream", err)
		}
		journal.Domains = append(journal.Domains, DomainOutcome{Domain: "upstreams", State: "staged", Detail: fmt.Sprintf("%d server(s)", manifest.Upstream.Servers)})
	} else {
		journal.Domains = append(journal.Domains, DomainOutcome{Domain: "upstreams", State: "absent"})
	}
	if err := inject("upstream"); err != nil {
		return fail("upstream", err)
	}

	if err := stageOAuth(manifest.Source.Root, stageRoot); err != nil {
		return fail("oauth", err)
	}
	if pathExists(filepath.Join(stageRoot, "oauth.json")) {
		journal.Domains = append(journal.Domains, DomainOutcome{Domain: "oauth", State: "staged"})
	} else {
		journal.Domains = append(journal.Domains, DomainOutcome{Domain: "oauth", State: "absent"})
	}
	if err := inject("oauth"); err != nil {
		return fail("oauth", err)
	}

	if err := stageGlobalDurableArtifacts(manifest, stageRoot, &journal); err != nil {
		return fail("global-state", err)
	}
	if err := inject("global-state"); err != nil {
		return fail("global-state", err)
	}

	workspaces, err := stageWorkspaces(manifest, stageRoot, now)
	if err != nil {
		return fail("workspaces", err)
	}
	journal.Workspaces = workspaces
	journal.Domains = append(journal.Domains, DomainOutcome{Domain: "workspaces", State: "staged", Detail: fmt.Sprintf("%d workspace(s)", len(workspaces))})
	if err := inject("workspaces"); err != nil {
		return fail("workspaces", err)
	}

	account, err := service.InvokingAccount(service.ScopeUser)
	if err != nil {
		return fail("service-environment", err)
	}
	if _, err := service.SaveEnvironment(stageRoot, service.CaptureEnvironment(account, nil)); err != nil {
		return fail("service-environment", err)
	}
	journal.Domains = append(journal.Domains, DomainOutcome{Domain: "service-environment", State: "regenerated"})
	if err := inject("service-environment"); err != nil {
		return fail("service-environment", err)
	}

	if _, err := config.VerifyAt(stageRoot); err != nil {
		return fail("validate", fmt.Errorf("verify staged configuration: %w", err))
	}
	if err := validateStagedWorkspacePayloads(workspaces); err != nil {
		return fail("validate", err)
	}
	if err := validateStagedStructuredFiles(stageRoot); err != nil {
		return fail("validate", err)
	}
	if err := inject("validate"); err != nil {
		return fail("validate", err)
	}

	finalSource, err := refreshStageManifest(ctx, options, manifest)
	if err != nil {
		return fail("source-recheck", err)
	}
	if finalSource.SourceSHA256 != manifest.SourceSHA256 {
		return fail("source-recheck", errors.New("released source changed while staging"))
	}
	stagedSHA, err := fingerprintStageTree(stageRoot)
	if err != nil {
		return fail("fingerprint", err)
	}
	journal.StagedSHA256 = stagedSHA
	journal.Phase = stagePhaseStaged
	journal.UpdatedAt = now().UTC()
	if err := writeStageJournal(journalPath, journal); err != nil {
		return fail("journal", err)
	}
	restoreOnFailure = false
	return stageResultFromJournal(journal, false), nil
}

func validateStageManifest(manifest Manifest) error {
	if manifest.Version != ManifestVersion || manifest.Source.Release != SourceRelease {
		return errors.New("released migration manifest is not supported")
	}
	if !manifest.Found || strings.TrimSpace(manifest.Source.Root) == "" || !manifest.Source.Marker.Verified {
		return errors.New("released migration source is not authoritative")
	}
	if strings.TrimSpace(manifest.SourceSHA256) == "" {
		return errors.New("released migration manifest is missing source fingerprint")
	}
	if manifest.Unsupported != 0 {
		return fmt.Errorf("released migration manifest contains %d unsupported state item(s)", manifest.Unsupported)
	}
	knownServices := map[string]bool{}
	for _, id := range manifest.Source.HistoricalServiceIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			knownServices[id] = true
		}
	}
	for _, state := range manifest.Services {
		if !state.Installed && !state.Bootstrapped && !state.Enabled && !state.Running && state.Ownership == OwnershipAbsent {
			continue
		}
		if !knownServices[strings.TrimSpace(state.ID)] {
			return fmt.Errorf("historical service %s is not part of the released source descriptor", state.ID)
		}
		if !sameComparablePath(state.ConfigRoot, manifest.Source.Root) {
			return fmt.Errorf("historical service %s config root does not match the released source", state.ID)
		}
		if state.Ownership == OwnershipVerified && strings.TrimSpace(state.Binary) == "" {
			return fmt.Errorf("historical service %s executable is not verified", state.ID)
		}
	}
	return nil
}

func normalizeStageTarget(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = configformat.DefaultRootPath()
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	if absolute == string(filepath.Separator) || absolute == filepath.VolumeName(absolute)+string(filepath.Separator) {
		return "", errors.New("migration target cannot be a filesystem root")
	}
	return absolute, nil
}

func stagePaths(targetRoot, sourceSHA string) (string, string, string) {
	suffix := sourceSHA
	if len(suffix) > 12 {
		suffix = suffix[:12]
	}
	base := "." + filepath.Base(targetRoot) + ".migration-024-" + suffix
	parent := filepath.Dir(targetRoot)
	return filepath.Join(parent, base+".stage"), filepath.Join(parent, base+".journal.json"), filepath.Join(parent, base+".lock")
}

func serviceNeedsQuiesce(state ServiceState) bool {
	if !state.Installed && !state.Bootstrapped && !state.Running {
		return false
	}
	if state.Backend == "launchd" || state.Backend == "task-scheduler" {
		return state.Bootstrapped || state.Enabled || state.Running
	}
	return state.Running
}

func restoreHistoricalServices(ctx context.Context, controller HistoricalServiceController, source SourceDescriptor, states []ServiceState) error {
	var result error
	for index := len(states) - 1; index >= 0; index-- {
		result = errors.Join(result, controller.Restore(ctx, source, states[index]))
	}
	return result
}

func refreshStageManifest(ctx context.Context, options StageOptions, previous Manifest) (Manifest, error) {
	if options.RefreshManifest != nil {
		return options.RefreshManifest(ctx, previous)
	}
	bundles := make([]string, 0, len(previous.Bundles))
	for _, item := range previous.Bundles {
		bundles = append(bundles, item.Path)
	}
	return Detect(ctx, Options{SourceRoot: previous.Source.Root, HomeDir: previous.Source.OperatorHome, BundlePaths: bundles})
}

func stageConfiguration(manifest Manifest, stageRoot string, journal *StageJournal) error {
	if manifest.Config.Exists {
		result, err := integrations024.Transform(integrations024.Input{SourcePath: manifest.Config.Path, DestinationPath: filepath.Join(stageRoot, "config.json")})
		if err != nil {
			return err
		}
		state := "staged"
		if result.AlreadyApplied {
			state = "deduplicated"
		}
		journal.Domains = append(journal.Domains, DomainOutcome{Domain: "config", State: state})
		integrationState := "validated"
		if result.Migrated {
			integrationState = "migrated"
		}
		journal.Domains = append(journal.Domains, DomainOutcome{Domain: "integrations", State: integrationState})
		return nil
	}
	if err := config.SaveAt(stageRoot, config.Default()); err != nil {
		return err
	}
	journal.Domains = append(journal.Domains, DomainOutcome{Domain: "config", State: "generated-default"})
	journal.Domains = append(journal.Domains, DomainOutcome{Domain: "integrations", State: "default"})
	return nil
}

func canonicalizeStagedTunnel(stageRoot string, credentials credentials024.Result) error {
	cfg, err := config.LoadAt(stageRoot)
	if err != nil {
		return err
	}
	store := secretstore.New(stageRoot)
	if credentials.Tunnel.RuntimeKeyConfigured {
		cfg.Tunnel.APIKey, err = store.Get(secretstore.AccountName(secretstore.DomainTunnel, "runtime-key"))
		if err != nil {
			return err
		}
	} else {
		cfg.Tunnel.APIKey = ""
	}
	if credentials.Tunnel.Admin.KeyConfigured {
		cfg.Tunnel.Admin.Key, err = store.Get(secretstore.AccountName(secretstore.DomainTunnel, "admin-key"))
		if err != nil {
			return err
		}
	} else {
		cfg.Tunnel.Admin.Key = ""
	}
	tunnel.SetAdminEnabled(&cfg.Tunnel, credentials.Tunnel.Admin.Enabled)
	cfg.Tunnel.Admin.OrganizationID = credentials.Tunnel.Admin.OrganizationID
	cfg.Tunnel.Admin.WorkspaceID = credentials.Tunnel.Admin.WorkspaceID
	cfg.Tunnel.Admin.TenantID = credentials.Tunnel.Admin.TenantID
	tunnel.InvalidateAdminVerification(&cfg.Tunnel)
	if err := os.Remove(filepath.Join(stageRoot, "config.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return config.SaveAt(stageRoot, cfg)
}

func stageOAuth(sourceRoot, stageRoot string) error {
	source, _, exists, err := discoverStructured(sourceRoot, "oauth")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if !strings.EqualFold(filepath.Ext(source), ".json") {
		return errors.New("released OAuth state must be JSON")
	}
	destination := filepath.Join(stageRoot, "oauth.json")
	if err := copyRegularFile(source, destination); err != nil {
		return err
	}
	return oauth.NewStore(destination).Migrate()
}

func stageGlobalDurableArtifacts(manifest Manifest, stageRoot string, journal *StageJournal) error {
	instructions, logs, skippedLogs := 0, 0, 0
	tuiState := "absent"
	for _, artifact := range manifest.Artifacts {
		if artifact.Classification == ClassOptionalSkipWithReport {
			switch artifact.Kind {
			case "log":
				skippedLogs++
			case "tui-state":
				tuiState = "skipped"
			}
			continue
		}
		if artifact.Classification != ClassDurableMigrate {
			continue
		}
		source := filepath.Join(manifest.Source.Root, filepath.FromSlash(artifact.Path))
		switch artifact.Kind {
		case "instance-identity":
			if manifest.Instance.Valid {
				if err := copyRegularFile(source, filepath.Join(stageRoot, filepath.FromSlash(artifact.Path))); err != nil {
					return err
				}
			}
		case "instructions":
			if err := copyRegularFile(source, filepath.Join(stageRoot, filepath.FromSlash(artifact.Path))); err != nil {
				return err
			}
			instructions++
		case "log":
			if err := copyRegularFile(source, filepath.Join(stageRoot, filepath.FromSlash(artifact.Path))); err != nil {
				return err
			}
			logs++
		case "tui-state":
			if err := copyRegularFile(source, filepath.Join(stageRoot, filepath.FromSlash(artifact.Path))); err != nil {
				return err
			}
			tuiState = "staged"
		}
	}
	instanceState := "regenerated"
	if manifest.Instance.Valid {
		instanceState = "staged"
	} else if _, err := instance.NewStore(stageRoot).LoadOrCreate(); err != nil {
		return err
	}
	logState := "staged"
	if skippedLogs > 0 {
		logState = "staged-with-skips"
	}
	journal.Domains = append(journal.Domains,
		DomainOutcome{Domain: "instance", State: instanceState},
		DomainOutcome{Domain: "instructions", State: "staged", Detail: fmt.Sprintf("%d file(s)", instructions)},
		DomainOutcome{Domain: "logs", State: logState, Detail: fmt.Sprintf("%d staged, %d skipped", logs, skippedLogs)},
	)
	if tuiState != "absent" {
		detail := ""
		if tuiState == "skipped" {
			detail = "invalid or unsupported optional state"
		}
		journal.Domains = append(journal.Domains, DomainOutcome{Domain: "tui-state", State: tuiState, Detail: detail})
	}
	return nil
}

func stageWorkspaces(manifest Manifest, stageRoot string, now func() time.Time) ([]WorkspaceStageOutcome, error) {
	if strings.TrimSpace(manifest.Workspaces.Path) == "" {
		return []WorkspaceStageOutcome{}, nil
	}
	manager := workspace.NewManager(manifest.Workspaces.Path)
	registry, err := manager.InspectRegistry()
	if err != nil {
		return nil, err
	}
	if registry.Version != 4 {
		return nil, fmt.Errorf("released workspace registry version changed after detection: %d", registry.Version)
	}
	classifier := workspace.NewManager(workspace.DefaultStorePath())
	outcomes := make([]WorkspaceStageOutcome, 0, len(registry.Workspaces))
	for _, item := range registry.Workspaces {
		legacyID := releasedLegacyWorkspaceID(item)
		outcome := WorkspaceStageOutcome{ID: item.ID, LegacyID: legacyID, WorkspaceRoot: item.Path, Destination: filepath.Join(item.Path, workspace.LocalDirName)}
		classification := classifier.ClassifyRegisteredRoot(item)
		switch classification.State {
		case workspace.RegisteredRootMissing:
			outcome.State = "unavailable"
			outcome.Detail = "workspace root unavailable; registration retained"
			outcomes = append(outcomes, outcome)
			continue
		case workspace.RegisteredLocalRootAbsent, workspace.RegisteredLocalValidSameID:
		default:
			return nil, workspace.ReconnectClassificationError(classification, item.Path)
		}

		payloadBase := filepath.Join(stageRoot, "workspace-payloads", item.ID)
		transformRoot := filepath.Join(payloadBase, "transformed")
		desiredRoot := filepath.Join(payloadBase, "root", workspace.LocalDirName)
		if dirExists(filepath.Join(manifest.Source.Root, "workspaces", legacyID)) {
			transformed, err := workspace024.Transform(workspace024.Input{
				SourceConfigRoot: manifest.Source.Root, WorkspaceRoot: item.Path,
				LegacyWorkspaceID: legacyID, SourceWorkspaceID: legacyID, TargetWorkspaceID: item.ID,
				Destination: transformRoot,
			})
			if err != nil {
				return nil, fmt.Errorf("stage workspace %s: %w", item.ID, err)
			}
			outcome.SourceSHA256 = transformed.Manifest.SourceSHA256
			if err := copyWorkspaceTransformPayload(transformRoot, desiredRoot); err != nil {
				return nil, err
			}
		} else if err := os.MkdirAll(desiredRoot, 0700); err != nil {
			return nil, err
		}
		createdAt := now().UTC()
		if classification.State == workspace.RegisteredLocalValidSameID {
			identity, err := workspacestate.New(item.Path).LoadIdentity()
			if err != nil {
				return nil, err
			}
			createdAt = identity.CreatedAt
		}
		identity, err := workspacestate.NewIdentity(item.ID, createdAt)
		if err != nil {
			return nil, err
		}
		if err := workspacestate.WriteIdentitySnapshot(desiredRoot, identity); err != nil {
			return nil, err
		}
		outcome.StagePath = desiredRoot
		if classification.State == workspace.RegisteredLocalValidSameID {
			equal, err := treesEqual(outcome.Destination, desiredRoot)
			if err != nil {
				return nil, err
			}
			if !equal {
				return nil, &workspace.DuplicateWorkspaceIdentityError{WorkspaceID: item.ID, RegisteredRoot: item.Path, DestinationRoot: item.Path}
			}
			outcome.State = "deduplicated"
			outcome.Detail = "existing workspace local state is byte-identical"
		} else {
			outcome.State = "staged"
		}
		outcomes = append(outcomes, outcome)
	}
	if err := workspace.WriteRegistrySnapshot(filepath.Join(stageRoot, "workspaces.json"), registry.Workspaces, registry.ContainerItems); err != nil {
		return nil, err
	}
	sort.Slice(outcomes, func(i, j int) bool { return outcomes[i].ID < outcomes[j].ID })
	return outcomes, nil
}

func releasedLegacyWorkspaceID(item workspace.Workspace) string {
	if len(item.LegacyIDs) > 0 {
		return item.LegacyIDs[0]
	}
	return item.ID
}

func copyWorkspaceTransformPayload(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace staging output contains symlink: %s", path)
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return os.MkdirAll(destination, 0700)
		}
		if filepath.Base(path) == ".workspace024-manifest.json" {
			return nil
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		return copyRegularFile(path, target)
	})
}

func validateStagedWorkspacePayloads(outcomes []WorkspaceStageOutcome) error {
	for _, outcome := range outcomes {
		if outcome.State != "staged" && outcome.State != "deduplicated" {
			continue
		}
		store := workspacestate.New(filepath.Dir(outcome.StagePath))
		identity, err := store.LoadIdentity()
		if err != nil {
			return fmt.Errorf("validate staged workspace %s identity: %w", outcome.ID, err)
		}
		if identity.ID != outcome.ID {
			return fmt.Errorf("staged workspace %s identity mismatch", outcome.ID)
		}
	}
	return nil
}

func validateStagedStructuredFiles(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("staged state contains symlink: %s", path)
		}
		extension := strings.ToLower(filepath.Ext(path))
		if extension != ".json" && extension != ".jsonl" {
			return nil
		}
		data, err := readBoundedRegular(path, maxRegularFileBytes)
		if err != nil {
			return err
		}
		if extension == ".json" {
			if !json.Valid(bytes.TrimSpace(data)) {
				return fmt.Errorf("staged JSON is invalid: %s", path)
			}
			return nil
		}
		for _, line := range bytes.Split(data, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) > 0 && !json.Valid(line) {
				return fmt.Errorf("staged JSONL is invalid: %s", path)
			}
		}
		return nil
	})
}

func copyRegularFile(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("migration source is not a regular file: %s", source)
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	opened, err := in.Stat()
	if err != nil {
		return err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return fmt.Errorf("migration source changed while opening: %s", source)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	syncErr := out.Sync()
	closeErr := out.Close()
	return errors.Join(copyErr, syncErr, closeErr)
}

func treesEqual(left, right string) (bool, error) {
	leftHash, err := fingerprintExactTree(left)
	if err != nil {
		return false, err
	}
	rightHash, err := fingerprintExactTree(right)
	if err != nil {
		return false, err
	}
	return leftHash == rightHash, nil
}

func fingerprintExactTree(root string) (string, error) {
	type record struct{ path, sum string }
	records := []record{}
	entries := 0
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path != root {
			entries++
			if entries > maxInventoryEntries {
				return fmt.Errorf("state tree exceeds %d entries", maxInventoryEntries)
			}
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("state tree contains symlink: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("state tree contains non-regular file: %s", path)
		}
		if info.Size() > maxRegularFileBytes {
			return fmt.Errorf("state tree file exceeds fingerprint limit: %s", path)
		}
		total += info.Size()
		if total > maxFingerprintBytes {
			return fmt.Errorf("state tree exceeds %d fingerprint bytes", maxFingerprintBytes)
		}
		sum, _, err := hashFile(path, maxRegularFileBytes)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		records = append(records, record{path: filepath.ToSlash(relative), sum: sum})
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

func fingerprintStageTree(root string) (string, error) {
	return fingerprintExactTree(root)
}

func writeStageJournal(path string, journal StageJournal) error {
	data, err := statepkg.MarshalJSON(journal)
	if err != nil {
		return err
	}
	if len(data) > maxStageJournalBytes {
		return errors.New("migration journal exceeds size limit")
	}
	return statepkg.WriteFileAtomic(path, data, 0600)
}

func loadStageJournal(path string) (StageJournal, bool) {
	journal, exists, err := readStageJournal(path)
	if err != nil {
		return StageJournal{}, false
	}
	return journal, exists
}

func readStageJournal(path string) (StageJournal, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return StageJournal{}, false, nil
	}
	if err != nil {
		return StageJournal{}, false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return StageJournal{}, false, errors.New("migration journal must be a regular non-symlink file")
	}
	if info.Size() > maxStageJournalBytes {
		return StageJournal{}, false, errors.New("migration journal exceeds size limit")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return StageJournal{}, false, err
	}
	var journal StageJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		return StageJournal{}, false, fmt.Errorf("decode migration journal: %w", err)
	}
	if journal.Version != StageJournalVersion {
		return StageJournal{}, false, fmt.Errorf("unsupported migration journal version: %d", journal.Version)
	}
	if journal.SourceRelease != SourceRelease || strings.TrimSpace(journal.SourceRoot) == "" ||
		strings.TrimSpace(journal.SourceSHA256) == "" || strings.TrimSpace(journal.TargetRoot) == "" ||
		strings.TrimSpace(journal.StageRoot) == "" {
		return StageJournal{}, false, errors.New("migration journal is incomplete")
	}
	switch journal.Phase {
	case stagePhaseQuiescing, stagePhaseStaging, stagePhaseStaged, stagePhaseActivating, stagePhaseActivationFailed, stagePhaseCommitted, stagePhaseFailed:
	default:
		return StageJournal{}, false, fmt.Errorf("unsupported migration journal phase: %q", journal.Phase)
	}
	return journal, true, nil
}

func removeOwnedStageRoot(stageRoot string, journal StageJournal) error {
	if !sameComparablePath(stageRoot, journal.StageRoot) {
		return errors.New("refusing to remove stage root not owned by migration journal")
	}
	return os.RemoveAll(stageRoot)
}

func stageResultFromJournal(journal StageJournal, already bool) StageResult {
	return StageResult{
		StageRoot: journal.StageRoot, JournalPath: stageJournalPath(journal), SourceSHA256: journal.SourceSHA256,
		StagedSHA256: journal.StagedSHA256, TargetRoot: journal.TargetRoot,
		Domains: append([]DomainOutcome(nil), journal.Domains...), Workspaces: append([]WorkspaceStageOutcome(nil), journal.Workspaces...),
		Services: append([]ServiceState(nil), journal.Services...), AlreadyStaged: already,
	}
}

func stageJournalPath(journal StageJournal) string {
	_, path, _ := stagePaths(journal.TargetRoot, journal.SourceSHA256)
	return path
}

func sameComparablePath(left, right string) bool {
	return comparablePath(left) == comparablePath(right)
}

func withinPath(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
