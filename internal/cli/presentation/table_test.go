package presentation

import (
	"bytes"
	"strings"
	"testing"
)

func TestTableStylesRenderFromOneWidthModel(t *testing.T) {
	tests := []struct {
		name   string
		border TableBorderStyle
		want   []string
	}{
		{name: "bare", border: TableBare, want: []string{"│  Name     State", "│  archify  managed"}},
		{name: "outline", border: TableOutline, want: []string{"│  ┌", "│  │ Name", "│  ├", "│  └"}},
		{name: "grid", border: TableGrid, want: []string{"│  ┌", "│  │ archify", "│  ├", "│  │ ui-ux", "│  └"}},
	}
	rows := []Row{{"archify", "managed"}, {"ui-ux", "native"}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			p := New(&output, ModeHuman, Capabilities{Width: 60, Unicode: true})
			p.Table([]string{"Name", "State"}, rows, TableOptions{Border: test.border, Layout: TableFitContent, Depth: 1})
			for _, want := range test.want {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("%s table missing %q: %q", test.name, want, output.String())
				}
			}
			assertDisplayLinesFit(t, output.String(), 60)
		})
	}
}

func TestFitContentTableNeverWrapsCellsAndFallsBackWhenTooWide(t *testing.T) {
	var output bytes.Buffer
	p := New(&output, ModeHuman, Capabilities{Width: 30, Unicode: true})
	long := "github:owner/repository-with-a-long-name"
	p.Table(
		[]string{"Name", "Source"},
		[]Row{{"archify", long}},
		TableOptions{Border: TableOutline, Layout: TableFitContent, Depth: 1},
	)
	got := output.String()
	if strings.Contains(got, "┌") || strings.Contains(got, "┬") {
		t.Fatalf("over-wide fit-content table kept a box and risked cell wrapping: %q", got)
	}
	for _, want := range []string{"│  └─ archify", "│  │  Source"} {
		if !strings.Contains(got, want) {
			t.Fatalf("fit-content fallback missing %q: %q", want, got)
		}
	}
	assertDisplayLinesFit(t, got, 30)
}

func TestNestedTableBudgetsWidthAfterDepthPrefix(t *testing.T) {
	var output bytes.Buffer
	p := New(&output, ModeHuman, Capabilities{Width: 28, Unicode: true})
	p.Table(
		[]string{"Name", "State"},
		[]Row{{"service-one", "ready"}, {"service-two", "degraded"}},
		TableOptions{Border: TableGrid, Layout: TableAdaptive, Depth: 2},
	)
	got := output.String()
	if !strings.Contains(got, "│  │  ") {
		t.Fatalf("nested table lost depth prefix: %q", got)
	}
	assertDisplayLinesFit(t, got, 28)
}

func TestAdaptiveBareTableGeneralizesBeyondThreeColumns(t *testing.T) {
	var output bytes.Buffer
	p := New(&output, ModeHuman, Capabilities{Width: 58, Unicode: true})
	p.Table(
		[]string{"ID", "State", "Workspace", "Description"},
		[]Row{{"agent_1", "running", "ws_demo", "This description is intentionally long enough to require wrapping"}},
		TableOptions{Border: TableBare, Layout: TableAdaptive, Depth: 1},
	)
	got := output.String()
	if !strings.Contains(got, "ID") || !strings.Contains(got, "Description") {
		t.Fatalf("adaptive table lost headers: %q", got)
	}
	if strings.Contains(got, "column 2") || strings.Contains(got, "column 3") {
		t.Fatalf("adaptive table fell back to stacked fields unexpectedly: %q", got)
	}
	assertDisplayLinesFit(t, got, 58)
}

func TestAlignedRowsPreserveSharedWidthsAcrossGroupedTables(t *testing.T) {
	headers := []string{"Key", "Value", "Accepts"}
	all := []Row{
		{"http.admin.enabled", "true", "true | false"},
		{"llm.provider", "openai", "<provider-id>"},
	}
	widths := AlignedRowWidths(headers, all...)

	var output bytes.Buffer
	p := New(&output, ModeHuman, Capabilities{Width: 100, Unicode: true})
	p.AlignedNestedRowsWithWidths(headers, widths, all[:1]...)
	p.AlignedNestedRowsWithWidths(headers, widths, all[1:]...)
	lines := strings.Split(output.String(), "\n")
	headersSeen := make([]string, 0, 2)
	for _, line := range lines {
		if strings.Contains(line, "Key") && strings.Contains(line, "Accepts") {
			headersSeen = append(headersSeen, line)
		}
	}
	if len(headersSeen) != 2 || headersSeen[0] != headersSeen[1] {
		t.Fatalf("grouped table headers do not share global widths: %#v output=%q", headersSeen, output.String())
	}
}
