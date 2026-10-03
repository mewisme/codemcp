package instructioncontext

import (
	"os"
	"path/filepath"
	"testing"

	"go.mewis.me/codemcp/internal/instructionpolicy"
)

func TestDynamicProviderContractIsSharedAcrossProjectContextRulesAndSkills(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	workspaceRoot := t.TempDir()
	projectRoot := filepath.Join(workspaceRoot, "packages", "app")
	home := t.TempDir()
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	writeInstructionFile(t, filepath.Join(projectRoot, "AGENTS.md"), "same\r\ncontext\n")
	writeInstructionFile(t, filepath.Join(projectRoot, ".agents", "AGENTS.md"), " same\ncontext ")
	writeInstructionFile(t, filepath.Join(projectRoot, ".newagent", "AGENTS.md"), "future context")
	writeInstructionFile(t, filepath.Join(projectRoot, ".zedagent", "CLAUDE.md"), "zed context")

	writeRuleFile(t, projectRoot, ".agents", "agents.md", "agents rule")
	writeRuleFile(t, projectRoot, ".newagent", "future.md", "future rule")
	writeRuleFile(t, projectRoot, ".zedagent", "zed.md", "zed rule")

	writeSkillFile(t, projectRoot, ".agents", "agents", "agents", "agents skill", "agents body")
	writeSkillFile(t, projectRoot, ".newagent", "future", "future", "future skill", "future body")
	writeSkillFile(t, projectRoot, ".zedagent", "zed", "zed", "zed skill", "zed body")

	writeInstructionFile(t, filepath.Join(home, ".homeagent", "AGENTS.md"), "home context")
	writeRuleFile(t, home, ".homeagent", "home.md", "home rule")
	writeSkillFile(t, home, ".homeagent", "home", "home", "home skill", "home body")

	disabled := false
	policy := instructionpolicy.DefaultConfig()
	policy.Sources["newagent"] = instructionpolicy.SourcePolicy{Rules: &disabled}

	bundle, err := LoadProjectMemory(projectRoot, MemoryLoadOptions{
		WorkspaceRoots: []string{workspaceRoot},
		HomeDir:        home,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Sections) != 3 {
		t.Fatalf("sections=%#v", bundle.Sections)
	}
	wantContextSources := []string{"agents", ".newagent", ".zedagent"}
	for i, source := range wantContextSources {
		if bundle.Sections[i].Source != source {
			t.Fatalf("context section %d=%#v want source=%q", i, bundle.Sections[i], source)
		}
	}
	for _, section := range bundle.Sections {
		if section.Source == ".agents" || section.Source == ".homeagent" {
			t.Fatalf("deduplicated or home context loaded: %#v", bundle.Sections)
		}
	}

	loadedRules, err := LoadUnconditionalRulesWithUserForWorkspace(projectRoot, workspaceRoot, home, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(loadedRules) != 3 || loadedRules[0].Source != ".agents" || loadedRules[1].Source != ".newagent" || loadedRules[2].Source != ".zedagent" {
		t.Fatalf("rules=%#v", loadedRules)
	}

	loadedSkills, err := LoadSkillSummariesWithUserForWorkspace(projectRoot, workspaceRoot, home, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(loadedSkills) != 7 {
		t.Fatalf("skills=%#v", loadedSkills)
	}
	wantSkillSources := []string{".agents", ".newagent", ".zedagent", ".homeagent", "codemcp", "codemcp", "codemcp"}
	for i, source := range wantSkillSources {
		if loadedSkills[i].Source != source {
			t.Fatalf("skill %d=%#v want source=%q", i, loadedSkills[i], source)
		}
	}

	sources := LoadedProjectSources(bundle, loadedRules, loadedSkills, workspaceRoot)
	providerOrder := make([]string, 0)
	seen := map[string]bool{}
	newAgentRuleLoaded := false
	for _, source := range sources {
		if !seen[source.Provider] {
			seen[source.Provider] = true
			providerOrder = append(providerOrder, source.Provider)
		}
		if source.Provider == ".newagent" && source.Kind == string(instructionpolicy.ResourceRules) {
			newAgentRuleLoaded = source.Loaded
		}
	}
	if !newAgentRuleLoaded {
		t.Fatalf("legacy source policy still filtered provider-native rules: %#v", sources)
	}
	wantProviderOrder := []string{"agents", ".agents", ".homeagent", ".newagent", ".zedagent", "codemcp"}
	if len(providerOrder) != len(wantProviderOrder) {
		t.Fatalf("provider order=%#v sources=%#v", providerOrder, sources)
	}
	for i, provider := range wantProviderOrder {
		if providerOrder[i] != provider {
			t.Fatalf("provider order=%#v want=%#v", providerOrder, wantProviderOrder)
		}
	}

	homeSources, err := DiscoverUserSources(home, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(homeSources) != 3 {
		t.Fatalf("home sources=%#v", homeSources)
	}
	for _, source := range homeSources {
		if source.Provider != "homeagent" || source.Scope != "user-provider" || source.Loaded {
			t.Fatalf("home provider visibility=%#v", source)
		}
	}
}
