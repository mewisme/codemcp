package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestWorkspaceContainerCommands(t *testing.T) {
	defer configformat.SetRootPath("")
	configRoot := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(configRoot); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	registered := executeRequestCommand(t, configRoot, []string{"workspace", "register", root})
	if !strings.Contains(strings.ToLower(registered), "workspace registered") {
		t.Fatalf("register output = %s", registered)
	}
	items, err := workspace.NewManager(workspace.DefaultStorePath()).List()
	if err != nil || len(items) != 1 {
		t.Fatalf("workspaces = %#v err=%v", items, err)
	}
	created := executeRequestCommand(t, configRoot, []string{"workspace", "container", "create", "Backend"})
	if !strings.Contains(strings.ToLower(created), "workspace container created") {
		t.Fatalf("create output = %s", created)
	}
	containers, err := workspace.NewManager(workspace.DefaultStorePath()).ListContainers()
	if err != nil || len(containers) != 1 {
		t.Fatalf("containers = %#v err=%v", containers, err)
	}
	id := containers[0].ID
	executeRequestCommand(t, configRoot, []string{"workspace", "container", "add", id, items[0].ID})
	listed := executeRequestCommand(t, configRoot, []string{"workspace", "container", "list", "--json"})
	var decoded []workspace.WorkspaceContainer
	if err := json.Unmarshal([]byte(listed), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 1 || len(decoded[0].WorkspaceIDs) != 1 || decoded[0].WorkspaceIDs[0] != items[0].ID {
		t.Fatalf("listed = %#v", decoded)
	}
	executeRequestCommand(t, configRoot, []string{"workspace", "container", "remove", id, items[0].ID})
	executeRequestCommand(t, configRoot, []string{"workspace", "container", "rename", id, "Services"})
	shown := executeRequestCommand(t, configRoot, []string{"workspace", "container", "show", id, "--json"})
	if !strings.Contains(shown, `"name": "Services"`) && !strings.Contains(shown, `"name":"Services"`) {
		t.Fatalf("show = %s", shown)
	}
	executeRequestCommand(t, configRoot, []string{"workspace", "container", "delete", id})
	containers, err = workspace.NewManager(workspace.DefaultStorePath()).ListContainers()
	if err != nil || len(containers) != 0 {
		t.Fatalf("containers after delete = %#v err=%v", containers, err)
	}
}
