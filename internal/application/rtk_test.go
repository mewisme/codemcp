package application

import (
	"context"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/integrations/rtk"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestRTKOperationsUseCanonicalDispatcher(t *testing.T) {
	cfg := config.Default()
	cfg.Integrations.RTK.Enabled = false
	service := &RTKService{
		LoadConfig: func() (config.Config, error) { return cfg, nil },
		SetField: func(_ context.Context, key, raw string) (ConfigMutationResult, error) {
			next := cfg
			if err := config.SetValue(&next, key, raw); err != nil {
				return ConfigMutationResult{}, err
			}
			cfg = next
			return ConfigMutationResult{Config: cfg}, nil
		},
	}
	dispatcher := NewDispatcher()
	if err := BindRTKOperations(dispatcher, service); err != nil {
		t.Fatal(err)
	}
	for _, id := range []capability.ID{
		capability.IntegrationRTKStatus,
		capability.IntegrationRTKEnable,
		capability.IntegrationRTKDisable,
		capability.IntegrationRTKProbe,
		capability.IntegrationRTKInstall,
	} {
		if dispatcher.handlers[id] == nil {
			t.Fatalf("operation %s is not bound", id)
		}
		if _, ok := capability.Lookup(id); !ok {
			t.Fatalf("operation %s is not canonical", id)
		}
	}

	enabled, err := dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: capability.IntegrationRTKEnable})
	if err != nil {
		t.Fatal(err)
	}
	status, ok := enabled.Value.(rtk.Status)
	if !ok || !status.Enabled || !cfg.Integrations.RTK.Enabled {
		t.Fatalf("enabled=%#v cfg=%#v", enabled.Value, cfg.Integrations.RTK)
	}
	disabled, err := dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: capability.IntegrationRTKDisable})
	if err != nil {
		t.Fatal(err)
	}
	status, ok = disabled.Value.(rtk.Status)
	if !ok || status.Enabled || cfg.Integrations.RTK.Enabled {
		t.Fatalf("disabled=%#v cfg=%#v", disabled.Value, cfg.Integrations.RTK)
	}
}

func TestRTKProjectContextProjectionUsesCanonicalConfig(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	cfg := config.Default()
	cfg.Integrations.RTK.Enabled = false
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	instructions, err := RTKProjectContextInstruction(t.Context(), "ws_test", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(instructions) != 1 || instructions[0].ID != "rtk" || instructions[0].Source != "CodeMCP" || instructions[0].Content != "RTK command rewriting is disabled." {
		t.Fatalf("instructions=%#v", instructions)
	}

	manager := workspace.NewManager(t.TempDir() + "/workspaces.json")
	service := NewProjectContextService(t.Context(), manager)
	if len(service.IntegrationProviders) == 0 {
		t.Fatal("RTK provider missing from Project Context service")
	}
}

func TestRTKDiagnosticsDoNotExposeConfiguredPathInProjectContext(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	cfg := config.Default()
	cfg.Integrations.RTK.Enabled = true
	cfg.Integrations.RTK.Path = "/private/user/path/rtk"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	instructions, err := RTKProjectContextInstruction(t.Context(), "ws_test", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(instructions) != 1 {
		t.Fatalf("instructions=%#v", instructions)
	}
	if instructions[0].Content == "" || containsDiagnosticPath(instructions[0].Content, cfg.Integrations.RTK.Path) {
		t.Fatalf("diagnostic leaked configured path: %q", instructions[0].Content)
	}
}

func containsDiagnosticPath(content, path string) bool {
	return len(path) > 0 && len(content) >= len(path) && findSubstring(content, path)
}

func findSubstring(content, value string) bool {
	for index := 0; index+len(value) <= len(content); index++ {
		if content[index:index+len(value)] == value {
			return true
		}
	}
	return false
}
