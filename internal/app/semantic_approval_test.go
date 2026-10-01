package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/tools"
)

func TestReloadConfigConvergesSemanticApprovalPolicy(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	cfg := config.Default()
	cfg.HTTP.MCP.Auth.TokenHash = "test-mcp-hash"
	cfg.HTTP.Admin.Auth.TokenHash = "test-admin-hash"
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	workspace, err := app.Tools.Workspaces.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := tools.WithCallSource(context.Background(), "tunnel")
	ctx = tools.WithApprovalCorrelation(ctx, "semantic-reload", "request-one")
	firstTarget := filepath.Join(root, "before-reload")
	first, err := app.Tools.Call(ctx, "run_command", map[string]any{
		"workspace_id": workspace.ID, "command": "touch " + filepath.Base(firstTarget),
	})
	if err != nil || first.IsError {
		t.Fatalf("disabled semantic command=%#v err=%v", first, err)
	}
	if _, err := os.Stat(firstTarget); err != nil {
		t.Fatal(err)
	}

	next := cfg
	next.Approval.Semantic.Enabled = true
	if err := app.ReloadConfig(next); err != nil {
		t.Fatal(err)
	}
	secondTarget := filepath.Join(root, "after-reload")
	second, err := app.Tools.Call(tools.WithApprovalCorrelation(ctx, "semantic-reload-two", "request-two"), "run_command", map[string]any{
		"workspace_id": workspace.ID, "command": "touch " + filepath.Base(secondTarget),
	})
	if err != nil || !second.IsError {
		t.Fatalf("enabled unavailable semantic command=%#v err=%v", second, err)
	}
	if len(second.Content) == 0 {
		t.Fatalf("missing semantic approval result: %#v", second)
	}
	if _, err := os.Stat(secondTarget); !os.IsNotExist(err) {
		t.Fatalf("enabled unavailable semantic executed mutation: %v", err)
	}
}
