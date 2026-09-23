package palette

import (
	"fmt"
	"testing"

	"go.mewis.me/codemcp/internal/interface/tui/action"
)

func TestRankPrefersTitleAndCommandPathMatches(t *testing.T) {
	actions := []action.Action{
		{ID: "logs", Title: "Go to Logs", Category: "App", Keywords: []string{"journal"}, CommandPath: []string{"tui", "logs"}},
		{ID: "verify", Title: "Verify configuration", Category: "Config", Keywords: []string{"validate"}, CommandPath: []string{"config", "verify"}},
		{ID: "workspace", Title: "Register workspace", Category: "Workspace", CommandPath: []string{"workspace", "register"}},
	}
	results := Rank(actions, "config verify", action.Context{})
	if len(results) == 0 || results[0].Action.ID != "verify" {
		t.Fatalf("results = %#v", results)
	}
	results = Rank(actions, "reg ws", action.Context{})
	if len(results) == 0 || results[0].Action.ID != "workspace" {
		t.Fatalf("fuzzy results = %#v", results)
	}
}

func TestRankBoostsCurrentResource(t *testing.T) {
	actions := []action.Action{
		{ID: "generic", Title: "Configure server", Category: "MCP", CommandPath: []string{"mcp", "server", "configure"}},
		{ID: "current", Title: "Configure github", Category: "MCP", Keywords: []string{"github"}, CommandPath: []string{"mcp", "server", "configure", "github"}},
	}
	results := Rank(actions, "configure", action.Context{Route: "mcp", ResourceID: "github"})
	if len(results) != 2 || results[0].Action.ID != "current" {
		t.Fatalf("results = %#v", results)
	}
}

func TestRecentActionsBoostEmptyPaletteButNotStrongQuery(t *testing.T) {
	actions := []action.Action{
		{ID: "logs", Title: "Go to Logs", Category: "App"},
		{ID: "config", Title: "Verify configuration", Category: "Config", CommandPath: []string{"config", "verify"}},
	}
	results := RankWithRecent(actions, "", action.Context{}, []string{"config"})
	if len(results) != 2 || results[0].Action.ID != "config" {
		t.Fatalf("recent results = %#v", results)
	}
	results = RankWithRecent(actions, "logs", action.Context{}, []string{"config"})
	if len(results) != 1 || results[0].Action.ID != "logs" {
		t.Fatalf("query results = %#v", results)
	}
}

func TestRankLargeRegistryFindsExactCapability(t *testing.T) {
	actions := largeActions(5000)
	results := Rank(actions, "workspace register 4321", action.Context{})
	if len(results) == 0 || results[0].Action.ID != "action-4321" {
		t.Fatalf("first result=%#v", results)
	}
}

func BenchmarkRankLargeRegistry(b *testing.B) {
	actions := largeActions(5000)
	b.ResetTimer()
	for range b.N {
		_ = Rank(actions, "workspace register 4321", action.Context{Route: "workspace", ResourceID: "ws_4321"})
	}
}

func largeActions(count int) []action.Action {
	actions := make([]action.Action, count)
	for index := range actions {
		actions[index] = action.Action{
			ID: fmt.Sprintf("action-%04d", index), Title: fmt.Sprintf("Register workspace %04d", index), Category: "Workspace",
			Keywords: []string{fmt.Sprintf("ws_%04d", index)}, CommandPath: []string{"workspace", "register", fmt.Sprintf("%04d", index)},
		}
	}
	return actions
}
