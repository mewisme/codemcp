package released024

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/install"
	codegraph "go.mewis.me/codemcp/internal/integrations/codegraph"
	rtk "go.mewis.me/codemcp/internal/integrations/rtk"
	typesafeintegration "go.mewis.me/codemcp/internal/integrations/typesafe"
	"go.mewis.me/codemcp/internal/oslock"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	"go.mewis.me/codemcp/internal/secretinventory"
	"go.mewis.me/codemcp/internal/secretstore"
	"go.mewis.me/codemcp/internal/service"
	"go.mewis.me/codemcp/internal/upstream"
	versionpkg "go.mewis.me/codemcp/internal/version"
	"go.mewis.me/codemcp/internal/workspace"
)

type ActivationOutcome struct {
	Stage  string `json:"stage"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

type HealthOutcome struct {
	Component string `json:"component"`
	State     string `json:"state"`
	Detail    string `json:"detail,omitempty"`
}

type ActivationEvent struct {
	Stage   string `json:"stage"`
	State   string `json:"state"`
	Message string `json:"message"`
	Child   bool   `json:"child,omitempty"`
}

type CommandActivation struct {
	Binary   string
	Rollback func(context.Context) error
}

type InstallCommandFunc func(context.Context, install.Layout, string, string) (CommandActivation, error)
type HealthCheckFunc func(context.Context, string, []WorkspaceStageOutcome) ([]HealthOutcome, error)

type ActivateOptions struct {
	JournalPath              string
	ServiceScope             service.Scope
	InstallLayout            install.Layout
	InstallVersion           string
	InstallSource            string
	ReadyTimeout             time.Duration
	ServiceManager           service.Manager
	RuntimeProbe             service.RuntimeProbe
	RuntimeShutdown          service.RuntimeShutdown
	RuntimeWait              service.RuntimeStatusWait
	HistoricalServiceControl HistoricalServiceController
	InstallCommand           InstallCommandFunc
	HealthCheck              HealthCheckFunc
	Observe                  func(ActivationEvent)
	InjectFailure            func(string) error
	Now                      func() time.Time
}

type ActivationResult struct {
	TargetRoot string                       `json:"target_root"`
	ServiceID  string                       `json:"service_id"`
	Scope      service.Scope                `json:"scope"`
	Binary     string                       `json:"binary"`
	Runtime    runtimecontrol.RuntimeStatus `json:"runtime"`
	Activation []ActivationOutcome          `json:"activation"`
	Health     []HealthOutcome              `json:"health"`
	Committed  bool                         `json:"committed"`
}

type preparedWorkspaceActivation struct {
	outcome WorkspaceStageOutcome
	temp    string
	hash    string
	active  bool
	info    os.FileInfo
}

type activationState struct {
	journal                  StageJournal
	journalPath              string
	rootPublished            bool
	rootInfo                 os.FileInfo
	workspaces               []*preparedWorkspaceActivation
	command                  CommandActivation
	commandActivated         bool
	serviceManager           service.Manager
	serviceSpec              service.Spec
	serviceBefore            service.Status
	serviceMatches           bool
	serviceTouched           bool
	historicalServiceControl HistoricalServiceController
}

func Activate(ctx context.Context, options ActivateOptions) (result ActivationResult, retErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	journalPath := strings.TrimSpace(options.JournalPath)
	if journalPath == "" {
		return ActivationResult{}, errors.New("migration activation journal path is required")
	}
	journal, exists, err := readStageJournal(journalPath)
	if err != nil {
		return ActivationResult{}, err
	}
	if !exists {
		return ActivationResult{}, errors.New("migration activation journal does not exist")
	}
	if expected := stageJournalPath(journal); !sameComparablePath(expected, journalPath) {
		return ActivationResult{}, errors.New("migration activation journal path does not match staged transaction")
	}
	_, _, lockPath := stagePaths(journal.TargetRoot, journal.SourceSHA256)
	lock, err := oslock.Acquire(lockPath, oslock.Exclusive)
	if err != nil {
		return ActivationResult{}, fmt.Errorf("acquire migration activation lock: %w", err)
	}
	defer lock.Release()

	journal, exists, err = readStageJournal(journalPath)
	if err != nil || !exists {
		if err == nil {
			err = errors.New("migration activation journal disappeared")
		}
		return ActivationResult{}, err
	}
	if journal.Phase == stagePhaseCommitted {
		return activationResultFromJournal(journal), nil
	}
	if journal.Phase != stagePhaseStaged {
		return ActivationResult{}, fmt.Errorf("migration activation requires staged journal, got %q", journal.Phase)
	}
	if journal.Rollback.TargetExists || pathExists(journal.TargetRoot) {
		return ActivationResult{}, errors.New("migration target already exists; refusing to replace existing CodeMCP state")
	}
	if sameComparablePath(journal.TargetRoot, journal.SourceRoot) || withinPath(journal.SourceRoot, journal.TargetRoot) {
		return ActivationResult{}, errors.New("migration target overlaps released mutable state")
	}
	if !configformat.IsManagedRoot(journal.StageRoot) {
		return ActivationResult{}, errors.New("staged CodeMCP root marker is missing")
	}
	stagedHash, err := fingerprintStageTree(journal.StageRoot)
	if err != nil {
		return ActivationResult{}, err
	}
	if stagedHash != journal.StagedSHA256 {
		return ActivationResult{}, errors.New("staged CodeMCP state fingerprint does not match migration journal")
	}

	now := time.Now
	if options.Now != nil {
		now = options.Now
	}
	emit := func(stage, state, message string, child bool) {
		if options.Observe != nil {
			options.Observe(ActivationEvent{Stage: stage, State: state, Message: message, Child: child})
		}
	}
	record := func(stage, state, detail string) {
		journal.Activation = append(journal.Activation, ActivationOutcome{Stage: stage, State: state, Detail: detail})
	}
	inject := func(point string) error {
		if options.InjectFailure == nil {
			return nil
		}
		return options.InjectFailure(point)
	}

	historicalServiceControl := options.HistoricalServiceControl
	if historicalServiceControl == nil {
		historicalServiceControl = platformHistoricalServiceController{}
	}
	state := &activationState{journal: journal, journalPath: journalPath, historicalServiceControl: historicalServiceControl}
	fail := func(point string, cause error) (ActivationResult, error) {
		emit(point, "failed", cause.Error(), false)
		record(point, "failed", cause.Error())
		journal.FailureStage = point
		state.journal = journal
		emit("rollback", "running", "Rolling back migrated CodeMCP activation", false)
		rollbackErr := rollbackActivation(ctx, state)
		journal = state.journal
		journal.FailureStage = point
		journal.UpdatedAt = now().UTC()
		if rollbackErr == nil {
			journal.Phase = stagePhaseStaged
			journal.Activation = append(journal.Activation, ActivationOutcome{Stage: "rollback", State: "success", Detail: "activation state restored"})
			emit("rollback", "success", "Migrated CodeMCP activation rolled back", false)
		} else {
			journal.Phase = stagePhaseActivationFailed
			journal.Activation = append(journal.Activation, ActivationOutcome{Stage: "rollback", State: "failed", Detail: rollbackErr.Error()})
			emit("rollback", "failed", "Migration rollback requires recovery", false)
		}
		journalErr := writeStageJournal(journalPath, journal)
		return ActivationResult{}, errors.Join(cause, rollbackErr, journalErr)
	}

	journal.Phase = stagePhaseActivating
	journal.Activation = []ActivationOutcome{}
	journal.Health = nil
	journal.CommittedAt = nil
	journal.FailureStage = ""
	journal.UpdatedAt = now().UTC()
	if err := writeStageJournal(journalPath, journal); err != nil {
		return ActivationResult{}, err
	}
	state.journal = journal

	emit("workspaces", "running", "Preparing workspace-local migration outputs", false)
	prepared, workspaceOutcomes, err := prepareWorkspaceActivation(journal)
	if err != nil {
		return fail("workspaces", err)
	}
	state.workspaces = prepared
	for _, outcome := range workspaceOutcomes {
		record("workspace:"+outcome.ID, outcome.State, outcome.Detail)
		emit("workspaces", outcome.State, workspaceActivationMessage(outcome), true)
	}
	record("workspaces", "success", fmt.Sprintf("%d workspace outcome(s)", len(workspaceOutcomes)))
	emit("workspaces", "success", "Workspace-local migration outputs prepared", false)
	if err := inject("workspaces-prepared"); err != nil {
		return fail("workspaces", err)
	}

	emit("secrets-bind", "running", "Binding staged secrets to the final CodeMCP root", false)
	boundSecrets, err := bindStagedSecrets(journal.StageRoot, journal.TargetRoot)
	if err != nil {
		return fail("secrets-bind", err)
	}
	record("secrets-bind", "success", fmt.Sprintf("%d secret(s) bound", boundSecrets))
	emit("secrets-bind", "success", "Staged secrets bound to final CodeMCP identity", false)
	if err := inject("secrets-bound"); err != nil {
		return fail("secrets-bind", err)
	}

	emit("publish", "running", "Publishing CodeMCP state root", false)
	if err := os.Rename(journal.StageRoot, journal.TargetRoot); err != nil {
		return fail("publish", fmt.Errorf("publish CodeMCP state root: %w", err))
	}
	state.rootPublished = true
	state.rootInfo, err = os.Stat(journal.TargetRoot)
	if err != nil {
		return fail("publish", err)
	}
	if !configformat.IsManagedRoot(journal.TargetRoot) {
		return fail("publish", errors.New("published CodeMCP state root marker is invalid"))
	}
	record("publish", "success", journal.TargetRoot)
	emit("publish", "success", "CodeMCP state root published", false)
	if err := inject("root-published"); err != nil {
		return fail("publish", err)
	}

	emit("workspaces-activate", "running", "Activating workspace-local state", false)
	for _, item := range state.workspaces {
		if item.temp == "" {
			continue
		}
		classification := workspace.NewManager(workspace.DefaultStorePath()).ClassifyRegisteredRoot(workspace.Workspace{ID: item.outcome.ID, Path: item.outcome.WorkspaceRoot})
		if classification.State != workspace.RegisteredLocalRootAbsent {
			_ = os.RemoveAll(item.temp)
			item.temp = ""
			detail := "destination changed after staging; left untouched"
			record("workspace:"+item.outcome.ID, "warning", detail)
			emit("workspaces-activate", "warning", item.outcome.ID+" · "+detail, true)
			continue
		}
		if err := os.Rename(item.temp, item.outcome.Destination); err != nil {
			return fail("workspaces-activate", fmt.Errorf("activate workspace %s: %w", item.outcome.ID, err))
		}
		item.temp = ""
		item.active = true
		item.info, err = os.Stat(item.outcome.Destination)
		if err != nil {
			return fail("workspaces-activate", err)
		}
		record("workspace:"+item.outcome.ID, "success", "workspace-local state activated")
		emit("workspaces-activate", "success", item.outcome.ID+" · local state activated", true)
	}
	record("workspaces-activate", "success", "workspace-local activation complete")
	emit("workspaces-activate", "success", "Workspace-local state activation complete", false)
	if err := inject("workspaces-activated"); err != nil {
		return fail("workspaces-activate", err)
	}

	emit("install", "running", "Installing canonical cm command", false)
	installCommand := options.InstallCommand
	if installCommand == nil {
		installCommand = defaultInstallCommand
	}
	command, err := installCommand(ctx, options.InstallLayout, options.InstallVersion, options.InstallSource)
	if err != nil {
		return fail("install", err)
	}
	if strings.TrimSpace(command.Binary) == "" {
		return fail("install", errors.New("canonical CodeMCP command activation returned no binary"))
	}
	state.command = command
	state.commandActivated = true
	record("install", "success", command.Binary)
	emit("install", "success", "Canonical cm command installed", false)
	if err := inject("command-installed"); err != nil {
		return fail("install", err)
	}

	emit("health", "running", "Checking migrated CodeMCP state", false)
	healthCheck := options.HealthCheck
	if healthCheck == nil {
		healthCheck = defaultMigrationHealthCheck
	}
	health, err := healthCheck(ctx, journal.TargetRoot, journal.Workspaces)
	journal.Health = append([]HealthOutcome(nil), health...)
	state.journal.Health = append([]HealthOutcome(nil), health...)
	for _, outcome := range health {
		emit("health", outcome.State, outcome.Component+" · "+outcome.Detail, true)
	}
	if err != nil {
		return fail("health", err)
	}
	record("health", "success", fmt.Sprintf("%d check(s)", len(health)))
	emit("health", "success", "Migrated CodeMCP state checks passed", false)
	if err := inject("health"); err != nil {
		return fail("health", err)
	}

	scope := options.ServiceScope
	if scope == "" {
		scope = service.DetectScope()
	}
	account, err := service.InvokingAccountContext(ctx, scope)
	if err != nil {
		return fail("service-environment", err)
	}
	cfg, err := config.LoadAt(journal.TargetRoot)
	if err != nil {
		return fail("service-environment", err)
	}
	emit("service-environment", "running", "Regenerating managed service environment", false)
	environmentHash, err := service.SaveEnvironmentContext(ctx, journal.TargetRoot, service.CaptureEnvironment(account, cfg.Shell.Path))
	if err != nil {
		return fail("service-environment", err)
	}
	spec, err := service.NewSpecContext(ctx, journal.TargetRoot, command.Binary, scope, account)
	if err != nil {
		return fail("service-environment", err)
	}
	spec.EnvironmentHash = environmentHash
	record("service-environment", "success", "canonical environment snapshot bound")
	emit("service-environment", "success", "Managed service environment regenerated", false)
	if err := inject("service-environment"); err != nil {
		return fail("service-environment", err)
	}

	manager := options.ServiceManager
	if manager == nil {
		manager = service.NewManager()
	}
	state.serviceManager = manager
	state.serviceSpec = spec
	state.serviceBefore, err = manager.Status(spec)
	if err != nil {
		return fail("service", err)
	}
	if state.serviceBefore.Running {
		return fail("service", errors.New("canonical CodeMCP service was already running before migration activation"))
	}
	if state.serviceBefore.Installed {
		state.serviceMatches, err = manager.DefinitionMatches(spec)
		if err != nil {
			return fail("service", err)
		}
		if !state.serviceMatches {
			return fail("service", errors.New("pre-existing CodeMCP service definition conflicts with migrated activation"))
		}
	}

	probe := options.RuntimeProbe
	if probe == nil {
		probe = runtimeProbeAt(journal.TargetRoot)
	}
	shutdown := options.RuntimeShutdown
	if shutdown == nil {
		shutdown = runtimeShutdownAt(journal.TargetRoot)
	}
	wait := options.RuntimeWait
	if wait == nil {
		wait = func(waitCtx context.Context, lifecycle string) (runtimecontrol.RuntimeStatus, error) {
			return runtimecontrol.WaitStatusChangeAt(waitCtx, journal.TargetRoot, lifecycle)
		}
	}
	emit("service", "running", "Starting canonical CodeMCP managed service", false)
	lifecycle := service.Lifecycle{
		Manager: manager, Spec: spec, Probe: probe, Shutdown: shutdown, WaitStatusChange: wait,
		Timeout: options.ReadyTimeout,
		Observe: func(event service.LifecycleEvent) { emit("service", "running", event.Message, true) },
	}
	state.serviceTouched = true
	lifecycleResult, err := lifecycle.Up(ctx)
	if err != nil {
		return fail("service", err)
	}
	record("service", "success", spec.ID)
	emit("service", "success", "Canonical CodeMCP managed service started", false)
	if err := inject("service-started"); err != nil {
		return fail("service", err)
	}

	emit("readiness", "running", "Verifying migrated runtime ownership and config root", false)
	runtimeStatus := lifecycleResult.Status
	if !sameComparablePath(runtimeStatus.ConfigRoot, journal.TargetRoot) {
		return fail("readiness", fmt.Errorf("managed runtime config root mismatch: got %s, want %s", runtimeStatus.ConfigRoot, journal.TargetRoot))
	}
	if sameComparablePath(runtimeStatus.ConfigRoot, journal.SourceRoot) {
		return fail("readiness", errors.New("managed runtime is still bound to released mutable state"))
	}
	record("readiness", "success", fmt.Sprintf("pid %d", runtimeStatus.PID))
	emit("readiness", "success", "Migrated runtime is ready", false)
	if err := inject("readiness"); err != nil {
		return fail("readiness", err)
	}

	committedAt := now().UTC()
	journal.Health = append([]HealthOutcome(nil), health...)
	journal.Canonical = CanonicalActivationReference{
		ServiceID: spec.ID, Scope: string(scope), Binary: command.Binary,
		EnvironmentHash: spec.EnvironmentHash, ConfigRoot: journal.TargetRoot, RunID: runtimeStatus.RunID,
	}
	journal.Phase = stagePhaseCommitted
	journal.FailureStage = ""
	journal.CommittedAt = &committedAt
	journal.UpdatedAt = committedAt
	if err := writeStageJournal(journalPath, journal); err != nil {
		return fail("commit", err)
	}
	emit("commit", "success", "Migrated CodeMCP state committed", false)
	return ActivationResult{
		TargetRoot: journal.TargetRoot, ServiceID: spec.ID, Scope: scope, Binary: command.Binary,
		Runtime: runtimeStatus, Activation: append([]ActivationOutcome(nil), journal.Activation...),
		Health: append([]HealthOutcome(nil), health...), Committed: true,
	}, nil
}

func prepareWorkspaceActivation(journal StageJournal) ([]*preparedWorkspaceActivation, []WorkspaceStageOutcome, error) {
	prepared := make([]*preparedWorkspaceActivation, 0, len(journal.Workspaces))
	outcomes := make([]WorkspaceStageOutcome, 0, len(journal.Workspaces))
	classifier := workspace.NewManager(workspace.DefaultStorePath())
	for _, outcome := range journal.Workspaces {
		copy := outcome
		classification := classifier.ClassifyRegisteredRoot(workspace.Workspace{ID: outcome.ID, Path: outcome.WorkspaceRoot})
		switch outcome.State {
		case "unavailable":
			copy.State = "warning"
			copy.Detail = "workspace remained unavailable; registration preserved"
			outcomes = append(outcomes, copy)
			prepared = append(prepared, &preparedWorkspaceActivation{outcome: outcome})
			continue
		case "deduplicated":
			if classification.State == workspace.RegisteredLocalValidSameID {
				equal, err := treesEqual(outcome.Destination, outcome.StagePath)
				if err == nil && equal {
					copy.State = "success"
					copy.Detail = "existing workspace local state is byte-identical"
					outcomes = append(outcomes, copy)
					prepared = append(prepared, &preparedWorkspaceActivation{outcome: outcome})
					continue
				}
			}
			copy.State = "warning"
			copy.Detail = "workspace local state changed after staging; left untouched"
			outcomes = append(outcomes, copy)
			prepared = append(prepared, &preparedWorkspaceActivation{outcome: outcome})
			continue
		case "staged":
			if classification.State == workspace.RegisteredRootMissing {
				copy.State = "warning"
				copy.Detail = "workspace became unavailable after staging; left untouched"
				outcomes = append(outcomes, copy)
				prepared = append(prepared, &preparedWorkspaceActivation{outcome: outcome})
				continue
			}
			if classification.State != workspace.RegisteredLocalRootAbsent {
				copy.State = "warning"
				copy.Detail = "workspace destination ownership changed after staging; left untouched"
				outcomes = append(outcomes, copy)
				prepared = append(prepared, &preparedWorkspaceActivation{outcome: outcome})
				continue
			}
		default:
			return nil, nil, fmt.Errorf("unsupported staged workspace outcome %q for %s", outcome.State, outcome.ID)
		}
		if strings.TrimSpace(outcome.StagePath) == "" || !dirExists(outcome.StagePath) {
			return nil, nil, fmt.Errorf("staged workspace payload is unavailable for %s", outcome.ID)
		}
		hash, err := fingerprintExactTree(outcome.StagePath)
		if err != nil {
			return nil, nil, err
		}
		temp, err := os.MkdirTemp(outcome.WorkspaceRoot, ".cm-migration-024-*")
		if err != nil {
			return nil, nil, err
		}
		if err := copyWorkspaceTransformPayload(outcome.StagePath, temp); err != nil {
			_ = os.RemoveAll(temp)
			return nil, nil, err
		}
		copiedHash, err := fingerprintExactTree(temp)
		if err != nil || copiedHash != hash {
			_ = os.RemoveAll(temp)
			if err == nil {
				err = errors.New("workspace activation copy fingerprint mismatch")
			}
			return nil, nil, err
		}
		copy.State = "prepared"
		copy.Detail = "workspace-local state prepared for atomic activation"
		outcomes = append(outcomes, copy)
		prepared = append(prepared, &preparedWorkspaceActivation{outcome: outcome, temp: temp, hash: hash})
	}
	return prepared, outcomes, nil
}

func rollbackActivation(ctx context.Context, state *activationState) error {
	if state == nil {
		return errors.New("activation rollback state is unavailable")
	}
	var result error
	if state.serviceTouched && state.serviceManager != nil && state.serviceSpec.ID != "" {
		status, err := state.serviceManager.Status(state.serviceSpec)
		if err == nil && status.Running {
			result = errors.Join(result, service.StopBackend(state.serviceManager, state.serviceSpec))
		}
		if !state.serviceBefore.Installed {
			status, err = state.serviceManager.Status(state.serviceSpec)
			if err == nil && status.Installed {
				result = errors.Join(result, state.serviceManager.Uninstall(state.serviceSpec))
			}
		}
	}
	if state.commandActivated && state.command.Rollback != nil {
		result = errors.Join(result, state.command.Rollback(ctx))
	}
	for index := len(state.workspaces) - 1; index >= 0; index-- {
		item := state.workspaces[index]
		if item.temp != "" {
			result = errors.Join(result, os.RemoveAll(item.temp))
			item.temp = ""
		}
		if !item.active {
			continue
		}
		current, err := os.Stat(item.outcome.Destination)
		if err != nil {
			result = errors.Join(result, fmt.Errorf("inspect activated workspace %s: %w", item.outcome.ID, err))
			continue
		}
		if item.info == nil || !os.SameFile(item.info, current) {
			result = errors.Join(result, fmt.Errorf("workspace %s activation ownership changed; refusing rollback removal", item.outcome.ID))
			continue
		}
		hash, err := fingerprintExactTree(item.outcome.Destination)
		if err != nil || hash != item.hash {
			if err == nil {
				err = errors.New("workspace local state changed after activation")
			}
			result = errors.Join(result, fmt.Errorf("workspace %s rollback requires recovery: %w", item.outcome.ID, err))
			continue
		}
		result = errors.Join(result, os.RemoveAll(item.outcome.Destination))
		item.active = false
	}
	if state.rootPublished {
		current, err := os.Stat(state.journal.TargetRoot)
		if err != nil {
			result = errors.Join(result, err)
		} else if state.rootInfo == nil || !os.SameFile(state.rootInfo, current) {
			result = errors.Join(result, errors.New("published CodeMCP root ownership changed; refusing rollback rename"))
		} else if pathExists(state.journal.StageRoot) {
			result = errors.Join(result, errors.New("migration stage root reappeared before rollback"))
		} else if err := os.Rename(state.journal.TargetRoot, state.journal.StageRoot); err != nil {
			result = errors.Join(result, err)
		} else {
			state.rootPublished = false
			if hash, hashErr := fingerprintStageTree(state.journal.StageRoot); hashErr != nil {
				result = errors.Join(result, hashErr)
			} else {
				state.journal.StagedSHA256 = hash
			}
		}
	}
	if !state.rootPublished && dirExists(state.journal.StageRoot) {
		if hash, hashErr := fingerprintStageTree(state.journal.StageRoot); hashErr != nil {
			result = errors.Join(result, hashErr)
		} else {
			state.journal.StagedSHA256 = hash
		}
	}
	if !state.rootPublished {
		source := SourceDescriptor{Release: SourceRelease, Root: state.journal.SourceRoot, OperatorHome: state.journal.Rollback.OperatorHome}
		controller := state.historicalServiceControl
		if controller == nil {
			controller = platformHistoricalServiceController{}
		}
		result = errors.Join(result, restoreHistoricalServices(ctx, controller, source, state.journal.Services))
	}
	return result
}

func bindStagedSecrets(stageRoot, targetRoot string) (int, error) {
	inventory, err := secretinventory.Inventory(stageRoot)
	if err != nil {
		return 0, err
	}
	source := secretstore.New(stageRoot)
	target := secretstore.NewForIdentity(stageRoot, targetRoot)
	changes := make([]secretstore.Change, 0, len(inventory))
	for _, descriptor := range inventory {
		if !descriptor.Present {
			continue
		}
		value, err := source.Get(descriptor.Account)
		if err != nil {
			return 0, fmt.Errorf("read staged %s secret: %w", descriptor.Domain, err)
		}
		current, err := target.Get(descriptor.Account)
		switch {
		case errors.Is(err, secretstore.ErrNotFound):
			changes = append(changes, secretstore.Change{Name: descriptor.Account, Value: value})
		case err != nil:
			return 0, fmt.Errorf("inspect final %s secret identity: %w", descriptor.Domain, err)
		case current != value:
			return 0, fmt.Errorf("final %s secret identity conflicts with staged state", descriptor.Domain)
		}
	}
	if err := target.Apply(changes); err != nil {
		return 0, fmt.Errorf("bind staged secrets to final CodeMCP root: %w", err)
	}
	for _, descriptor := range inventory {
		if !descriptor.Present {
			continue
		}
		left, err := source.Get(descriptor.Account)
		if err != nil {
			return 0, err
		}
		right, err := target.Get(descriptor.Account)
		if err != nil || left != right {
			if err == nil {
				err = errors.New("secret content mismatch")
			}
			return 0, fmt.Errorf("verify final %s secret identity: %w", descriptor.Domain, err)
		}
	}
	return len(changes), nil
}

func defaultInstallCommand(ctx context.Context, layout install.Layout, version, source string) (CommandActivation, error) {
	if strings.TrimSpace(layout.Root) == "" {
		var err error
		layout, err = install.DefaultLayout()
		if err != nil {
			return CommandActivation{}, err
		}
	}
	canonicalBefore, err := install.StatusCanonical(layout)
	if err != nil {
		return CommandActivation{}, err
	}
	version = strings.TrimSpace(version)
	if version == "" {
		version = versionpkg.Version
	}
	installed, err := install.Install(install.Options{
		Context: ctx, Layout: layout, Version: version, Source: source, Force: versionpkg.IsDevelopment(version),
	})
	if err != nil {
		return CommandActivation{}, err
	}
	rollback := func(rollbackCtx context.Context) error {
		err := install.RollbackResultContext(rollbackCtx, installed)
		if canonicalBefore.State == install.CanonicalMissing {
			_, removeErr := install.RemoveCanonical(installed.Layout)
			err = errors.Join(err, removeErr)
		}
		return err
	}
	return CommandActivation{Binary: installed.Layout.CurrentBinary, Rollback: rollback}, nil
}

func defaultMigrationHealthCheck(_ context.Context, root string, workspaces []WorkspaceStageOutcome) ([]HealthOutcome, error) {
	outcomes := []HealthOutcome{}
	appendOutcome := func(component, state, detail string) {
		outcomes = append(outcomes, HealthOutcome{Component: component, State: state, Detail: detail})
	}
	if _, err := config.VerifyAt(root); err != nil {
		appendOutcome("config", "failed", err.Error())
		return outcomes, err
	}
	cfg, err := config.LoadAt(root)
	if err != nil {
		appendOutcome("config", "failed", err.Error())
		return outcomes, err
	}
	appendOutcome("config", "success", "canonical structured state verified")

	inventory, err := secretinventory.Inventory(root)
	if err != nil {
		appendOutcome("secrets", "failed", err.Error())
		return outcomes, err
	}
	missing := 0
	for _, descriptor := range inventory {
		if descriptor.Configured && descriptor.RuntimeRequired && !descriptor.Present {
			missing++
		}
	}
	if missing > 0 {
		err := fmt.Errorf("%d configured runtime secret(s) are missing", missing)
		appendOutcome("secrets", "failed", err.Error())
		return outcomes, err
	}
	appendOutcome("secrets", "success", fmt.Sprintf("%d managed secret descriptor(s) checked", len(inventory)))

	registry := workspace.NewManager(configformat.StructuredPath(root, "workspaces"))
	inspection, err := registry.InspectRegistry()
	if err != nil {
		appendOutcome("workspaces", "failed", err.Error())
		return outcomes, err
	}
	warnings := 0
	for _, item := range workspaces {
		if item.State == "unavailable" {
			warnings++
		}
	}
	if warnings > 0 {
		appendOutcome("workspaces", "warning", fmt.Sprintf("%d registered workspace(s) remain unavailable", warnings))
	} else {
		appendOutcome("workspaces", "success", fmt.Sprintf("%d registered workspace(s) readable", len(inspection.Workspaces)))
	}

	servers, err := upstream.NewStore(configformat.StructuredPath(root, "upstreams")).Load()
	if err != nil {
		appendOutcome("upstreams", "failed", err.Error())
		return outcomes, err
	}
	appendOutcome("upstreams", "success", fmt.Sprintf("%d upstream server(s) verified", len(servers)))

	integrationWarnings := []string{}
	if cfg.Integrations.RTK.Enabled {
		status, statusErr := rtk.New(rtk.Options{Enabled: true, ConfiguredPath: cfg.Integrations.RTK.Path, ManagedRoot: filepath.Join(root, "managed-assets")}).Status()
		if statusErr != nil || status.Source == rtk.SourceUnavailable {
			integrationWarnings = append(integrationWarnings, "RTK unavailable")
		}
	}
	if cfg.Integrations.CodeGraph.Enabled {
		managedRoot, _ := codegraph.ManagedRoot(root)
		status, statusErr := codegraph.New(codegraph.Options{Enabled: true, ConfiguredPath: cfg.Integrations.CodeGraph.Path, ManagedRoot: managedRoot}).Status()
		if statusErr != nil || status.Resolution.Source == codegraph.ExecutableUnavailable {
			integrationWarnings = append(integrationWarnings, "CodeGraph unavailable")
		}
	}
	if cfg.Integrations.TypeSafe.Enabled {
		credential, credentialErr := typesafeintegration.Credential(root)
		if credentialErr != nil {
			appendOutcome("integrations", "failed", credentialErr.Error())
			return outcomes, credentialErr
		}
		if !credential.Configured {
			integrationWarnings = append(integrationWarnings, "TypeSafe credential missing")
		}
	}
	if len(integrationWarnings) > 0 {
		appendOutcome("integrations", "warning", strings.Join(integrationWarnings, "; "))
	} else {
		appendOutcome("integrations", "success", "configured integration state verified")
	}
	return outcomes, nil
}

func runtimeProbeAt(root string) service.RuntimeProbe {
	return func(ctx context.Context) (runtimecontrol.RuntimeStatus, bool, error) {
		status, err := runtimecontrol.RequestStatusAt(ctx, root)
		if err == nil {
			return status, true, nil
		}
		if runtimecontrol.IsUnavailable(err) {
			return runtimecontrol.RuntimeStatus{}, false, nil
		}
		return runtimecontrol.RuntimeStatus{}, false, err
	}
}

func runtimeShutdownAt(root string) service.RuntimeShutdown {
	return func(ctx context.Context) error {
		return runtimecontrol.RequestShutdownAt(ctx, root)
	}
}

func activationResultFromJournal(journal StageJournal) ActivationResult {
	return ActivationResult{
		TargetRoot: journal.TargetRoot,
		ServiceID:  journal.Canonical.ServiceID,
		Scope:      service.Scope(journal.Canonical.Scope),
		Binary:     journal.Canonical.Binary,
		Activation: append([]ActivationOutcome(nil), journal.Activation...),
		Health:     append([]HealthOutcome(nil), journal.Health...),
		Committed:  journal.Phase == stagePhaseCommitted,
	}
}

func workspaceActivationMessage(outcome WorkspaceStageOutcome) string {
	message := outcome.ID
	if strings.TrimSpace(outcome.Detail) != "" {
		message += " · " + outcome.Detail
	}
	return message
}
