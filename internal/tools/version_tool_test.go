package tools

import (
	"context"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/checkpoint"
	"go.mewis.me/codemcp/internal/version"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestGetVersionTool(t *testing.T) {
	oldVersion, oldCommit, oldDate := version.Version, version.Commit, version.Date
	oldStartedAt := processStartedAt
	oldMachineUptime := machineUptime
	defer func() {
		version.Version, version.Commit, version.Date = oldVersion, oldCommit, oldDate
		processStartedAt = oldStartedAt
		machineUptime = oldMachineUptime
	}()
	version.Version, version.Commit, version.Date = "0.0.7", "abc123", "2026-08-30T19:26:57Z"
	processStartedAt = time.Now().UTC().Add(-90 * time.Second)
	machineUptime = func() (time.Duration, error) { return 36*time.Hour + 2*time.Minute + 3*time.Second, nil }
	registry := NewRegistry()
	RegisterCore(registry, workspace.NewManager(t.TempDir()+"/workspaces.json"), checkpoint.NewStore(t.TempDir()))
	schema, ok := registry.Schema("get_version")
	if !ok || schema.Annotations["readOnlyHint"] != true {
		t.Fatalf("get_version schema = %#v", schema)
	}
	result, err := registry.Call(context.Background(), "get_version", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := result.StructuredContent.(VersionResult)
	if !ok {
		t.Fatalf("structured content = %#v", result.StructuredContent)
	}
	if got.Version != "0.0.7" || got.Commit != "abc123" || got.BuildTime != "2026-08-30T19:26:57Z" || got.ServerStartedAt == "" || got.ServerUptime == "" || got.ServerUptimeSeconds < 89 || got.ServerUptimeSeconds > 91 || got.MachineUptime != "36h2m3s" || got.MachineUptimeSeconds != 129723 {
		t.Fatalf("version result = %#v", got)
	}
}
