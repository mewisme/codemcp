package application

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
	"go.mewis.me/codemcp/internal/instructionpolicy"
	"go.mewis.me/codemcp/internal/llm"
	mcpoauth "go.mewis.me/codemcp/internal/oauth"
	runtimeevent "go.mewis.me/codemcp/internal/runtime/event"
	producttelemetry "go.mewis.me/codemcp/internal/telemetry/product"
	"go.mewis.me/codemcp/internal/upstream"
	"go.mewis.me/codemcp/internal/workspace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func TestCanonicalPersistenceLayoutSeparatesMachineStateFromAuthoredContent(t *testing.T) {
	previous := configformat.RootPath()
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}

	telemetry := producttelemetry.NewIdentityStore()
	machineState := map[string]string{
		"config":                     config.DefaultPath(),
		"workspace registry":         workspace.DefaultStorePath(),
		"upstreams":                  upstream.Path(),
		"oauth":                      mcpoauth.Path(),
		"llm providers":              llm.NewStore(root).Path(),
		"product telemetry identity": telemetry.Path(),
		"completion sequence":        agentcompletion.DefaultSequencePath(),
		"runtime journal":            runtimeevent.Path(root),
		"instruction policy":         instructionpolicy.DefaultPath(),
	}
	for name, path := range machineState {
		if !pathWithin(root, path) {
			t.Errorf("%s escaped selected config root: %s", name, path)
		}
		switch filepath.Ext(path) {
		case ".json", ".jsonl":
		default:
			t.Errorf("%s uses non-JSON structured state path: %s", name, path)
		}
	}

	workspaceRoot := t.TempDir()
	local := workspacestate.New(workspaceRoot)
	if local.Root() != filepath.Join(workspaceRoot, ".cm") {
		t.Fatalf("workspace local root = %q", local.Root())
	}
	workspaceMachineState := []string{local.IdentityPath(), local.ConfigPath()}
	for _, path := range workspaceMachineState {
		if !pathWithin(local.Root(), path) || filepath.Ext(path) != ".json" {
			t.Errorf("workspace machine state is not canonical JSON under .cm: %s", path)
		}
	}
	for name, path := range map[string]string{
		"rules":   local.RulesRoot(),
		"skills":  local.SkillsRoot(),
		"prompts": local.PromptRoot(),
		"plans":   local.PlansRoot(),
	} {
		if !pathWithin(local.Root(), path) {
			t.Errorf("workspace authored %s root escaped .cm: %s", name, path)
		}
	}
	for name, path := range map[string]string{
		"rules":   filepath.Join(root, "rules"),
		"skills":  filepath.Join(root, "skills"),
		"prompts": filepath.Join(root, "prompts"),
	} {
		if !pathWithin(root, path) {
			t.Errorf("global authored %s root escaped config root: %s", name, path)
		}
	}
}

func TestUninitializeRemovesOwnedGlobalPersistenceAndTelemetryIdentity(t *testing.T) {
	previous := configformat.RootPath()
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	if _, err := Initialize(InitOptions{}); err != nil {
		t.Fatal(err)
	}

	telemetry := producttelemetry.NewIdentityStore()
	telemetry.NewID = func() (string, error) { return "123e4567-e89b-42d3-a456-426614174099", nil }
	if _, created, err := telemetry.Ensure(true, "https://telemetry.example/v1/products/codemcp/events"); err != nil || !created {
		t.Fatalf("telemetry identity created=%t err=%v", created, err)
	}
	if telemetry.Path() != filepath.Join(root, "state", "product-telemetry.json") {
		t.Fatalf("telemetry identity path = %q", telemetry.Path())
	}

	ownedRoots := []string{"rules", "skills", "prompts", "managed-assets", "runtime"}
	for _, name := range ownedRoots {
		path := filepath.Join(root, name, "owned")
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "sentinel"), []byte("owned"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ownedFiles := []string{
		".runtime-control.json",
		".llm-providers.lock",
		"workspace-registry.mutation.lock",
		"executions.json",
		"background-deliveries.json",
		"telegram-topics.json",
		"telegram-approval-messages.json",
		"telegram-operation-messages.json",
	}
	for _, name := range ownedFiles {
		if err := os.WriteFile(filepath.Join(root, name), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	unrelated := filepath.Join(root, "keep.txt")
	if err := os.WriteFile(unrelated, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := UninitializeContext(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	for _, name := range ownedRoots {
		if _, err := os.Stat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("owned root %s survived uninitialize: %v", name, err)
		}
	}
	for _, name := range ownedFiles {
		if _, err := os.Stat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("owned file %s survived uninitialize: %v", name, err)
		}
	}
	if _, err := os.Stat(telemetry.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("product telemetry identity survived uninitialize: %v", err)
	}
	data, err := os.ReadFile(unrelated)
	if err != nil || string(data) != "keep" {
		t.Fatalf("unrelated file data=%q err=%v", data, err)
	}
	if configformat.IsManagedRoot(root) {
		t.Fatal("managed root marker survived uninitialize")
	}
}

func pathWithin(root, path string) bool {
	root, path = filepath.Clean(root), filepath.Clean(path)
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && relative != "." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
