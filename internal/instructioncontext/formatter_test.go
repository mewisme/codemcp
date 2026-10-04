package instructioncontext

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"go.mewis.me/codemcp/internal/rules"
	"go.mewis.me/codemcp/internal/skills"
)

func TestFormatInstructionsStableOrderingAndByteCount(t *testing.T) {
	value := InstructionContext{
		ToolProfile: ToolProfile{Name: "full", Count: 54},
		ToolCapabilities: &ToolCapabilities{Groups: []ToolCapabilityGroup{
			{Domain: "filesystem", Tools: []string{"grep", "read_text_file"}},
			{Domain: "git", Tools: []string{"git_log", "git_push"}},
		}, TotalTools: 4, IncludedTools: 4},
		Environment: EnvironmentSnapshot{
			Platform: "linux", OS: "linux", Arch: "amd64", Go: "go1.27.0", PID: 123,
			WorkspaceID: "ws_test", WorkspaceRoot: "/workspace", CWD: "/workspace/sub", EffectiveRoots: []string{"/workspace", "/shared"},
			Admin: AdminSnapshot{Enabled: true, URL: "http://127.0.0.1:37422/"},
		},
		Git:        GitSnapshot{IsRepo: true, Root: "/workspace", Branch: "main", StatusShort: "## main\n M a.go", RecentCommits: []string{"abc first", "def second"}},
		AutoMemory: AutoMemorySnapshot{Loaded: true, Content: "remember pnpm", Bytes: 13},
		ProjectMemory: ProjectMemoryBundle{Sections: []Section{
			{Path: "/home/user/.agents/AGENTS.md", Kind: SectionUser, Source: "agents", Content: "user instruction"},
			{Path: "/workspace/AGENTS.md", Kind: SectionProject, Source: "agents", Content: "project instruction"},
			{Path: "/workspace/CLAUDE.md", Kind: SectionProject, Source: "claude", Content: "claude fallback", Truncated: true},
		}},
		Rules:  []rules.Rule{{Path: "/workspace/.agents/rules/global.md", Source: ".agents", Content: "global rule"}},
		Skills: []skills.Skill{{Name: "release", Description: "Release workflow", Source: ".agents", Path: "/workspace/.agents/skills/release/SKILL.md"}},
	}
	text, size := FormatInstructions(value)
	if size != len([]byte(text)) {
		t.Fatalf("size = %d, bytes = %d", size, len([]byte(text)))
	}
	ordered := []string{
		"## Agent workflow", "## Tool profile", "## Tool capabilities", "## Environment", "## Git", "## Auto memory", "## User instructions", "## Project instructions", "## Always-on rules", "## Skills", "## Quick pointers",
	}
	last := -1
	for _, heading := range ordered {
		index := strings.Index(text, heading)
		if index < 0 || index <= last {
			t.Fatalf("heading %q out of order in:\n%s", heading, text)
		}
		last = index
	}
	for _, expected := range []string{
		"- git: git_log, git_push",
		"### /workspace/AGENTS.md [agents]\nproject instruction",
		"### /workspace/CLAUDE.md [claude] (truncated)\nclaude fallback",
		"### /workspace/.agents/rules/global.md [.agents]\nglobal rule",
		"- release: Release workflow [.agents] (/workspace/.agents/skills/release/SKILL.md)",
		"Load an applicable skill with load_skill",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("formatted instructions missing %q:\n%s", expected, text)
		}
	}
}

func TestFormatInstructionsDoesNotRenderImportMetadataTwice(t *testing.T) {
	marker := "<!-- @import /workspace/import.md -->\nimported body"
	value := InstructionContext{ProjectMemory: ProjectMemoryBundle{
		Sections: []Section{{Path: "/workspace/AGENTS.md", Kind: SectionProject, Source: "agents", Content: "root\n" + marker}},
		Imports:  []Section{{Path: "/workspace/import.md", Kind: SectionImport, Source: "import", Content: "imported body"}},
	}}
	text, _ := FormatInstructions(value)
	if strings.Count(text, "imported body") != 1 {
		t.Fatalf("import rendered more than once:\n%s", text)
	}
}

func TestFormatInstructionsOmitsEmptyOptionalBlocks(t *testing.T) {
	text, _ := FormatInstructions(InstructionContext{})
	for _, heading := range []string{"## Tool capabilities", "## Auto memory", "## User instructions", "## Project instructions", "## Always-on rules", "## Skills"} {
		if strings.Contains(text, heading) {
			t.Fatalf("unexpected empty block %q:\n%s", heading, text)
		}
	}
	for _, heading := range []string{"## Agent workflow", "## Tool profile", "## Environment", "## Git", "## Quick pointers"} {
		if !strings.Contains(text, heading) {
			t.Fatalf("missing required block %q:\n%s", heading, text)
		}
	}
}

func TestFormatToolCapabilitiesBoundsAndMarksTruncation(t *testing.T) {
	groups := make([]ToolCapabilityGroup, maxRenderedCapabilityGroups+2)
	for groupIndex := range groups {
		groups[groupIndex].Domain = fmt.Sprintf("domain-%02d", groupIndex)
		for toolIndex := 0; toolIndex < maxRenderedCapabilityToolsPerGroup+2; toolIndex++ {
			groups[groupIndex].Tools = append(groups[groupIndex].Tools, fmt.Sprintf("tool_%02d_%02d", groupIndex, toolIndex))
		}
	}
	capabilities := &ToolCapabilities{Groups: groups, TotalTools: 200, IncludedTools: 180}
	text := formatToolCapabilities(capabilities)
	if len([]byte(text)) > maxRenderedCapabilityBytes || !strings.Contains(text, "- truncated: true") {
		t.Fatalf("bounded capabilities=%d bytes\n%s", len([]byte(text)), text)
	}
	if strings.Contains(text, "domain-16") || strings.Contains(text, "tool_00_08") {
		t.Fatalf("render limits were not enforced:\n%s", text)
	}
	if !strings.Contains(text, "domain-00: tool_00_00") || !strings.Contains(text, "(truncated)") {
		t.Fatalf("visible group truncation missing:\n%s", text)
	}
}

func TestToolCapabilitiesRemainRequiredUnderInstructionBudget(t *testing.T) {
	value := InstructionContext{
		ToolProfile: ToolProfile{Name: "full", Count: 2},
		ToolCapabilities: &ToolCapabilities{
			Groups:     []ToolCapabilityGroup{{Domain: "git", Tools: []string{"git_log", "git_push"}}},
			TotalTools: 2, IncludedTools: 2,
		},
		ProjectMemory: ProjectMemoryBundle{Sections: []Section{{Path: "/workspace/AGENTS.md", Kind: SectionProject, Content: strings.Repeat("optional ", 5000)}}},
	}
	required := canonicalInstructionBlockSubset(instructionBlocks(value), map[string]bool{
		"Agent workflow": true, "Tool profile": true, "Tool capabilities": true, "Environment": true, "Quick pointers": true,
	})
	_, minimum := renderInstructionBlocks(required)
	if err := ApplyFormattedInstructionsLimit(&value, minimum); err != nil {
		t.Fatal(err)
	}
	if !value.InstructionTruncated || !strings.Contains(value.InstructionsText, "## Tool capabilities") || !strings.Contains(value.InstructionsText, "git_push") {
		t.Fatalf("required capability block was evicted:\n%s", value.InstructionsText)
	}
	if strings.Contains(value.InstructionsText, strings.Repeat("optional ", 100)) {
		t.Fatalf("optional block survived exact minimum budget:\n%s", value.InstructionsText)
	}
}

func TestFormatInstructionsNestsAutoMemoryHeadings(t *testing.T) {
	value := InstructionContext{AutoMemory: AutoMemorySnapshot{Loaded: true, Content: "## general\n\n- use Charm defaults"}}
	text, _ := FormatInstructions(value)
	expected := "## Auto memory\n### general\n\n- use Charm defaults"
	if !strings.Contains(text, expected) {
		t.Fatalf("auto memory hierarchy is not nested:\n%s", text)
	}
	if strings.Contains(text, "## Auto memory\n## general") || strings.Contains(text, "#### general") {
		t.Fatalf("auto memory scope leaked at block level:\n%s", text)
	}
}

func TestFormatInstructionsDetachedAndNonRepoGit(t *testing.T) {
	detached := formatGit(GitSnapshot{IsRepo: true, Root: "/workspace"})
	if !strings.Contains(detached, "- branch: (detached)") {
		t.Fatalf("detached git = %q", detached)
	}
	nonRepo := formatGit(GitSnapshot{Error: "git unavailable"})
	if !strings.Contains(nonRepo, "- repository: false") || !strings.Contains(nonRepo, "- error: git unavailable") {
		t.Fatalf("non repo git = %q", nonRepo)
	}
}

func TestApplyFormattedInstructionsDefaultsWorkflow(t *testing.T) {
	value := InstructionContext{ToolProfile: ToolProfile{Name: "full", Count: 1}}
	ApplyFormattedInstructions(&value)
	if value.AgentWorkflow != AgentWorkflow() || value.InstructionsText == "" || value.InstructionBytes != len([]byte(value.InstructionsText)) || value.InstructionTruncated {
		t.Fatalf("value = %#v", value)
	}
	ApplyFormattedInstructions(nil)
}

func TestFormatInstructionsOmitsSkippedGit(t *testing.T) {
	text, _ := FormatInstructions(InstructionContext{Git: GitSnapshot{Skipped: true}})
	if strings.Contains(text, "## Git") {
		t.Fatalf("skipped git rendered:\n%s", text)
	}
}

func TestApplyFormattedInstructionsLimitUTF8(t *testing.T) {
	value := InstructionContext{ProjectMemory: ProjectMemoryBundle{Sections: []Section{{Path: "/workspace/AGENTS.md", Kind: SectionProject, Content: strings.Repeat("🙂", 2000)}}}}
	if err := ApplyFormattedInstructionsLimit(&value, 7000); err != nil {
		t.Fatal(err)
	}
	if !value.InstructionTruncated || value.InstructionBytes > 7000 || value.InstructionBytes != len([]byte(value.InstructionsText)) || !utf8.ValidString(value.InstructionsText) {
		t.Fatalf("value = %#v", value)
	}
	if strings.Contains(value.InstructionsText, "🙂") {
		t.Fatalf("optional UTF-8 block was partially retained:\n%s", value.InstructionsText)
	}
	if err := ApplyFormattedInstructionsLimit(nil, 257); err != nil {
		t.Fatal(err)
	}
}

func TestFormatInstructionsGolden(t *testing.T) {
	value := InstructionContext{
		AgentWorkflow: "workflow",
		ToolProfile:   ToolProfile{Name: "full", Count: 3},
		ToolCapabilities: &ToolCapabilities{
			Groups:     []ToolCapabilityGroup{{Domain: "git", Tools: []string{"git_log", "git_push"}}},
			TotalTools: 2, IncludedTools: 2,
		},
		Environment: EnvironmentSnapshot{
			Platform: "linux", OS: "linux", Arch: "amd64", Go: "go1.test", PID: 42,
			WorkspaceID: "ws_test", WorkspaceRoot: "/workspace", CWD: "/workspace/sub", EffectiveRoots: []string{"/workspace", "/shared"},
		},
		Git:        GitSnapshot{IsRepo: true, Root: "/workspace", Branch: "main", StatusShort: "## main\n M main.go", RecentCommits: []string{"abc first"}},
		AutoMemory: AutoMemorySnapshot{Loaded: true, Content: "remember this", Bytes: 13},
		ProjectMemory: ProjectMemoryBundle{Sections: []Section{
			{Path: "/home/user/.agents/AGENTS.md", Kind: SectionUser, Source: "agents", Content: "user rules"},
			{Path: "/workspace/AGENTS.md", Kind: SectionProject, Source: "agents", Content: "project rules"},
		}},
		Rules:  []rules.Rule{{Path: "/workspace/.agents/rules/global.md", Source: ".agents", Content: "global rule"}},
		Skills: []skills.Skill{{Name: "release", Description: "Release workflow", Source: ".agents", Path: "/workspace/.agents/skills/release/SKILL.md"}},
	}
	actual, _ := FormatInstructions(value)
	expected, err := os.ReadFile(filepath.Join("testdata", "instructions.golden"))
	if err != nil {
		t.Fatal(err)
	}
	expectedText := strings.ReplaceAll(string(expected), "\r\n", "\n")
	if actual != strings.TrimSuffix(expectedText, "\n") {
		t.Fatalf("formatted instructions differ from golden\n--- actual ---\n%s\n--- expected ---\n%s", actual, expected)
	}
}
