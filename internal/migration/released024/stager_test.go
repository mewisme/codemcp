package released024

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/install"
	"go.mewis.me/codemcp/internal/secretstore"
	"go.mewis.me/codemcp/internal/tunnel"
	"go.mewis.me/codemcp/internal/workspace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

type fakeHistoricalController struct {
	running    bool
	quiesced   []string
	restored   []string
	quiesceErr error
	restoreErr error
	onQuiesce  func(ServiceState)
}

func (f *fakeHistoricalController) Quiesce(_ context.Context, _ SourceDescriptor, state ServiceState) error {
	if f.onQuiesce != nil {
		f.onQuiesce(state)
	}
	f.quiesced = append(f.quiesced, state.ID)
	if f.quiesceErr != nil {
		return f.quiesceErr
	}
	f.running = false
	return nil
}

func (f *fakeHistoricalController) Restore(_ context.Context, _ SourceDescriptor, state ServiceState) error {
	f.restored = append(f.restored, state.ID)
	if f.restoreErr != nil {
		return f.restoreErr
	}
	if state.Running {
		f.running = true
	}
	return nil
}

type stageFixture struct {
	home          string
	sourceRoot    string
	targetRoot    string
	workspaceRoot string
	missingRoot   string
	controller    *fakeHistoricalController
	manifest      Manifest
	refresh       func(context.Context, Manifest) (Manifest, error)
	secrets       []string
}

func TestStageReleasedStateIsTransactionalAndSemantic(t *testing.T) {
	fixture := newStageFixture(t, true)
	sourceBefore := treeSnapshot(t, fixture.sourceRoot)
	workspaceBefore := pathSnapshot(t, fixture.workspaceRoot)
	_, journalPath, _ := stagePaths(fixture.targetRoot, fixture.manifest.SourceSHA256)
	fixture.controller.onQuiesce = func(state ServiceState) {
		journal, ok := loadStageJournal(journalPath)
		if !ok || journal.Phase != stagePhaseQuiescing || len(journal.Services) != 1 || !journal.Services[0].Running {
			t.Fatalf("pre-quiesce journal=%#v ok=%t service=%#v", journal, ok, state)
		}
	}

	result, err := Stage(t.Context(), StageOptions{
		Manifest: fixture.manifest, TargetRoot: fixture.targetRoot,
		RefreshManifest: fixture.refresh, ServiceControl: fixture.controller,
		Now: func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AlreadyStaged || result.StagedSHA256 == "" || result.SourceSHA256 != fixture.manifest.SourceSHA256 {
		t.Fatalf("stage result=%#v", result)
	}
	if fixture.controller.running || len(fixture.controller.quiesced) != 1 || len(fixture.controller.restored) != 0 {
		t.Fatalf("historical service lifecycle quiesced=%v restored=%v running=%t", fixture.controller.quiesced, fixture.controller.restored, fixture.controller.running)
	}
	if _, err := os.Stat(fixture.targetRoot); !os.IsNotExist(err) {
		t.Fatalf("final target mutated before activation: %v", err)
	}
	if after := pathSnapshot(t, fixture.workspaceRoot); !equalStringMaps(workspaceBefore, after) {
		t.Fatalf("workspace mutated before activation: before=%#v after=%#v", workspaceBefore, after)
	}
	if after := treeSnapshot(t, fixture.sourceRoot); !equalStringMaps(sourceBefore, after) {
		t.Fatalf("released source mutated: before=%#v after=%#v", sourceBefore, after)
	}

	cfg, err := config.LoadAt(result.StageRoot)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.MCPTokenHash != "mcp-hash" || cfg.Auth.AdminTokenHash != "admin-hash" || !cfg.Auth.MCPLegacyBearer {
		t.Fatalf("auth semantics not preserved: %#v", cfg.Auth)
	}
	if !tunnel.AdminEnabled(cfg.Tunnel) || cfg.Tunnel.Admin.OrganizationID != "org_stage" || cfg.Tunnel.Admin.Verified || cfg.Tunnel.Admin.ReadAccess || cfg.Tunnel.Admin.ManageAccess {
		t.Fatalf("tunnel admin not canonicalized: %#v", cfg.Tunnel.Admin)
	}
	if cfg.Tunnel.APIKey != "runtime-secret" || cfg.Tunnel.Admin.Key != "admin-secret" {
		t.Fatalf("staged config cannot resolve canonical tunnel secrets")
	}
	for _, secret := range fixture.secrets {
		assertFileDoesNotContain(t, filepath.Join(result.StageRoot, "config.json"), secret)
		assertFileDoesNotContain(t, filepath.Join(result.StageRoot, "tunnel.json"), secret)
		assertFileDoesNotContain(t, filepath.Join(result.StageRoot, "upstream.json"), secret)
		assertFileDoesNotContain(t, filepath.Join(result.StageRoot, "oauth.json"), secret)
		assertFileDoesNotContain(t, result.JournalPath, secret)
	}
	rawConfig, err := os.ReadFile(filepath.Join(result.StageRoot, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawConfig), `"features"`) || !strings.Contains(string(rawConfig), `"integrations"`) || strings.Contains(string(rawConfig), `"admin_organization_id"`) || strings.Contains(string(rawConfig), `"admin_key"`) {
		t.Fatalf("staged config contains stale released schema: %s", rawConfig)
	}
	rawTunnel, err := os.ReadFile(filepath.Join(result.StageRoot, "tunnel.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawTunnel), `"admin_enabled"`) || strings.Contains(string(rawTunnel), `"admin_organization_id"`) || !strings.Contains(string(rawTunnel), `"admin"`) || !strings.Contains(string(rawTunnel), `"organization_id"`) {
		t.Fatalf("staged tunnel contains stale flat admin schema: %s", rawTunnel)
	}
	rawUpstream, err := os.ReadFile(filepath.Join(result.StageRoot, "upstream.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawUpstream), `"servers"`) || !strings.Contains(string(rawUpstream), `"upstreams"`) || !strings.Contains(string(rawUpstream), "secret-file") || !strings.Contains(string(rawUpstream), `"auth"`) {
		t.Fatalf("staged upstream schema/secret/auth mismatch: %s", rawUpstream)
	}
	rawOAuth, err := os.ReadFile(filepath.Join(result.StageRoot, "oauth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawOAuth), "secret-file") {
		t.Fatalf("staged OAuth did not protect credentials: %s", rawOAuth)
	}

	secrets := secretstore.New(result.StageRoot)
	for account, want := range map[string]string{
		secretstore.AccountName(secretstore.DomainTunnel, "runtime-key"):                         "runtime-secret",
		secretstore.AccountName(secretstore.DomainTunnel, "admin-key"):                           "admin-secret",
		secretstore.AccountName(secretstore.DomainUpstream, "up_one", "header", "Authorization"): "Bearer upstream-secret",
		secretstore.AccountName(secretstore.DomainOAuth, "up_one", "client-secret"):              "oauth-client-secret",
		secretstore.AccountName(secretstore.DomainOAuth, "up_one", "access-token"):               "oauth-access-secret",
		secretstore.AccountName(secretstore.DomainOAuth, "up_one", "refresh-token"):              "oauth-refresh-secret",
	} {
		got, err := secrets.Get(account)
		if err != nil || got != want {
			t.Fatalf("staged secret %s=%q err=%v want=%q", account, got, err, want)
		}
	}

	if len(result.Workspaces) != 2 {
		t.Fatalf("workspace outcomes=%#v", result.Workspaces)
	}
	var available, unavailable WorkspaceStageOutcome
	for _, outcome := range result.Workspaces {
		if filepath.Clean(outcome.WorkspaceRoot) == filepath.Clean(fixture.workspaceRoot) {
			available = outcome
		} else {
			unavailable = outcome
		}
	}
	if available.State != "staged" || available.StagePath == "" || unavailable.State != "unavailable" {
		t.Fatalf("workspace outcomes=%#v", result.Workspaces)
	}
	stagedIdentity, err := workspacestate.New(filepath.Dir(available.StagePath)).LoadIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if stagedIdentity.ID != available.ID || stagedIdentity.ID == available.LegacyID {
		t.Fatalf("workspace identity not migrated: %#v outcome=%#v", stagedIdentity, available)
	}
	if _, err := os.Stat(filepath.Join(available.StagePath, "memory", "MEMORY.md")); err != nil {
		t.Fatalf("staged workspace memory missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(available.StagePath, "state", "shell.json")); err != nil {
		t.Fatalf("staged workspace shell state missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture.missingRoot, workspace.LocalDirName)); !os.IsNotExist(err) {
		t.Fatalf("unavailable workspace was written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(result.StageRoot, "runtime", "environment.json")); err != nil {
		t.Fatalf("canonical service environment not regenerated: %v", err)
	}

	again, err := Stage(t.Context(), StageOptions{
		Manifest: fixture.manifest, TargetRoot: fixture.targetRoot,
		RefreshManifest: fixture.refresh, ServiceControl: fixture.controller,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !again.AlreadyStaged || len(fixture.controller.quiesced) != 1 {
		t.Fatalf("idempotent rerun=%#v quiesced=%v", again, fixture.controller.quiesced)
	}
}

func TestStageLeavesPreviouslyStoppedHistoricalServiceUntouched(t *testing.T) {
	fixture := newStageFixture(t, false)
	result, err := Stage(t.Context(), StageOptions{
		Manifest: fixture.manifest, TargetRoot: fixture.targetRoot,
		RefreshManifest: fixture.refresh, ServiceControl: fixture.controller,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.StagedSHA256 == "" || fixture.controller.running ||
		len(fixture.controller.quiesced) != 0 || len(fixture.controller.restored) != 0 {
		t.Fatalf("stopped historical service was changed: result=%#v controller=%#v", result, fixture.controller)
	}
}

func TestStageFailureRestoresHistoricalServiceAndLeavesDestinationsUntouched(t *testing.T) {
	fixture := newStageFixture(t, true)
	sourceBefore := treeSnapshot(t, fixture.sourceRoot)
	workspaceBefore := pathSnapshot(t, fixture.workspaceRoot)
	_, err := Stage(t.Context(), StageOptions{
		Manifest: fixture.manifest, TargetRoot: fixture.targetRoot,
		RefreshManifest: fixture.refresh, ServiceControl: fixture.controller,
		InjectFailure: func(point string) error {
			if point == "credentials" {
				return errors.New("injected staging failure")
			}
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "injected staging failure") {
		t.Fatalf("failure err=%v", err)
	}
	if !fixture.controller.running || len(fixture.controller.restored) != 1 {
		t.Fatalf("historical service not restored: %#v", fixture.controller)
	}
	stageRoot, journalPath, _ := stagePaths(fixture.targetRoot, fixture.manifest.SourceSHA256)
	if _, err := os.Stat(stageRoot); !os.IsNotExist(err) {
		t.Fatalf("failed stage root still exists: %v", err)
	}
	journal, ok := loadStageJournal(journalPath)
	if !ok || journal.Phase != stagePhaseFailed || journal.FailureStage != "credentials" {
		t.Fatalf("failed journal=%#v ok=%t", journal, ok)
	}
	for _, secret := range fixture.secrets {
		assertFileDoesNotContain(t, journalPath, secret)
	}
	if _, err := os.Stat(fixture.targetRoot); !os.IsNotExist(err) {
		t.Fatalf("final target mutated on failure: %v", err)
	}
	if after := treeSnapshot(t, fixture.sourceRoot); !equalStringMaps(sourceBefore, after) {
		t.Fatalf("source changed on failure")
	}
	if after := pathSnapshot(t, fixture.workspaceRoot); !equalStringMaps(workspaceBefore, after) {
		t.Fatalf("workspace changed on failure")
	}
}

func TestStageRefusesUnverifiedWriterBeforeAnyWrite(t *testing.T) {
	fixture := newStageFixture(t, true)
	fixture.manifest.Services[0].Ownership = OwnershipAmbiguous
	_, err := Stage(t.Context(), StageOptions{
		Manifest: fixture.manifest, TargetRoot: fixture.targetRoot,
		RefreshManifest: fixture.refresh, ServiceControl: fixture.controller,
	})
	if err == nil || !strings.Contains(err.Error(), "ownership is not verified") {
		t.Fatalf("unverified service err=%v", err)
	}
	stageRoot, journalPath, _ := stagePaths(fixture.targetRoot, fixture.manifest.SourceSHA256)
	if pathExists(stageRoot) || pathExists(journalPath) {
		t.Fatalf("unverified writer produced migration state")
	}
	if len(fixture.controller.quiesced) != 0 {
		t.Fatalf("unverified writer was touched: %v", fixture.controller.quiesced)
	}
}

func TestStageRejectsServiceOutsideReleasedSourceDescriptor(t *testing.T) {
	fixture := newStageFixture(t, true)
	tests := []struct {
		name   string
		mutate func(*Manifest)
		want   string
	}{
		{
			name: "unexpected service id",
			mutate: func(manifest *Manifest) {
				manifest.Services[0].ID = "chatgpt-mcp-user-unrelated"
			},
			want: "not part of the released source descriptor",
		},
		{
			name: "different config root",
			mutate: func(manifest *Manifest) {
				manifest.Services[0].ConfigRoot = filepath.Join(fixture.home, "other-root")
			},
			want: "config root does not match",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := fixture.manifest
			manifest.Services = append([]ServiceState(nil), fixture.manifest.Services...)
			test.mutate(&manifest)
			_, err := Stage(t.Context(), StageOptions{
				Manifest: manifest, TargetRoot: fixture.targetRoot,
				RefreshManifest: fixture.refresh, ServiceControl: fixture.controller,
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("service descriptor validation err=%v", err)
			}
		})
	}
	if len(fixture.controller.quiesced) != 0 || len(fixture.controller.restored) != 0 {
		t.Fatalf("invalid service descriptor touched historical service: %#v", fixture.controller)
	}
}

func TestStageFailureAfterExternalWritesRestoresServiceAndLeavesFinalStateUntouched(t *testing.T) {
	for _, point := range []string{
		"root-marker",
		"config",
		"credentials",
		"upstream",
		"oauth",
		"global-state",
		"workspaces",
		"service-environment",
		"validate",
	} {
		t.Run(point, func(t *testing.T) {
			fixture := newStageFixture(t, true)
			sourceBefore := treeSnapshot(t, fixture.sourceRoot)
			workspaceBefore := pathSnapshot(t, fixture.workspaceRoot)
			_, err := Stage(t.Context(), StageOptions{
				Manifest: fixture.manifest, TargetRoot: fixture.targetRoot,
				RefreshManifest: fixture.refresh, ServiceControl: fixture.controller,
				InjectFailure: func(current string) error {
					if current == point {
						return errors.New("injected external-write failure")
					}
					return nil
				},
			})
			if err == nil || !strings.Contains(err.Error(), "injected external-write failure") {
				t.Fatalf("failure err=%v", err)
			}
			if !fixture.controller.running || len(fixture.controller.restored) != 1 {
				t.Fatalf("historical service not restored: %#v", fixture.controller)
			}
			stageRoot, journalPath, _ := stagePaths(fixture.targetRoot, fixture.manifest.SourceSHA256)
			if _, err := os.Stat(stageRoot); !os.IsNotExist(err) {
				t.Fatalf("failed stage root still exists: %v", err)
			}
			journal, ok := loadStageJournal(journalPath)
			if !ok || journal.Phase != stagePhaseFailed || journal.FailureStage != point {
				t.Fatalf("failed journal=%#v ok=%t", journal, ok)
			}
			for _, secret := range fixture.secrets {
				assertFileDoesNotContain(t, journalPath, secret)
			}
			if _, err := os.Stat(fixture.targetRoot); !os.IsNotExist(err) {
				t.Fatalf("final target mutated on failure: %v", err)
			}
			if after := treeSnapshot(t, fixture.sourceRoot); !equalStringMaps(sourceBefore, after) {
				t.Fatal("released source changed on failure")
			}
			if after := pathSnapshot(t, fixture.workspaceRoot); !equalStringMaps(workspaceBefore, after) {
				t.Fatal("workspace changed on failure")
			}
		})
	}
}

func TestStageRejectsMalformedJournalBeforeTouchingHistoricalService(t *testing.T) {
	fixture := newStageFixture(t, true)
	_, journalPath, _ := stagePaths(fixture.targetRoot, fixture.manifest.SourceSHA256)
	writeFixture(t, journalPath, "{broken")

	_, err := Stage(t.Context(), StageOptions{
		Manifest: fixture.manifest, TargetRoot: fixture.targetRoot,
		RefreshManifest: fixture.refresh, ServiceControl: fixture.controller,
	})
	if err == nil || !strings.Contains(err.Error(), "read migration journal") {
		t.Fatalf("malformed journal err=%v", err)
	}
	if len(fixture.controller.quiesced) != 0 || len(fixture.controller.restored) != 0 {
		t.Fatalf("malformed journal touched historical service: %#v", fixture.controller)
	}
}

func TestStageRejectsInterruptedJournalUntilExplicitRecovery(t *testing.T) {
	fixture := newStageFixture(t, true)
	stageRoot, journalPath, _ := stagePaths(fixture.targetRoot, fixture.manifest.SourceSHA256)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	journal := StageJournal{
		Version: StageJournalVersion, SourceRelease: SourceRelease, Phase: stagePhaseQuiescing,
		SourceRoot: fixture.manifest.Source.Root, SourceSHA256: fixture.manifest.SourceSHA256,
		TargetRoot: fixture.targetRoot, StageRoot: stageRoot,
		Rollback: RollbackReference{
			SourceRoot: fixture.manifest.Source.Root, SourceSHA256: fixture.manifest.SourceSHA256,
			TargetRoot: fixture.targetRoot,
		},
		Services: fixture.manifest.Services, CreatedAt: now, UpdatedAt: now,
	}
	if err := writeStageJournal(journalPath, journal); err != nil {
		t.Fatal(err)
	}

	_, err := Stage(t.Context(), StageOptions{
		Manifest: fixture.manifest, TargetRoot: fixture.targetRoot,
		RefreshManifest: fixture.refresh, ServiceControl: fixture.controller,
	})
	if err == nil || !strings.Contains(err.Error(), "explicit recovery") {
		t.Fatalf("interrupted journal err=%v", err)
	}
	if len(fixture.controller.quiesced) != 0 {
		t.Fatalf("interrupted journal retried service mutation: %#v", fixture.controller)
	}
}

func TestStageSkipsAndReportsInvalidOptionalHistory(t *testing.T) {
	fixture := newStageFixture(t, true)
	writeFixture(t, filepath.Join(fixture.sourceRoot, "logs", "runtime.jsonl"), "not-json\n")
	writeFixture(t, filepath.Join(fixture.sourceRoot, "tui-state.json"), "{invalid")
	manifest, err := fixture.refresh(t.Context(), fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	fixture.manifest = manifest

	result, err := Stage(t.Context(), StageOptions{
		Manifest: fixture.manifest, TargetRoot: fixture.targetRoot,
		RefreshManifest: fixture.refresh, ServiceControl: fixture.controller,
	})
	if err != nil {
		t.Fatal(err)
	}
	logs, ok := stageDomain(result.Domains, "logs")
	if !ok || logs.State != "staged-with-skips" || !strings.Contains(logs.Detail, "1 skipped") {
		t.Fatalf("logs outcome=%#v ok=%t", logs, ok)
	}
	tui, ok := stageDomain(result.Domains, "tui-state")
	if !ok || tui.State != "skipped" {
		t.Fatalf("tui outcome=%#v ok=%t", tui, ok)
	}
	if _, err := os.Stat(filepath.Join(result.StageRoot, "logs", "runtime.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("invalid optional log was staged: %v", err)
	}
	if _, err := os.Stat(filepath.Join(result.StageRoot, "tui-state.json")); !os.IsNotExist(err) {
		t.Fatalf("invalid optional TUI state was staged: %v", err)
	}
}

func TestStageRejectsConflictingExistingWorkspaceLocalState(t *testing.T) {
	fixture := newStageFixture(t, true)
	registry, err := workspace.NewManager(fixture.manifest.Workspaces.Path).InspectRegistry()
	if err != nil {
		t.Fatal(err)
	}
	var current workspace.Workspace
	for _, item := range registry.Workspaces {
		if filepath.Clean(item.Path) == filepath.Clean(fixture.workspaceRoot) {
			current = item
		}
	}
	if current.ID == "" {
		t.Fatal("current workspace id not resolved")
	}
	identity, err := workspacestate.NewIdentity(current.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	localRoot := filepath.Join(fixture.workspaceRoot, workspace.LocalDirName)
	if err := workspacestate.WriteIdentitySnapshot(localRoot, identity); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(localRoot, "operator.txt"), "do not overwrite")
	before := pathSnapshot(t, localRoot)

	_, err = Stage(t.Context(), StageOptions{
		Manifest: fixture.manifest, TargetRoot: fixture.targetRoot,
		RefreshManifest: fixture.refresh, ServiceControl: fixture.controller,
	})
	var duplicate *workspace.DuplicateWorkspaceIdentityError
	if !errors.As(err, &duplicate) {
		t.Fatalf("workspace conflict err=%v", err)
	}
	if after := pathSnapshot(t, localRoot); !equalStringMaps(before, after) {
		t.Fatalf("conflicting workspace local state changed: before=%#v after=%#v", before, after)
	}
	if !fixture.controller.running || len(fixture.controller.restored) != 1 {
		t.Fatalf("service not restored after workspace conflict: %#v", fixture.controller)
	}
}

func TestStageRejectsSourceChangeAfterQuiesce(t *testing.T) {
	fixture := newStageFixture(t, true)
	fixture.refresh = func(context.Context, Manifest) (Manifest, error) {
		changed := fixture.manifest
		changed.SourceSHA256 = strings.Repeat("f", 64)
		changed.Services[0].Running = false
		return changed, nil
	}
	_, err := Stage(t.Context(), StageOptions{
		Manifest: fixture.manifest, TargetRoot: fixture.targetRoot,
		RefreshManifest: fixture.refresh, ServiceControl: fixture.controller,
	})
	if err == nil || !strings.Contains(err.Error(), "changed between detection and quiescence") {
		t.Fatalf("source change err=%v", err)
	}
	if !fixture.controller.running || len(fixture.controller.restored) != 1 {
		t.Fatalf("service not restored after source change: %#v", fixture.controller)
	}
}

func TestStageBundleUsesCanonicalSemanticStager(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", filepath.Join(home, "current"))
	bundlePath := filepath.Join(home, "released.cgm")
	configData, err := json.Marshal(map[string]any{
		"server": map[string]any{"enabled": false},
		"auth": map[string]any{
			"mcp_enabled": true, "admin_enabled": true,
			"mcp_token_hash": "bundle-mcp-hash", "admin_token_hash": "bundle-admin-hash",
		},
		"features": map[string]any{},
		"tunnel": map[string]any{
			"enabled": true, "id": "tunnel_bundle",
			"api_key": secretstore.Marker, "api_key_configured": true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeReleasedBundleFixture(t, bundlePath, map[string][]byte{"config.json": configData}, map[string]string{
		secretstore.AccountName(secretstore.DomainTunnel, "runtime-key"): "bundle-runtime-secret",
	})
	targetRoot := filepath.Join(home, ".cm")
	result, err := StageBundle(t.Context(), BundleStageOptions{BundlePath: bundlePath, TargetRoot: targetRoot})
	if err != nil {
		t.Fatal(err)
	}
	if result.AlreadyStaged || result.StagedSHA256 == "" {
		t.Fatalf("bundle stage result=%#v", result)
	}
	cfg, err := config.LoadAt(result.StageRoot)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tunnel.APIKey != "bundle-runtime-secret" || cfg.Tunnel.ID != "tunnel_bundle" {
		t.Fatalf("bundle tunnel state=%#v", cfg.Tunnel)
	}
	raw, err := os.ReadFile(filepath.Join(result.StageRoot, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"features"`) || !strings.Contains(string(raw), `"integrations"`) || strings.Contains(string(raw), "bundle-runtime-secret") {
		t.Fatalf("bundle bypassed semantic config migration: %s", raw)
	}
	journal, ok := loadStageJournal(result.JournalPath)
	if !ok || journal.Rollback.BundlePath != filepath.Clean(bundlePath) || journal.Rollback.BundleSHA256 == "" {
		t.Fatalf("bundle rollback reference=%#v ok=%t", journal.Rollback, ok)
	}
	materialized := bundleMaterializedRoot(targetRoot, journal.Rollback.BundleSHA256)
	if _, err := os.Stat(filepath.Join(materialized, bundleSourceMetadataName)); err != nil {
		t.Fatalf("bundle materialized source not retained: %v", err)
	}
	assertFileDoesNotContain(t, result.JournalPath, "bundle-runtime-secret")

	again, err := StageBundle(t.Context(), BundleStageOptions{BundlePath: bundlePath, TargetRoot: targetRoot})
	if err != nil {
		t.Fatal(err)
	}
	if !again.AlreadyStaged || again.StagedSHA256 != result.StagedSHA256 {
		t.Fatalf("bundle rerun=%#v first=%#v", again, result)
	}
}

func newStageFixture(t *testing.T, running bool) stageFixture {
	t.Helper()
	currentRoot := filepath.Join(t.TempDir(), "current")
	t.Setenv("CM_CONFIG_DIR", currentRoot)
	home := t.TempDir()
	sourceRoot := filepath.Join(home, "released")
	workspaceRoot := filepath.Join(home, "workspace")
	missingRoot := filepath.Join(home, "missing-workspace")
	targetRoot := filepath.Join(home, ".cm")
	if err := os.MkdirAll(workspaceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(sourceRoot, legacyRootMarkerName), legacyRootMarkerValue+"\n")
	writeJSONFixture(t, filepath.Join(sourceRoot, "config.json"), map[string]any{
		"server": map[string]any{"enabled": true},
		"admin":  map[string]any{"enabled": true},
		"auth": map[string]any{
			"mcp_enabled": true, "admin_enabled": true,
			"mcp_token_hash": "mcp-hash", "admin_token_hash": "admin-hash",
			"mcp_legacy_bearer": true,
		},
		"features": map[string]any{
			"ponytail": map[string]any{"enabled": true},
			"caveman":  map[string]any{"enabled": false},
		},
		"tunnel": map[string]any{
			"enabled": true, "id": "tunnel_stage", "api_key": "runtime-secret",
			"admin_key": "admin-secret", "admin_enabled": true,
			"admin_organization_id": "org_stage", "admin_verified": true,
			"admin_read_access": true, "admin_manage_access": true,
		},
	})
	writeJSONFixture(t, filepath.Join(sourceRoot, "upstream.json"), map[string]any{
		"version": 1,
		"servers": []map[string]any{{
			"id": "up_one", "name": "One", "transport": "stdio", "enabled": true,
			"command": "echo", "headers": map[string]any{"Authorization": "Bearer upstream-secret"},
			"auth": map[string]any{"type": "oauth", "scope": "read write"},
		}},
	})
	writeJSONFixture(t, filepath.Join(sourceRoot, "oauth.json"), map[string]any{
		"version": 1,
		"credentials": map[string]any{
			"up_one": map[string]any{
				"server_id": "up_one", "server_url": "https://example.invalid/mcp",
				"issuer": "https://issuer.invalid", "registration": "dynamic", "client_id": "client",
				"client_secret": "oauth-client-secret", "access_token": "oauth-access-secret",
				"refresh_token": "oauth-refresh-secret", "token_type": "Bearer",
				"updated_at": "2026-01-01T00:00:00Z",
			},
		},
	})
	writeJSONFixture(t, filepath.Join(sourceRoot, "workspaces.json"), map[string]any{
		"version": 4,
		"workspaces": []map[string]any{
			{"id": "ws_legacy", "path": workspaceRoot, "name": "available"},
			{"id": "ws_missing", "path": missingRoot, "name": "missing"},
		},
		"containers": []map[string]any{{"id": "container_one", "name": "One", "workspace_ids": []string{"ws_legacy", "ws_missing"}}},
	})
	writeFixture(t, filepath.Join(sourceRoot, "workspaces", "ws_legacy", "MEMORY.md"), "durable memory\n")
	writeJSONFixture(t, filepath.Join(sourceRoot, "workspaces", "ws_legacy", "shell.json"), map[string]any{
		"workspace_id": "ws_legacy", "cwd": workspaceRoot,
		"started_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:01:00Z",
		"recent_commands": []string{"go test ./..."},
	})
	writeJSONFixture(t, filepath.Join(sourceRoot, "state", "instance.json"), map[string]any{
		"version": 1,
		"identity": map[string]any{
			"id":   "inst_11111111111111111111111111111111",
			"name": "legacy-host", "created_at": "2026-01-01T00:00:00Z",
		},
	})
	writeJSONFixture(t, filepath.Join(sourceRoot, "tui-state.json"), map[string]any{"version": 1, "recent_actions": []string{"logs"}})
	writeJSONLineFixture(t, filepath.Join(sourceRoot, "logs", "runtime.jsonl"), map[string]any{"event": "ready"})
	writeFixture(t, filepath.Join(sourceRoot, "instructions", "AGENTS.md"), "Use canonical owners.\n")
	writeJSONFixture(t, filepath.Join(sourceRoot, "runtime", "environment.json"), map[string]any{"version": 1, "values": map[string]any{"PATH": "/legacy"}})
	writeJSONFixture(t, filepath.Join(sourceRoot, ".runtime-control.json"), map[string]any{"pid": 1234})

	controller := &fakeHistoricalController{running: running}
	serviceID := historicalServiceID(sourceRoot, "user")
	detect := func(ctx context.Context) (Manifest, error) {
		return Detect(ctx, Options{
			SourceRoot: sourceRoot, HomeDir: home,
			LookupEnv:         func(string) string { return "" },
			FindInstallations: func(install.Layout, string) ([]install.LegacyInstallation, error) { return nil, nil },
			FindAliases:       func() ([]install.LegacyAlias, error) { return nil, nil },
			InspectServices: func(context.Context, SourceDescriptor) ([]ServiceState, error) {
				return []ServiceState{{
					Scope: "user", ID: serviceID, Backend: "test",
					Installed: true, Enabled: true, Bootstrapped: true, Running: controller.running,
					Ownership: OwnershipVerified, Binary: filepath.Join(home, ".chatgpt-mcp", "current", "chatgpt-mcp"),
					ConfigRoot: sourceRoot,
				}}, nil
			},
		})
	}
	manifest, err := detect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	refresh := func(ctx context.Context, _ Manifest) (Manifest, error) { return detect(ctx) }
	return stageFixture{
		home: home, sourceRoot: sourceRoot, targetRoot: targetRoot,
		workspaceRoot: workspaceRoot, missingRoot: missingRoot,
		controller: controller, manifest: manifest, refresh: refresh,
		secrets: []string{"runtime-secret", "admin-secret", "upstream-secret", "oauth-client-secret", "oauth-access-secret", "oauth-refresh-secret"},
	}
}

func assertFileDoesNotContain(t *testing.T, path, secret string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("%s leaked secret %q", path, secret)
	}
}

func pathSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return map[string]string{}
	}
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result[filepath.ToSlash(relative)+"/"] = "dir"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256ForTest(data)
		result[filepath.ToSlash(relative)] = sum
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func sha256ForTest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func equalStringMaps(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func stageDomain(values []DomainOutcome, domain string) (DomainOutcome, bool) {
	for _, value := range values {
		if value.Domain == domain {
			return value, true
		}
	}
	return DomainOutcome{}, false
}

func writeReleasedBundleFixture(t *testing.T, path string, files map[string][]byte, secrets map[string]string) {
	t.Helper()
	type bundleFile struct {
		Path string `json:"path"`
		Mode uint32 `json:"mode,omitempty"`
		Data []byte `json:"data"`
	}
	type platform struct {
		OS   string `json:"os"`
		Arch string `json:"arch"`
	}
	type bundle struct {
		Version   int               `json:"version"`
		CreatedAt time.Time         `json:"created_at"`
		Source    platform          `json:"source"`
		Files     []bundleFile      `json:"files"`
		Secrets   map[string]string `json:"secrets,omitempty"`
	}
	items := make([]bundleFile, 0, len(files))
	for name, data := range files {
		items = append(items, bundleFile{Path: name, Mode: 0600, Data: data})
	}
	plain, err := json.Marshal(bundle{
		Version: 1, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Source: platform{OS: "linux", Arch: "amd64"}, Files: items, Secrets: secrets,
	})
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	zipper := gzip.NewWriter(&compressed)
	if _, err := zipper.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := zipper.Close(); err != nil {
		t.Fatal(err)
	}
	key := sha256.Sum256([]byte("chatgpt-mcp portable config bundle v1 / mewis.me"))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	var aead cipher.AEAD
	aead, err = cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		t.Fatal(err)
	}
	magic := []byte("CGMCFG\x00\x01")
	sealed := aead.Seal(nil, nonce, compressed.Bytes(), magic)
	encoded := append(append(append([]byte{}, magic...), nonce...), sealed...)
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
}
