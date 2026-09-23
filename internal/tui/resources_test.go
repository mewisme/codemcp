package tui

import (
	"os"
	"path/filepath"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/upstream"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestQuickOpenResourcesIncludeLocalEntities(t *testing.T) {
	defer configformat.SetRootPath("")
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	workspaces := workspace.NewManager(workspace.DefaultStorePath())
	registered, err := workspaces.Register(project)
	if err != nil {
		t.Fatal(err)
	}
	container, err := workspaces.CreateContainer("Primary")
	if err != nil {
		t.Fatal(err)
	}
	upstreams := upstream.NewManager(upstream.NewStore(upstream.Path()))
	if err := upstreams.Load(); err != nil {
		t.Fatal(err)
	}
	if err := upstreams.Add(upstream.Server{ID: "docs", Name: "Docs MCP", Enabled: true, Transport: "http", URL: "https://example.invalid/mcp", Expose: "all"}); err != nil {
		t.Fatal(err)
	}
	resources, err := loadQuickOpenResources()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{registered.ID: false, container.ID: false, "docs": false, "config": false}
	for _, resource := range resources {
		if _, ok := want[resource.ID]; ok {
			want[resource.ID] = true
		}
	}
	for id, found := range want {
		if !found {
			t.Fatalf("Commands resource missing: %s", id)
		}
	}
}
