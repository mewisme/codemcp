package instructioncontext

import (
	"encoding/json"
	"testing"
	"time"
)

func TestInstructionContextJSONContract(t *testing.T) {
	loadedAt := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	value := InstructionContext{
		Root: "/project", WorkspaceID: "ws_test", WorkspaceRoots: []string{"/project"},
		Environment:   EnvironmentSnapshot{Platform: "linux", OS: "linux", Arch: "amd64", Go: "go1.27", PID: 42, WorkspaceID: "ws_test", WorkspaceRoot: "/project", CWD: "/project", EffectiveRoots: []string{"/project"}},
		Git:           GitSnapshot{IsRepo: true, Root: "/project", Branch: "main", RecentCommits: []string{"abc123 test"}},
		ProjectMemory: ProjectMemoryBundle{Root: "/project", WorkspaceRoots: []string{"/project"}, Sections: []Section{{Path: "/project/AGENTS.md", Kind: SectionProject, Source: "agents", Content: "Use pnpm", LoadedBytes: 8}}, TotalBytes: 8, LoadedAt: loadedAt},
		AutoMemory:    AutoMemorySnapshot{Loaded: true, Content: "remember", Bytes: 8},
		ToolProfile:   ToolProfile{Name: "full", Count: 10}, InstructionsText: "instructions", InstructionBytes: 12, LoadedAt: loadedAt,
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"root", "workspace_id", "workspace_roots", "environment", "git", "project_memory", "auto_memory", "rules", "skills", "sources", "tool_profile", "instructions_text", "instruction_bytes", "loaded_at"} {
		if _, ok := object[key]; !ok {
			t.Fatalf("missing JSON field %q: %s", key, data)
		}
	}
}

func TestSectionKinds(t *testing.T) {
	values := []SectionKind{SectionUser, SectionProject, SectionRule, SectionImport}
	want := []string{"user", "project", "rule", "import"}
	for i, value := range values {
		if string(value) != want[i] {
			t.Fatalf("kind %d = %q, want %q", i, value, want[i])
		}
	}
}
