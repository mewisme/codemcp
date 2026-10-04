package presentation

import (
	"bytes"
	"strings"
	"testing"
)

func TestTypedDesignEntityListMatchesRailHierarchy(t *testing.T) {
	var output bytes.Buffer
	New(&output, ModeHuman, Capabilities{Width: 100, Unicode: true}).Render(Design{
		Title:      "Configuration",
		Completion: "Done",
		Blocks: []DesignBlock{
			EntityList{
				Title: "Settings · 2",
				Items: []Entity{
					{Title: "admin", Fields: []Field{{Label: "http.admin.enabled", Value: true}, {Label: "http.admin.port", Value: 37422}}},
					{Title: "server", Fields: []Field{{Label: "http.mcp.enabled", Value: false}}},
				},
			},
		},
	})

	want := "┌  Configuration\n" +
		"│\n" +
		"│  ▸ Settings · 2\n" +
		"│\n" +
		"│  ▸ admin\n" +
		"│  │  http.admin.enabled — true\n" +
		"│  │  http.admin.port — 37422\n" +
		"│\n" +
		"│  ▸ server\n" +
		"│  │  http.mcp.enabled — false\n" +
		"│\n" +
		"└  Done\n"
	if got := output.String(); got != want {
		t.Fatalf("typed entity-list design mismatch:\nwant=%q\ngot =%q", want, got)
	}
}

func TestTypedDesignBlocksComposeExistingPresenterPrimitives(t *testing.T) {
	var output bytes.Buffer
	New(&output, ModeHuman, Capabilities{Width: 100, Unicode: true}).Render(Design{
		Title: "Typed designs",
		Blocks: []DesignBlock{
			StatusBlock{Kind: StatusSuccess, Message: "Ready", Fields: []Field{{Label: "pid", Value: 42}}},
			FieldSection{Title: "Runtime", Fields: []Field{{Label: "state", Value: "running"}}},
			StateSection{Kind: StatusInactive, Title: "Tunnel", Fields: []Field{{Label: "tunnel.enabled", Value: false}}},
			StatusChild{Kind: StatusWarning, Message: "Metadata unavailable"},
			StateChild{Kind: StatusInactive, Label: "OpenAI Secure MCP Tunnel", Value: "disabled"},
			RowSection{Title: "Rows", Headers: []string{"ID", "State"}, Rows: []Row{{"one", "ready"}}},
			TextListSection{Title: "Items", Items: []string{"alpha"}},
			NoteBlock{Title: "Hint", Body: "Use typed layouts."},
		},
		Completion: "Done",
	})

	got := output.String()
	for _, want := range []string{
		"✓  Ready",
		"│  pid — 42",
		"│  ▸ Runtime",
		"│  state — running",
		"◇  Tunnel",
		"│  │  tunnel.enabled — false",
		"│  ! Metadata unavailable",
		"│  ◇ OpenAI Secure MCP Tunnel — disabled",
		"│  ▸ Rows",
		"│  ID   State",
		"│  one  ready",
		"│  ▸ Items",
		"│  └─ alpha",
		"│  · Hint",
		"└  Done",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("typed design output missing %q: %q", want, got)
		}
	}
}

func TestRenderBlockRendersTypedFragmentWithoutOwningFrame(t *testing.T) {
	var output bytes.Buffer
	p := New(&output, ModeHuman, Capabilities{Width: 100, Unicode: true})
	p.Frame("Fragment")
	p.RenderBlock(EntityList{Items: []Entity{{Title: "one", Fields: []Field{{Label: "name", Value: "One"}}}}})
	p.Complete("Done")

	got := output.String()
	for _, want := range []string{"┌  Fragment", "│  ▸ one", "│  │  name — One", "└  Done"} {
		if !strings.Contains(got, want) {
			t.Fatalf("typed fragment output missing %q: %q", want, got)
		}
	}
}

func TestTypedDesignIsSilentInJSONMode(t *testing.T) {
	var output bytes.Buffer
	New(&output, ModeJSON, Capabilities{Width: 100, Unicode: true}).Render(Design{
		Title:      "ignored",
		Completion: "ignored",
		Blocks: []DesignBlock{
			FieldSection{Title: "ignored", Fields: []Field{{Label: "ignored", Value: "ignored"}}},
			EntityList{Title: "ignored", Items: []Entity{{Title: "ignored", Fields: []Field{{Label: "ignored", Value: "ignored"}}}}},
			StatusBlock{Kind: StatusSuccess, Message: "ignored"},
			StateSection{Kind: StatusInactive, Title: "ignored"},
		},
	})
	if output.Len() != 0 {
		t.Fatalf("typed design wrote JSON-mode output: %q", output.String())
	}
}
