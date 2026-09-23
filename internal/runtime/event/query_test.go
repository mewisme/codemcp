package event

import (
	"testing"
	"time"
)

func TestQueryMatchCoversAllFilters(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	event := Event{
		Time: now, RunID: "run_abcdef", Level: "warn", Component: "TOOL", WorkspaceID: "ws_test", Tool: "run_command", Status: "error", Source: "tunnel", Name: "tool.call.failed", Message: "connection timeout",
		Fields: []Field{{Key: "attempt", Value: 2}},
	}
	since, until := now.Add(-time.Minute), now.Add(time.Minute)
	query := Query{Since: &since, Until: &until, RunID: "RUN_ABC", MinLevel: "info", Components: []string{"server", " tool "}, WorkspaceID: "WS_TEST", Tool: "RUN_COMMAND", Status: "ERROR", Source: "TUNNEL", EventGlob: "tool.*", Grep: "ATTEMPT 2"}
	if !query.Match(event) {
		t.Fatalf("full query did not match event: %#v", query)
	}

	before, after := now.Add(time.Second), now.Add(-time.Second)
	for _, test := range []struct {
		name  string
		query Query
	}{
		{name: "since", query: Query{Since: &before}},
		{name: "until", query: Query{Until: &after}},
		{name: "run", query: Query{RunID: "other"}},
		{name: "level", query: Query{MinLevel: "error"}},
		{name: "component", query: Query{Components: []string{"SERVER"}}},
		{name: "workspace", query: Query{WorkspaceID: "other"}},
		{name: "tool", query: Query{Tool: "other"}},
		{name: "status", query: Query{Status: "ok"}},
		{name: "source", query: Query{Source: "local"}},
		{name: "glob-miss", query: Query{EventGlob: "server.*"}},
		{name: "glob-invalid", query: Query{EventGlob: "["}},
		{name: "grep", query: Query{Grep: "not present"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.query.Match(event) {
				t.Fatalf("query unexpectedly matched: %#v", test.query)
			}
		})
	}
}

func TestLevelRankAndContainsFold(t *testing.T) {
	for level, want := range map[string]int{"debug": 0, "info": 1, "warning": 2, "warn": 2, "error": 3, "unknown": 1} {
		if got := levelRank(level); got != want {
			t.Fatalf("levelRank(%q)=%d want %d", level, got, want)
		}
	}
	if !containsFold([]string{" SERVER ", "tool"}, "server") || containsFold([]string{"server"}, "tool") {
		t.Fatal("containsFold mismatch")
	}
}
