package install

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func TestRollbackResultRestoresCurrentAndMetadata(t *testing.T) {
	layout := testLayout(t)
	first, err := Install(Options{Layout: layout, Version: "v1.0.0", Source: testBinary(t, "old"), NoAlias: true})
	if err != nil {
		t.Fatal(err)
	}
	if first.PreviousMetadata != nil {
		t.Fatalf("first install previous metadata = %+v", first.PreviousMetadata)
	}
	second, err := Install(Options{Layout: layout, Version: "v1.1.0", Source: testBinary(t, "new"), NoAlias: true})
	if err != nil {
		t.Fatal(err)
	}
	if second.PreviousMetadata == nil || second.PreviousMetadata.Version != "v1.0.0" {
		t.Fatalf("previous metadata = %+v", second.PreviousMetadata)
	}
	if err := RollbackResult(second); err != nil {
		t.Fatal(err)
	}
	version, _, err := CurrentVersion(layout)
	if err != nil {
		t.Fatal(err)
	}
	if version != "v1.0.0" {
		t.Fatalf("current version = %q", version)
	}
	metadata, err := ReadMetadata(layout.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Version != "v1.0.0" {
		t.Fatalf("metadata version = %q", metadata.Version)
	}
}

func TestFinalizeResultKeepsCurrentAndPrevious(t *testing.T) {
	layout := testLayout(t)
	if _, err := Install(Options{Layout: layout, Version: "v1.0.0", Source: testBinary(t, "oldest"), NoAlias: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(Options{Layout: layout, Version: "v1.1.0", Source: testBinary(t, "old"), NoAlias: true}); err != nil {
		t.Fatal(err)
	}
	result, err := Install(Options{Layout: layout, Version: "v1.2.0", Source: testBinary(t, "new"), NoAlias: true})
	if err != nil {
		t.Fatal(err)
	}
	events := []tracepkg.Event{}
	ctx := tracepkg.WithObserver(context.Background(), func(event tracepkg.Event) { events = append(events, event) })
	if err := FinalizeResultContext(ctx, result); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(layout.Versions, "v1.0.0")); !os.IsNotExist(err) {
		t.Fatalf("old version still exists: %v", err)
	}
	for _, version := range []string{"v1.1.0", "v1.2.0"} {
		if _, err := os.Stat(filepath.Join(layout.Versions, version)); err != nil {
			t.Fatalf("kept version %s: %v", version, err)
		}
	}
	if !finalizeTraceFields(events, "install.versions.cleanup.completed", map[string]any{"before_count": 3, "after_count": 2, "removed_count": 1}) {
		t.Fatalf("missing old-version cleanup trace: %#v", events)
	}
}

func finalizeTraceFields(events []tracepkg.Event, name string, expected map[string]any) bool {
	for _, event := range events {
		if event.Name != name {
			continue
		}
		values := map[string]any{}
		for _, field := range event.Fields {
			values[field.Key] = field.Value
		}
		matched := true
		for key, want := range expected {
			if values[key] != want {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
