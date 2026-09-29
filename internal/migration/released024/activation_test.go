package released024

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/install"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	"go.mewis.me/codemcp/internal/service"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

type fakeActivationServiceManager struct {
	status         service.Status
	spec           service.Spec
	matches        bool
	installCalls   int
	startCalls     int
	stopCalls      int
	uninstallCalls int
}

func (f *fakeActivationServiceManager) Backend() string { return "fake" }

func (f *fakeActivationServiceManager) DefinitionMatches(spec service.Spec) (bool, error) {
	if !f.status.Installed {
		return false, nil
	}
	if !f.matches {
		return false, nil
	}
	return sameComparablePath(f.spec.ConfigRoot, spec.ConfigRoot) &&
		f.spec.Binary == spec.Binary &&
		f.spec.EnvironmentHash == spec.EnvironmentHash, nil
}

func (f *fakeActivationServiceManager) Install(spec service.Spec) error {
	f.installCalls++
	f.status.Installed = true
	f.status.Backend = "fake"
	f.spec = spec
	f.matches = true
	return nil
}

func (f *fakeActivationServiceManager) Start(spec service.Spec) error {
	f.startCalls++
	if !f.status.Installed {
		return errors.New("service is not installed")
	}
	f.spec = spec
	f.status.Running = true
	f.status.PID = 4242
	return nil
}

func (f *fakeActivationServiceManager) Stop(service.Spec) error {
	f.stopCalls++
	f.status.Running = false
	f.status.PID = 0
	return nil
}

func (f *fakeActivationServiceManager) Uninstall(service.Spec) error {
	f.uninstallCalls++
	f.status = service.Status{Backend: "fake"}
	f.matches = false
	return nil
}

func (f *fakeActivationServiceManager) Status(service.Spec) (service.Status, error) {
	status := f.status
	if status.Backend == "" {
		status.Backend = "fake"
	}
	return status, nil
}

type activationHarness struct {
	fixture           stageFixture
	stage             StageResult
	manager           *fakeActivationServiceManager
	installRollback   *int
	events            *[]ActivationEvent
	runtimeConfigRoot string
}

func newActivationHarness(t *testing.T) activationHarness {
	t.Helper()
	fixture := newStageFixture(t, false)
	stage, err := Stage(t.Context(), StageOptions{
		Manifest: fixture.manifest, TargetRoot: fixture.targetRoot,
		RefreshManifest: fixture.refresh, ServiceControl: fixture.controller,
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := &fakeActivationServiceManager{}
	rollbackCalls := 0
	events := []ActivationEvent{}
	return activationHarness{
		fixture: fixture, stage: stage, manager: manager,
		installRollback: &rollbackCalls, events: &events,
		runtimeConfigRoot: fixture.targetRoot,
	}
}

func (h activationHarness) options(t *testing.T) ActivateOptions {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "cm")
	installCommand := func(context.Context, install.Layout, string, string) (CommandActivation, error) {
		return CommandActivation{
			Binary: binary,
			Rollback: func(context.Context) error {
				*h.installRollback++
				return nil
			},
		}, nil
	}
	probe := func(context.Context) (runtimecontrol.RuntimeStatus, bool, error) {
		if !h.manager.status.Running {
			return runtimecontrol.RuntimeStatus{}, false, nil
		}
		return runtimecontrol.RuntimeStatus{
			PID: 4242, RunID: "run_migrated", Managed: true,
			ServiceID: h.manager.spec.ID, ServiceScope: string(h.manager.spec.Scope),
			ConfigRoot: h.runtimeConfigRoot,
		}, true, nil
	}
	return ActivateOptions{
		JournalPath:              h.stage.JournalPath,
		ServiceScope:             service.ScopeUser,
		ServiceManager:           h.manager,
		HistoricalServiceControl: h.fixture.controller,
		InstallCommand:           installCommand,
		RuntimeProbe:             probe,
		RuntimeShutdown: func(context.Context) error {
			h.manager.status.Running = false
			h.manager.status.PID = 0
			return nil
		},
		RuntimeWait: func(context.Context, string) (runtimecontrol.RuntimeStatus, error) {
			status, _, err := probe(context.Background())
			return status, err
		},
		ReadyTimeout: time.Second,
		Observe: func(event ActivationEvent) {
			*h.events = append(*h.events, event)
		},
	}
}

func TestActivatePublishesStateWorkspacesServiceAndCommits(t *testing.T) {
	h := newActivationHarness(t)
	result, err := Activate(t.Context(), h.options(t))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Committed || !sameComparablePath(result.TargetRoot, h.fixture.targetRoot) {
		t.Fatalf("activation result=%#v", result)
	}
	if !configRootExists(h.fixture.targetRoot) {
		t.Fatal("published CodeMCP root is missing")
	}
	if _, err := os.Stat(h.stage.StageRoot); !os.IsNotExist(err) {
		t.Fatalf("staged root still exists after commit: %v", err)
	}
	workspaceLocal := filepath.Join(h.fixture.workspaceRoot, ".cm")
	if _, err := os.Stat(workspaceLocal); err != nil {
		t.Fatalf("workspace local state was not activated: %v", err)
	}
	if _, err := os.Stat(h.fixture.missingRoot); !os.IsNotExist(err) {
		t.Fatalf("unavailable workspace was mutated: %v", err)
	}
	if h.manager.installCalls != 1 || h.manager.startCalls != 1 || h.manager.stopCalls != 0 || h.manager.uninstallCalls != 0 {
		t.Fatalf("service lifecycle install=%d start=%d stop=%d uninstall=%d", h.manager.installCalls, h.manager.startCalls, h.manager.stopCalls, h.manager.uninstallCalls)
	}
	if !sameComparablePath(h.manager.spec.ConfigRoot, h.fixture.targetRoot) || h.manager.spec.EnvironmentHash == "" {
		t.Fatalf("service spec=%#v", h.manager.spec)
	}
	if *h.installRollback != 0 {
		t.Fatalf("install rollback called on successful activation: %d", *h.installRollback)
	}
	for _, secret := range h.fixture.secrets {
		assertFileDoesNotContain(t, h.stage.JournalPath, secret)
	}
	journal, ok := loadStageJournal(h.stage.JournalPath)
	if !ok || journal.Phase != stagePhaseCommitted || journal.CommittedAt == nil || journal.FailureStage != "" {
		t.Fatalf("committed journal=%#v ok=%t", journal, ok)
	}
	if len(journal.Health) < 5 {
		t.Fatalf("health outcomes=%#v", journal.Health)
	}
	if !hasActivationEvent(*h.events, "health", true) || hasActivationEvent(*h.events, "rollback", false) {
		t.Fatalf("activation events=%#v", *h.events)
	}
}

func TestActivateFailureRollsBackRootWorkspaceInstallAndJournal(t *testing.T) {
	h := newActivationHarness(t)
	options := h.options(t)
	options.InjectFailure = func(point string) error {
		if point == "command-installed" {
			return errors.New("injected command failure")
		}
		return nil
	}
	if _, err := Activate(t.Context(), options); err == nil || !strings.Contains(err.Error(), "injected command failure") {
		t.Fatalf("activation error=%v", err)
	}
	if _, err := os.Stat(h.fixture.targetRoot); !os.IsNotExist(err) {
		t.Fatalf("target root remained after rollback: %v", err)
	}
	if _, err := os.Stat(h.stage.StageRoot); err != nil {
		t.Fatalf("stage root was not restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.fixture.workspaceRoot, ".cm")); !os.IsNotExist(err) {
		t.Fatalf("workspace local state remained after rollback: %v", err)
	}
	if *h.installRollback != 1 {
		t.Fatalf("install rollback calls=%d", *h.installRollback)
	}
	journal, ok := loadStageJournal(h.stage.JournalPath)
	if !ok || journal.Phase != stagePhaseStaged || journal.FailureStage != "install" {
		t.Fatalf("rollback journal=%#v ok=%t", journal, ok)
	}
	if !hasActivationEvent(*h.events, "rollback", false) {
		t.Fatalf("rollback stage was not rendered: %#v", *h.events)
	}
	if hash, err := fingerprintStageTree(h.stage.StageRoot); err != nil || hash != journal.StagedSHA256 {
		t.Fatalf("restored stage fingerprint=%q err=%v journal=%q", hash, err, journal.StagedSHA256)
	}
}

func TestActivateWorkspaceConflictIsUntouchedAndReported(t *testing.T) {
	h := newActivationHarness(t)
	identity, _, err := workspacestate.New(h.fixture.workspaceRoot).EnsureIdentity("ws_other")
	if err != nil {
		t.Fatal(err)
	}
	result, err := Activate(t.Context(), h.options(t))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Committed {
		t.Fatalf("activation result=%#v", result)
	}
	loaded, err := workspacestate.New(h.fixture.workspaceRoot).LoadIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != identity.ID || loaded.ID != "ws_other" {
		t.Fatalf("conflicting workspace identity changed: %#v", loaded)
	}
	journal, ok := loadStageJournal(h.stage.JournalPath)
	if !ok || !hasActivationDetail(journal.Activation, "warning", "ownership changed") {
		t.Fatalf("workspace conflict was not reported: %#v", journal.Activation)
	}
}

func TestActivateReadinessRejectsReleasedConfigRootAndRollsBack(t *testing.T) {
	h := newActivationHarness(t)
	h.runtimeConfigRoot = h.fixture.sourceRoot
	if _, err := Activate(t.Context(), h.options(t)); err == nil || !strings.Contains(err.Error(), "config root mismatch") {
		t.Fatalf("readiness error=%v", err)
	}
	if _, err := os.Stat(h.fixture.targetRoot); !os.IsNotExist(err) {
		t.Fatalf("target root remained after readiness rollback: %v", err)
	}
	if h.manager.stopCalls == 0 || h.manager.uninstallCalls == 0 {
		t.Fatalf("new service was not rolled back stop=%d uninstall=%d", h.manager.stopCalls, h.manager.uninstallCalls)
	}
	if *h.installRollback != 1 {
		t.Fatalf("install rollback calls=%d", *h.installRollback)
	}
	journal, _ := loadStageJournal(h.stage.JournalPath)
	if journal.Phase != stagePhaseStaged || journal.CommittedAt != nil {
		t.Fatalf("readiness failure committed journal: %#v", journal)
	}
}

func TestActivateRollbackPreservesWorkspaceChangedAfterActivation(t *testing.T) {
	h := newActivationHarness(t)
	options := h.options(t)
	options.InjectFailure = func(point string) error {
		if point != "workspaces-activated" {
			return nil
		}
		path := filepath.Join(h.fixture.workspaceRoot, ".cm", "concurrent.txt")
		if err := os.WriteFile(path, []byte("operator change\n"), 0600); err != nil {
			t.Fatal(err)
		}
		return errors.New("fail after workspace activation")
	}
	if _, err := Activate(t.Context(), options); err == nil || !strings.Contains(err.Error(), "requires recovery") {
		t.Fatalf("activation error=%v", err)
	}
	data, err := os.ReadFile(filepath.Join(h.fixture.workspaceRoot, ".cm", "concurrent.txt"))
	if err != nil || string(data) != "operator change\n" {
		t.Fatalf("concurrent workspace content=%q err=%v", data, err)
	}
	if _, err := os.Stat(h.fixture.targetRoot); !os.IsNotExist(err) {
		t.Fatalf("global target was not rolled back: %v", err)
	}
	journal, _ := loadStageJournal(h.stage.JournalPath)
	if journal.Phase != stagePhaseActivationFailed || !hasActivationOutcome(journal.Activation, "rollback", "failed") {
		t.Fatalf("actionable rollback journal=%#v", journal)
	}
}

func configRootExists(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".cm-root"))
	return err == nil
}

func hasActivationEvent(events []ActivationEvent, stage string, child bool) bool {
	for _, event := range events {
		if event.Stage == stage && event.Child == child {
			return true
		}
	}
	return false
}

func hasActivationOutcome(outcomes []ActivationOutcome, stage, state string) bool {
	for _, outcome := range outcomes {
		if outcome.Stage == stage && outcome.State == state {
			return true
		}
	}
	return false
}

func hasActivationDetail(outcomes []ActivationOutcome, state, detail string) bool {
	for _, outcome := range outcomes {
		if outcome.State == state && strings.Contains(outcome.Detail, detail) {
			return true
		}
	}
	return false
}
