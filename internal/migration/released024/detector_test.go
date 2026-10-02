package released024

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/install"
)

func TestDetectReleasedStateBuildsDeterministicSecretSafeManifest(t *testing.T) {
	currentRoot := filepath.Join(t.TempDir(), "current-root-must-not-exist")
	t.Setenv("CM_CONFIG_DIR", currentRoot)
	home := t.TempDir()
	root := filepath.Join(home, "released")
	workspaceRoot := filepath.Join(home, "project")
	if err := os.MkdirAll(filepath.Join(root, "workspaces", "ws_one", "checkpoints", "cp_one"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "runtime"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "logs"), 0700); err != nil {
		t.Fatal(err)
	}
	secret := "released-super-secret"
	writeFixture(t, filepath.Join(root, legacyRootMarkerName), legacyRootMarkerValue+"\n")
	writeJSONFixture(t, filepath.Join(root, "config.json"), map[string]any{
		"server":   map[string]any{"enabled": true},
		"auth":     map[string]any{"mcp_token_hash": "hash-only"},
		"features": map[string]any{},
		"tunnel":   map[string]any{"api_key": secret},
	})
	writeJSONFixture(t, filepath.Join(root, "upstream.json"), map[string]any{
		"version": 1,
		"servers": []map[string]any{{"id": "up_one", "name": "One", "transport": "stdio", "command": "echo"}},
	})
	writeJSONFixture(t, filepath.Join(root, "workspaces.json"), map[string]any{
		"version":    4,
		"workspaces": []map[string]any{{"id": "ws_one", "path": workspaceRoot}},
		"containers": []any{},
	})
	writeFixture(t, filepath.Join(root, "workspaces", "ws_one", "MEMORY.md"), "durable memory")
	writeJSONFixture(t, filepath.Join(root, "workspaces", "ws_one", "shell.json"), map[string]any{"workspace_id": "ws_one", "cwd": workspaceRoot})
	writeJSONFixture(t, filepath.Join(root, "workspaces", "ws_one", "checkpoints", "cp_one", "manifest.json"), map[string]any{"id": "cp_one"})
	writeJSONFixture(t, filepath.Join(root, "state", "instance.json"), map[string]any{
		"version": 1,
		"identity": map[string]any{
			"id": "inst_00000000000000000000000000000000", "name": "legacy-host", "created_at": "2026-01-02T03:04:05Z",
		},
	})
	writeJSONFixture(t, filepath.Join(root, "runtime", "environment.json"), map[string]any{"values": map[string]any{"PATH": "/legacy"}})
	writeJSONFixture(t, filepath.Join(root, ".runtime-control.json"), map[string]any{"pid": 99})
	serviceLauncherName := ".service-launcher-" + historicalServiceID(root, "user") + ".vbs"
	writeFixture(t, filepath.Join(root, serviceLauncherName), "service launcher")
	writeJSONFixture(t, filepath.Join(root, "tunnels", "tunnel_fixture.json"), map[string]any{"version": 1, "id": "tunnel_fixture"})
	writeJSONLineFixture(t, filepath.Join(root, "logs", "runtime.jsonl"), map[string]any{"event": "ready"})
	writeJSONFixture(t, filepath.Join(root, "tui-state.json"), map[string]any{"version": 1, "recent_actions": []string{"logs"}})
	writeFixture(t, filepath.Join(root, "instructions", "AGENTS.md"), "Use canonical owners.\n")
	writeJSONFixture(t, filepath.Join(root, "instructions", "global.json"), map[string]any{
		"version": 1, "context": "legacy context",
		"rules":   []map[string]any{{"id": "legacy", "enabled": true, "content": "legacy rule"}},
		"sources": map[string]any{"claude": map[string]any{"enabled": false}},
	})
	writeJSONFixture(t, filepath.Join(root, "state", "update.json"), map[string]any{"status": "pending"})
	writeFixture(t, filepath.Join(root, "runtime", "processes.json"), "transient")
	writeFixture(t, filepath.Join(root, "runtime.pid"), "123")
	writeFixture(t, filepath.Join(root, "migration.lock"), "held")

	options := isolatedOptions(home, root)
	before := treeSnapshot(t, root)
	first, err := Detect(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Detect(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	after := treeSnapshot(t, root)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("read-only detector changed source: before=%#v after=%#v", before, after)
	}
	if first.SourceSHA256 == "" || first.SourceSHA256 != second.SourceSHA256 || !reflect.DeepEqual(first.Artifacts, second.Artifacts) {
		t.Fatalf("manifest is not deterministic: first=%#v second=%#v", first, second)
	}
	if !first.Found || first.Source.Root != filepath.Clean(root) || !first.Source.Marker.Verified ||
		first.Source.Marker.Name != legacyRootMarkerName || first.Source.Marker.Value != legacyRootMarkerValue {
		t.Fatalf("source descriptor=%#v", first.Source)
	}
	if !first.Config.Exists || first.Config.Format != "json" || first.Config.Schema != "chatgpt-mcp-0.2.24" || first.Config.SchemaVersion != "unversioned" {
		t.Fatalf("config=%#v", first.Config)
	}
	if !first.Instance.Valid || first.Instance.Version != 1 || first.Instance.ID != "inst_00000000000000000000000000000000" {
		t.Fatalf("instance=%#v", first.Instance)
	}
	if !first.Integrations.LegacyFeatures || first.Integrations.SourceFormat != "json" {
		t.Fatalf("integrations=%#v", first.Integrations)
	}
	if first.Workspaces.Version != 4 || len(first.Workspaces.Workspaces) != 1 || !first.Workspaces.Workspaces[0].HasState {
		t.Fatalf("workspaces=%#v", first.Workspaces)
	}
	if first.Credentials.Inventory.Recoverable != 1 || !first.Credentials.Tunnel.RuntimeKeyConfigured {
		t.Fatalf("credentials=%#v", first.Credentials)
	}
	if first.Upstream.Version != 1 || first.Upstream.Servers != 1 {
		t.Fatalf("upstream=%#v", first.Upstream)
	}
	classes := map[string]Classification{}
	for _, artifact := range first.Artifacts {
		classes[artifact.Path] = artifact.Classification
	}
	for path, want := range map[string]Classification{
		"config.json":                 ClassDurableMigrate,
		"upstream.json":               ClassDurableMigrate,
		"workspaces/ws_one/MEMORY.md": ClassDurableMigrate,
		"workspaces/ws_one/checkpoints/cp_one/manifest.json": ClassDurableMigrate,
		"state/instance.json":                                ClassDurableMigrate,
		"logs/runtime.jsonl":                                 ClassDurableMigrate,
		"tui-state.json":                                     ClassDurableMigrate,
		"instructions/AGENTS.md":                             ClassDurableMigrate,
		"instructions/global.json":                           ClassDurableMigrate,
		"runtime/environment.json":                           ClassRegenerate,
		serviceLauncherName:                                  ClassRegenerate,
		".runtime-control.json":                              ClassTransientDrop,
		"tunnels/tunnel_fixture.json":                        ClassTransientDrop,
		"state/update.json":                                  ClassTransientDrop,
		"runtime/processes.json":                             ClassTransientDrop,
		"runtime.pid":                                        ClassTransientDrop,
		"migration.lock":                                     ClassTransientDrop,
	} {
		if classes[path] != want {
			t.Errorf("artifact %s class=%q want=%q", path, classes[path], want)
		}
	}
	if first.Unsupported != 0 {
		t.Fatalf("known released state was classified unsupported: %d", first.Unsupported)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("manifest leaked raw credential: %s", encoded)
	}
	if _, err := os.Stat(currentRoot); !os.IsNotExist(err) {
		t.Fatalf("detector activated current config root: %v", err)
	}
	if len(first.Services) != 2 || first.Services[0].Ownership == OwnershipAbsent {
		t.Fatalf("services=%#v", first.Services)
	}
}

func TestDetectDoesNotFailClosedOnLargeOwnedCheckpointHistory(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "released")
	writeFixture(t, filepath.Join(root, legacyRootMarkerName), legacyRootMarkerValue)
	manifestPath := filepath.Join(root, "workspaces", "ws_one", "checkpoints", "data", "cp_large", "manifest.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(manifestPath, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxRegularFileBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	manifest, err := Detect(t.Context(), isolatedOptions(home, root))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Unsupported != 0 {
		t.Fatalf("large owned checkpoint blocked cutover: unsupported=%d artifacts=%#v", manifest.Unsupported, manifest.Artifacts)
	}
	for _, artifact := range manifest.Artifacts {
		if artifact.Path == "workspaces/ws_one/checkpoints/data/cp_large/manifest.json" {
			if artifact.Kind != "checkpoint" || artifact.Classification != ClassDurableMigrate {
				t.Fatalf("large checkpoint artifact=%#v", artifact)
			}
			return
		}
	}
	t.Fatal("large checkpoint artifact missing")
}

func TestResolveSourceExplicitWinsAndImplicitAmbiguityFailsClosed(t *testing.T) {
	home := t.TempDir()
	defaultRoot := filepath.Join(home, ".config", "chatgpt-mcp")
	envRoot := filepath.Join(home, "legacy-env")
	explicitRoot := filepath.Join(home, "explicit")
	for _, root := range []string{defaultRoot, envRoot, explicitRoot} {
		writeFixture(t, filepath.Join(root, legacyRootMarkerName), legacyRootMarkerValue)
	}
	lookup := func(key string) string {
		if key == "CHATGPT_MCP_CONFIG_DIR" {
			return envRoot
		}
		return ""
	}
	if _, _, err := resolveSource(Options{HomeDir: home, LookupEnv: lookup}); err == nil || !strings.Contains(err.Error(), "multiple authoritative") {
		t.Fatalf("implicit ambiguity err=%v", err)
	}
	descriptor, found, err := resolveSource(Options{HomeDir: home, LookupEnv: lookup, SourceRoot: explicitRoot})
	if err != nil {
		t.Fatal(err)
	}
	if !found || descriptor.Root != filepath.Clean(explicitRoot) || descriptor.RootSource != "explicit" {
		t.Fatalf("explicit descriptor=%#v found=%t", descriptor, found)
	}
}

func TestDetectRejectsAmbiguousStructuredConfig(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "released")
	writeFixture(t, filepath.Join(root, legacyRootMarkerName), legacyRootMarkerValue)
	writeJSONFixture(t, filepath.Join(root, "config.json"), map[string]any{"server": map[string]any{}})
	writeFixture(t, filepath.Join(root, "config.yaml"), "server: {}\n")
	_, err := Detect(t.Context(), isolatedOptions(home, root))
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous config err=%v", err)
	}
}

func TestDetectPreservesUnverifiedAndPackageManagedLaunchers(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "released")
	writeFixture(t, filepath.Join(root, legacyRootMarkerName), legacyRootMarkerValue)
	options := isolatedOptions(home, root)
	options.FindInstallations = func(install.Layout, string) ([]install.LegacyInstallation, error) {
		return []install.LegacyInstallation{
			{Path: "/tmp/chatgpt-mcp", Target: "/tmp/chatgpt-mcp", Method: install.MethodUnknown, Reason: "same-name unrelated"},
			{Path: "/opt/homebrew/bin/chatgpt-mcp", Target: "/opt/homebrew/Cellar/chatgpt-mcp/0.2.24/bin/chatgpt-mcp", Method: install.MethodHomebrew, PackageManaged: true, Reason: "managed by Homebrew"},
		}, nil
	}
	options.FindAliases = func() ([]install.LegacyAlias, error) {
		return []install.LegacyAlias{{Path: "/tmp/cgm", Target: "/tmp/chatgpt-mcp", Reason: "unverified target"}}, nil
	}
	manifest, err := Detect(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	ownership := map[string]Ownership{}
	removable := map[string]bool{}
	for _, item := range manifest.Launchers {
		ownership[item.Path] = item.Ownership
		removable[item.Path] = item.Removable
	}
	if ownership["/tmp/chatgpt-mcp"] != OwnershipAmbiguous || removable["/tmp/chatgpt-mcp"] {
		t.Fatalf("unrelated launcher was claimed: %#v", manifest.Launchers)
	}
	if ownership["/opt/homebrew/bin/chatgpt-mcp"] != OwnershipPackageManager || removable["/opt/homebrew/bin/chatgpt-mcp"] {
		t.Fatalf("package manager launcher was claimed: %#v", manifest.Launchers)
	}
	if ownership["/tmp/cgm"] != OwnershipAmbiguous || removable["/tmp/cgm"] {
		t.Fatalf("unverified alias was claimed: %#v", manifest.Launchers)
	}
}

func TestDetectReportsUnknownSourceAsFailClosedWithoutDeletingIt(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "released")
	writeFixture(t, filepath.Join(root, legacyRootMarkerName), legacyRootMarkerValue)
	unknown := filepath.Join(root, "operator-custom.dat")
	writeFixture(t, unknown, "preserve me")
	manifest, err := Detect(t.Context(), isolatedOptions(home, root))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Unsupported != 1 {
		t.Fatalf("unsupported=%d artifacts=%#v", manifest.Unsupported, manifest.Artifacts)
	}
	data, err := os.ReadFile(unknown)
	if err != nil || string(data) != "preserve me" {
		t.Fatalf("unknown source was changed: data=%q err=%v", data, err)
	}
}

func TestDetectKeepsReservedMigrationNamespacesFailClosedWhenOwnershipIsInvalid(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "released")
	writeFixture(t, filepath.Join(root, legacyRootMarkerName), legacyRootMarkerValue)
	writeFixture(t, filepath.Join(root, ".service-launcher-operator.vbs"), "operator data")
	writeJSONFixture(t, filepath.Join(root, "tunnels", "tunnel_expected.json"), map[string]any{
		"version": 1,
		"id":      "tunnel_other",
	})

	manifest, err := Detect(t.Context(), isolatedOptions(home, root))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Unsupported != 2 {
		t.Fatalf("unsupported=%d artifacts=%#v", manifest.Unsupported, manifest.Artifacts)
	}
}

func TestDetectRegeneratesInvalidInstanceAndSkipsInvalidOptionalViews(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "released")
	writeFixture(t, filepath.Join(root, legacyRootMarkerName), legacyRootMarkerValue)
	writeFixture(t, filepath.Join(root, "state", "instance.json"), `{"version":1,"identity":{"id":"invalid"}}`)
	writeFixture(t, filepath.Join(root, "logs", "runtime.jsonl"), "not-json\n")
	writeFixture(t, filepath.Join(root, "tui-state.json"), "{invalid")
	manifest, err := Detect(t.Context(), isolatedOptions(home, root))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Instance.Valid || manifest.Instance.Reason == "" {
		t.Fatalf("invalid instance was accepted: %#v", manifest.Instance)
	}
	classes := map[string]Classification{}
	for _, artifact := range manifest.Artifacts {
		classes[artifact.Path] = artifact.Classification
	}
	if classes["state/instance.json"] != ClassRegenerate ||
		classes["logs/runtime.jsonl"] != ClassOptionalSkipWithReport ||
		classes["tui-state.json"] != ClassOptionalSkipWithReport {
		t.Fatalf("invalid optional state classes=%#v", classes)
	}
}

func TestDetectSkipsUnsupportedReleasedTUIStateVersion(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "released")
	writeFixture(t, filepath.Join(root, legacyRootMarkerName), legacyRootMarkerValue)
	writeJSONFixture(t, filepath.Join(root, "tui-state.json"), map[string]any{
		"version": 2, "recent_actions": []string{"logs"},
	})
	manifest, err := Detect(t.Context(), isolatedOptions(home, root))
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range manifest.Artifacts {
		if artifact.Path == "tui-state.json" {
			if artifact.Classification != ClassOptionalSkipWithReport {
				t.Fatalf("unsupported TUI state classification=%q", artifact.Classification)
			}
			return
		}
	}
	t.Fatal("TUI state artifact not found")
}

func TestReleasedDetectorHasNoLiveWorkspaceOrMutationAuthority(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve detector test source")
	}
	dir := filepath.Dir(current)
	for _, name := range []string{
		"detector.go",
		"platform_unix.go",
		"platform_windows.go",
		"service_linux.go",
		"service_darwin.go",
		"service_windows.go",
	} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		body := string(data)
		for _, forbidden := range []string{
			`"go.mewis.me/codemcp/internal/workspace"`,
			`"go.mewis.me/codemcp/internal/service"`,
			`"go.mewis.me/codemcp/internal/configformat"`,
			"os.WriteFile(",
			"os.Remove(",
			"os.RemoveAll(",
			"os.Rename(",
			"os.MkdirAll(",
		} {
			if strings.Contains(body, forbidden) {
				t.Errorf("%s gained live/mutation authority via %q", name, forbidden)
			}
		}
	}
}

func TestHistoricalServiceIDMatchesReleasedFormula(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".config", "chatgpt-mcp")
	sum := sha256.Sum256([]byte(filepath.Clean(root)))
	want := "chatgpt-mcp-user-" + hex.EncodeToString(sum[:6])
	if got := historicalServiceID(root, "user"); got != want || !strings.HasPrefix(got, "chatgpt-mcp-user-") {
		t.Fatalf("historical service id=%q want=%q", got, want)
	}
}

func isolatedOptions(home, root string) Options {
	return Options{
		HomeDir:           home,
		SourceRoot:        root,
		LookupEnv:         func(string) string { return "" },
		FindInstallations: func(install.Layout, string) ([]install.LegacyInstallation, error) { return nil, nil },
		FindAliases:       func() ([]install.LegacyAlias, error) { return nil, nil },
		InspectServices: func(_ context.Context, descriptor SourceDescriptor) ([]ServiceState, error) {
			return []ServiceState{
				{Scope: "system", ID: historicalServiceID(descriptor.Root, "system"), Backend: "test", Installed: true, Enabled: true, Bootstrapped: true, Running: true, Ownership: OwnershipVerified, ConfigRoot: descriptor.Root},
				{Scope: "user", ID: historicalServiceID(descriptor.Root, "user"), Backend: "test", Installed: true, Enabled: true, Bootstrapped: true, Running: true, Ownership: OwnershipVerified, ConfigRoot: descriptor.Root},
			}, nil
		},
	}
}

func writeFixture(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func writeJSONFixture(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, path, string(data))
}

func writeJSONLineFixture(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, path, string(data)+"\n")
}

func treeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		sha, size, err := hashFile(path, maxRegularFileBytes)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = sha + ":" + strconv.FormatInt(size, 10)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
