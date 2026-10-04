package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/configformat"
	gitpkg "go.mewis.me/codemcp/internal/git"
	"go.mewis.me/codemcp/internal/skills"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestSkillsCommandGrammarAliasesScopeConflictAndCompletion(t *testing.T) {
	configRoot, workspaceRoot := newSkillsCLIFixture(t)
	writeCLISkill(t, filepath.Join(workspaceRoot, ".cm", "skills", "workspace-alpha"), "workspace-alpha", "Workspace alpha")

	root := newRootCommand()
	for _, path := range [][]string{{"skills", "list"}, {"skills", "add"}, {"skills", "info"}, {"skills", "update"}, {"skills", "remove"}} {
		command, remaining, err := root.Find(path)
		if err != nil || command == nil || len(remaining) != 0 || !command.Runnable() {
			t.Fatalf("command %v: command=%v remaining=%v err=%v", path, command, remaining, err)
		}
	}
	alias, remaining, err := root.Find([]string{"skills", "ls"})
	if err != nil || alias == nil || alias.Name() != "list" || len(remaining) != 0 {
		t.Fatalf("skills ls: command=%v remaining=%v err=%v", alias, remaining, err)
	}
	list, _, _ := root.Find([]string{"skills", "list"})
	if list.Flags().ShorthandLookup("w") == nil || list.Flags().ShorthandLookup("g") == nil {
		t.Fatal("skills scope shorthand flags are missing")
	}
	add, _, _ := root.Find([]string{"skills", "add"})
	if add.Flags().Lookup("skill") == nil || add.Flags().Lookup("all") == nil || add.Flags().Lookup("full-depth") == nil {
		t.Fatal("skills add selection flags are missing")
	}
	if add.Flags().ShorthandLookup("y") == nil || add.Flags().ShorthandLookup("y").Name != "yes" {
		t.Fatal("skills add -y/--yes flag is missing")
	}
	update, _, _ := root.Find([]string{"skills", "update"})
	if update.Flags().Lookup("all") == nil {
		t.Fatal("skills update --all flag is missing")
	}
	if update.Flags().ShorthandLookup("y") == nil || update.Flags().ShorthandLookup("y").Name != "yes" {
		t.Fatal("skills update -y/--yes flag is missing")
	}
	removeAlias, remaining, err := root.Find([]string{"skills", "rm"})
	if err != nil || removeAlias == nil || removeAlias.Name() != "remove" || len(remaining) != 0 {
		t.Fatalf("skills rm: command=%v remaining=%v err=%v", removeAlias, remaining, err)
	}

	var help bytes.Buffer
	helpRoot := newRootCommand()
	helpRoot.SetOut(&help)
	helpRoot.SetErr(&help)
	helpRoot.SetArgs([]string{"--help"})
	if err := helpRoot.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(help.String(), "skills") {
		t.Fatalf("root help does not expose skills:\n%s", help.String())
	}

	conflict := newRootCommand()
	conflict.SilenceErrors = true
	conflict.SilenceUsage = true
	conflict.SetArgs([]string{"--config-dir", configRoot, "skills", "list", "-w", "-g"})
	if err := conflict.Execute(); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("scope conflict err=%v", err)
	}

	info, _, err := root.Find([]string{"skills", "info"})
	if err != nil {
		t.Fatal(err)
	}
	values, directive := completeSkillName(info, nil, "workspace-")
	if directive != cobra.ShellCompDirectiveNoFileComp || !containsString(values, "workspace-alpha") {
		t.Fatalf("skill completion values=%v directive=%v", values, directive)
	}
}

func TestSkillsListEffectiveAndScopedViewsAreDeterministic(t *testing.T) {
	configRoot, workspaceRoot := newSkillsCLIFixture(t)
	writeCLISkill(t, filepath.Join(workspaceRoot, ".cm", "skills", "shadowed"), "shadowed", "Workspace wins")
	writeCLISkill(t, filepath.Join(workspaceRoot, ".cm", "skills", "workspace-only"), "workspace-only", "Workspace only")
	writeCLISkill(t, filepath.Join(configRoot, "skills", "shadowed"), "shadowed", "Global loses")
	writeCLISkill(t, filepath.Join(configRoot, "skills", "global-only"), "global-only", "Global only")
	writeCLISkill(t, filepath.Join(workspaceRoot, ".agents", "skills", "provider-only"), "provider-only", "Provider only")

	effectiveText := executeSkillsCLI(t, configRoot, "skills", "list", "--json")
	var effective application.SkillListResult
	if err := json.Unmarshal([]byte(effectiveText), &effective); err != nil {
		t.Fatalf("decode effective JSON: %v\n%s", err, effectiveText)
	}
	if !effective.Effective {
		t.Fatal("default skills list is not marked effective")
	}
	shadowed := skillViewByName(t, effective.Skills, "shadowed")
	if shadowed.Scope != application.SkillScopeWorkspace || shadowed.Source != "native" || shadowed.Description != "Workspace wins" {
		t.Fatalf("shadowed effective entry=%#v", shadowed)
	}
	provider := skillViewByName(t, effective.Skills, "provider-only")
	if !provider.ReadOnly || provider.Scope != application.SkillManagementScope("provider") || provider.Source == "native" {
		t.Fatalf("provider entry=%#v", provider)
	}
	builtinCount := 0
	for _, value := range effective.Skills {
		if value.Source == "builtin" {
			builtinCount++
			if !value.ReadOnly {
				t.Fatalf("builtin is writable: %#v", value)
			}
		}
	}
	if builtinCount == 0 {
		t.Fatal("effective inventory lost built-in skills")
	}

	workspaceText := executeSkillsCLI(t, configRoot, "skills", "ls", "-w", "--json")
	var workspaceList application.SkillListResult
	if err := json.Unmarshal([]byte(workspaceText), &workspaceList); err != nil {
		t.Fatal(err)
	}
	if workspaceList.Effective || !equalStrings(skillViewNames(workspaceList.Skills), []string{"shadowed", "workspace-only"}) {
		t.Fatalf("workspace list=%#v", workspaceList)
	}

	globalText := executeSkillsCLI(t, configRoot, "skills", "list", "-g", "--json")
	var globalList application.SkillListResult
	if err := json.Unmarshal([]byte(globalText), &globalList); err != nil {
		t.Fatal(err)
	}
	if globalList.Effective || !equalStrings(skillViewNames(globalList.Skills), []string{"global-only", "shadowed"}) {
		t.Fatalf("global list=%#v", globalList)
	}

	human1 := executeSkillsCLI(t, configRoot, "skills", "list", "-w")
	human2 := executeSkillsCLI(t, configRoot, "skills", "list", "-w")
	if human1 != human2 || !strings.Contains(human1, "workspace-only") || !strings.Contains(human1, "workspace:native") {
		t.Fatalf("human output is not deterministic/useful:\nfirst=%s\nsecond=%s", human1, human2)
	}
}

func TestSkillsAddDefaultsWorkspaceAndInfoShowsManagedSource(t *testing.T) {
	configRoot, workspaceRoot := newSkillsCLIFixture(t)
	repository, revision := createCLISkillGitRepository(t, "managed-cli")
	configureCLIGitHubRewrite(t, repository, "owner", "repo")

	output := executeSkillsCLI(t, configRoot, "skills", "add", "owner/repo", "--json")
	var added application.SkillAddResult
	if err := json.Unmarshal([]byte(output), &added); err != nil {
		t.Fatalf("decode add result: %v\n%s", err, output)
	}
	if added.Scope != application.SkillScopeWorkspace || added.Revision != revision || len(added.Skills) != 1 || added.Skills[0].Name != "managed-cli" {
		t.Fatalf("add result=%#v", added)
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, ".cm", "skills", "managed-cli", "SKILL.md")); err != nil {
		t.Fatalf("workspace install missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configRoot, "skills", "managed-cli")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("default add unexpectedly targeted global store: %v", err)
	}

	infoText := executeSkillsCLI(t, configRoot, "skills", "info", "managed-cli", "--json")
	var info application.SkillView
	if err := json.Unmarshal([]byte(infoText), &info); err != nil {
		t.Fatal(err)
	}
	if !info.Managed || info.ReadOnly || info.Scope != application.SkillScopeWorkspace || info.GitHub == nil {
		t.Fatalf("managed info=%#v", info)
	}
	if info.GitHub.Source != "github:owner/repo" || info.GitHub.Revision != revision {
		t.Fatalf("managed source=%#v", info.GitHub)
	}

	globalOutput := executeSkillsCLI(t, configRoot, "skills", "add", "-g", "owner/repo", "--json")
	var globalAdded application.SkillAddResult
	if err := json.Unmarshal([]byte(globalOutput), &globalAdded); err != nil {
		t.Fatal(err)
	}
	if globalAdded.Scope != application.SkillScopeGlobal {
		t.Fatalf("global add result=%#v", globalAdded)
	}
	if _, err := os.Stat(filepath.Join(configRoot, "skills", "managed-cli", "SKILL.md")); err != nil {
		t.Fatalf("--config-dir global install missing: %v", err)
	}
}

func TestSkillsAddHumanProgressStartsBeforeRepositoryMutation(t *testing.T) {
	configRoot, workspaceRoot := newSkillsCLIFixture(t)
	repository, _ := createCLISkillGitRepository(t, "streamed-skill")
	configureCLIGitHubRewrite(t, repository, "owner", "repo")

	gate := newProgressGateWriter("Cloning GitHub repository")
	cmd := newRootCommand()
	cmd.SetOut(presentation.WrapWriter(gate, presentation.Capabilities{
		Width: 120, Unicode: true, Interactive: true, CursorControl: true, Animation: true,
	}))
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--config-dir", configRoot, "skills", "add", "owner/repo"})

	done := make(chan error, 1)
	go func() { done <- executeCommand(cmd) }()

	select {
	case <-gate.reached:
	case <-time.After(5 * time.Second):
		close(gate.release)
		t.Fatal("human progress did not render before skill add")
	}
	installed := filepath.Join(workspaceRoot, ".cm", "skills", "streamed-skill")
	if _, err := os.Stat(installed); !errors.Is(err, os.ErrNotExist) {
		close(gate.release)
		t.Fatalf("skill mutation started before progress became visible: %v", err)
	}
	close(gate.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(installed, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	text := gate.String()
	if !strings.Contains(text, "Repository cloned") ||
		!strings.Contains(text, "Found 1 skill") ||
		!strings.Contains(text, "Skill: streamed-skill") ||
		!strings.Contains(text, "GitHub skills installed") ||
		!strings.Contains(text, "Skills installed") {
		t.Fatalf("human add progress/result missing: %q", text)
	}
}

func TestSkillsAddRejectsSelectionAndScopeConflictsBeforeNetwork(t *testing.T) {
	configRoot, _ := newSkillsCLIFixture(t)
	for _, args := range [][]string{
		{"skills", "add", "-w", "-g", "owner/repo"},
		{"skills", "add", "--skill", "one", "--all", "owner/repo"},
	} {
		cmd := newRootCommand()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		cmd.SetArgs(append([]string{"--config-dir", configRoot}, args...))
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Fatalf("args=%v err=%v", args, err)
		}
	}
}

func TestSkillsCLIUpdateAndRemoveManagedSkill(t *testing.T) {
	configRoot, workspaceRoot := newSkillsCLIFixture(t)
	repository, _ := createCLISkillGitRepository(t, "managed-cli")
	configureCLIGitHubRewrite(t, repository, "owner", "repo")

	executeSkillsCLI(t, configRoot, "skills", "add", "owner/repo", "--json")
	if err := os.WriteFile(filepath.Join(repository, "managed-cli", "updated.txt"), []byte("updated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "--quiet", "-m", "update fixture"}} {
		if _, err := gitpkg.OrThrow(t.Context(), repository, args...); err != nil {
			t.Fatal(err)
		}
	}

	updateText := executeSkillsCLI(t, configRoot, "skills", "update", "managed-cli", "--json")
	var updated application.SkillUpdateResult
	if err := json.Unmarshal([]byte(updateText), &updated); err != nil {
		t.Fatalf("decode update result: %v\n%s", err, updateText)
	}
	if len(updated.Skills) != 1 || updated.Skills[0].Name != "managed-cli" || !updated.Skills[0].Changed {
		t.Fatalf("update result=%#v", updated)
	}
	installed := filepath.Join(workspaceRoot, ".cm", "skills", "managed-cli")
	if data, err := os.ReadFile(filepath.Join(installed, "updated.txt")); err != nil || string(data) != "updated\n" {
		t.Fatalf("updated file err=%v data=%q", err, data)
	}

	removeText := executeSkillsCLI(t, configRoot, "skills", "rm", "managed-cli", "--json")
	var removed application.SkillRemoveResult
	if err := json.Unmarshal([]byte(removeText), &removed); err != nil {
		t.Fatalf("decode remove result: %v\n%s", err, removeText)
	}
	if removed.Name != "managed-cli" || !removed.Managed || removed.Scope != application.SkillScopeWorkspace {
		t.Fatalf("remove result=%#v", removed)
	}
	if _, err := os.Stat(installed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("managed skill still exists after remove: %v", err)
	}
}

func TestSkillsUpdateAndRemoveHumanProgress(t *testing.T) {
	configRoot, _ := newSkillsCLIFixture(t)
	repository, _ := createCLISkillGitRepository(t, "human-progress")
	configureCLIGitHubRewrite(t, repository, "owner", "repo")
	executeSkillsCLI(t, configRoot, "skills", "add", "owner/repo", "--json")

	if err := os.WriteFile(filepath.Join(repository, "human-progress", "updated.txt"), []byte("updated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "--quiet", "-m", "update human progress fixture"}} {
		if _, err := gitpkg.OrThrow(t.Context(), repository, args...); err != nil {
			t.Fatal(err)
		}
	}

	updateText := executeSkillsCLI(t, configRoot, "skills", "update", "human-progress")
	if !strings.Contains(updateText, "Managed GitHub skill sources acquired") || !strings.Contains(updateText, "Managed GitHub skill updates installed") || !strings.Contains(updateText, "Skills updated") {
		t.Fatalf("human update progress/result missing: %q", updateText)
	}
	removeText := executeSkillsCLI(t, configRoot, "skills", "rm", "human-progress")
	if !strings.Contains(removeText, "Native skill removed") || !strings.Contains(removeText, "Skill removed") {
		t.Fatalf("human remove progress/result missing: %q", removeText)
	}
}

func TestSkillRiskReviewerRendersVercelStyleAssessmentAndOnlyPromptsWhenRisky(t *testing.T) {
	alerts := 2
	assessment := skills.SecurityAssessment{
		Source: "owner/repo", DetailsURL: "https://skills.sh/owner/repo",
		Skills: []skills.SkillSecurityAssessment{{
			Name:   "risk-demo",
			Gen:    &skills.PartnerAudit{Risk: skills.SecurityRiskHigh},
			Socket: &skills.PartnerAudit{Risk: skills.SecurityRiskUnknown, Alerts: &alerts},
			Snyk:   &skills.PartnerAudit{Risk: skills.SecurityRiskSafe},
		}},
	}

	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(presentation.WrapWriter(&output, presentation.Capabilities{
		Width: 120, Unicode: true, Interactive: true, CursorControl: true, Animation: true,
	}))
	root.SetErr(&bytes.Buffer{})
	root.SetIn(strings.NewReader("yes\n"))
	cmd, _, err := root.Find([]string{"skills", "add"})
	if err != nil {
		t.Fatal(err)
	}
	if err := skillRiskReviewer(cmd, false, "installation")(assessment); err != nil {
		t.Fatal(err)
	}
	closeCommandProgress(cmd, nil)
	text := output.String()
	for _, want := range []string{
		"Security Risk Assessments", "Gen", "Socket", "Snyk",
		"risk-demo", "High Risk", "2 alerts", "Safe",
		"Details:", "https://skills.sh/owner/repo",
		"Security risks detected. Proceed with installation? [Y/n]",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("risk assessment missing %q: %q", want, text)
		}
	}

	root = newRootCommand()
	output.Reset()
	root.SetOut(presentation.WrapWriter(&output, presentation.Capabilities{
		Width: 120, Unicode: true, Interactive: true, CursorControl: true, Animation: true,
	}))
	root.SetErr(&bytes.Buffer{})
	root.SetIn(strings.NewReader("no\n"))
	cmd, _, err = root.Find([]string{"skills", "add"})
	if err != nil {
		t.Fatal(err)
	}
	safeAssessment := assessment
	safeAssessment.Skills = []skills.SkillSecurityAssessment{{
		Name: "safe-demo", Gen: &skills.PartnerAudit{Risk: skills.SecurityRiskLow},
		Socket: &skills.PartnerAudit{Alerts: new(int)}, Snyk: &skills.PartnerAudit{Risk: skills.SecurityRiskSafe},
	}}
	if err := skillRiskReviewer(cmd, false, "installation")(safeAssessment); err != nil {
		t.Fatal(err)
	}
	closeCommandProgress(cmd, nil)
	if text := output.String(); strings.Contains(text, "Proceed with") || !strings.Contains(text, "Low Risk") || !strings.Contains(text, "0 alerts") {
		t.Fatalf("safe assessment should render without prompting: %q", text)
	}
}

func TestSkillRiskReviewerHonorsYesAndRejectsNonInteractiveRiskWithoutIt(t *testing.T) {
	assessment := skills.SecurityAssessment{
		Source: "owner/repo", DetailsURL: "https://skills.sh/owner/repo",
		Skills: []skills.SkillSecurityAssessment{{
			Name: "risk-demo", Gen: &skills.PartnerAudit{Risk: skills.SecurityRiskCritical},
		}},
	}

	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(presentation.WrapWriter(&output, presentation.Capabilities{
		Width: 120, Unicode: true, Interactive: true, CursorControl: true, Animation: true,
	}))
	root.SetErr(&bytes.Buffer{})
	cmd, _, err := root.Find([]string{"skills", "add"})
	if err != nil {
		t.Fatal(err)
	}
	if err := skillRiskReviewer(cmd, true, "installation")(assessment); err != nil {
		t.Fatal(err)
	}
	closeCommandProgress(cmd, nil)
	if text := output.String(); !strings.Contains(text, "Critical Risk") || strings.Contains(text, "Proceed with") {
		t.Fatalf("--yes risk rendering=%q", text)
	}

	root = newRootCommand()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	cmd, _, err = root.Find([]string{"skills", "add"})
	if err != nil {
		t.Fatal(err)
	}
	if err := skillRiskReviewer(cmd, false, "installation")(assessment); !errors.Is(err, errSkillRiskConfirmationRequired) {
		t.Fatalf("non-interactive risk err=%v", err)
	}
}

func TestSkillRiskReviewerDeclineCancels(t *testing.T) {
	root := newRootCommand()
	root.SetOut(presentation.WrapWriter(&bytes.Buffer{}, presentation.Capabilities{
		Width: 120, Unicode: true, Interactive: true, CursorControl: true, Animation: true,
	}))
	root.SetErr(&bytes.Buffer{})
	root.SetIn(strings.NewReader("n\n"))
	cmd, _, err := root.Find([]string{"skills", "update"})
	if err != nil {
		t.Fatal(err)
	}
	assessment := skills.SecurityAssessment{
		Source: "owner/repo", DetailsURL: "https://skills.sh/owner/repo",
		Skills: []skills.SkillSecurityAssessment{{
			Name: "risk-demo", Snyk: &skills.PartnerAudit{Risk: skills.SecurityRiskMedium},
		}},
	}
	if err := skillRiskReviewer(cmd, false, "update")(assessment); !errors.Is(err, errSkillRiskCancelled) {
		t.Fatalf("decline err=%v", err)
	}
	closeCommandProgress(cmd, nil)
}

func TestSkillRiskReviewerEnterAcceptsRiskByDefault(t *testing.T) {
	root := newRootCommand()
	root.SetOut(presentation.WrapWriter(&bytes.Buffer{}, presentation.Capabilities{
		Width: 120, Unicode: true, Interactive: true, CursorControl: true, Animation: true,
	}))
	root.SetErr(&bytes.Buffer{})
	root.SetIn(strings.NewReader("\n"))
	cmd, _, err := root.Find([]string{"skills", "add"})
	if err != nil {
		t.Fatal(err)
	}
	assessment := skills.SecurityAssessment{
		Source: "owner/repo", DetailsURL: "https://skills.sh/owner/repo",
		Skills: []skills.SkillSecurityAssessment{{
			Name: "risk-demo", Gen: &skills.PartnerAudit{Risk: skills.SecurityRiskMedium},
		}},
	}
	if err := skillRiskReviewer(cmd, false, "installation")(assessment); err != nil {
		t.Fatalf("default Enter should accept risk review: %v", err)
	}
	closeCommandProgress(cmd, nil)
}

func TestSkillMutationHumanFlowOrdersAcquireAuditAskThenInstall(t *testing.T) {
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(presentation.WrapWriter(&output, presentation.Capabilities{
		Width: 120, Unicode: true, Interactive: true, CursorControl: true, Animation: true,
	}))
	root.SetErr(&bytes.Buffer{})
	root.SetIn(strings.NewReader("\n"))
	cmd, _, err := root.Find([]string{"skills", "add"})
	if err != nil {
		t.Fatal(err)
	}
	progress := newCommandProgress(cmd, "SKILLS")
	progress.Start("skills.add.cloning", "Cloning GitHub repository", "Repository cloned")
	observe := skillMutationProgressObserver(progress, "add")
	observe(application.SkillMutationEvent{Phase: application.SkillMutationPhaseRepositoryAcquired})
	observe(application.SkillMutationEvent{Phase: application.SkillMutationPhaseDiscovered, Count: 1})
	observe(application.SkillMutationEvent{
		Phase: application.SkillMutationPhaseSelected,
		Skills: []application.SkillMutationSelection{{
			Name: "risk-demo", Description: "Risk demo skill",
		}},
	})
	assessment := skills.SecurityAssessment{
		Source: "owner/repo", DetailsURL: "https://skills.sh/owner/repo",
		Skills: []skills.SkillSecurityAssessment{{
			Name: "risk-demo", Gen: &skills.PartnerAudit{Risk: skills.SecurityRiskMedium},
		}},
	}
	if err := skillRiskReviewer(cmd, false, "installation")(assessment); err != nil {
		t.Fatal(err)
	}
	observe(application.SkillMutationEvent{Phase: application.SkillMutationPhaseInstalling})
	progress.Complete()
	closeCommandProgress(cmd, nil)

	text := output.String()
	parts := []string{
		"Repository cloned",
		"Discovering skills",
		"Found 1 skill",
		"Skill: risk-demo",
		"Security Risk Assessments",
		"Security risks detected. Proceed with installation? [Y/n]",
		"Installing GitHub skills",
		"GitHub skills installed",
	}
	last := -1
	for _, part := range parts {
		index := strings.Index(text, part)
		if index < 0 || index <= last {
			t.Fatalf("human skill flow out of order at %q: %q", part, text)
		}
		last = index
	}
}

type progressGateWriter struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	needle  string
	reached chan struct{}
	release chan struct{}
	once    sync.Once
}

func newProgressGateWriter(needle string) *progressGateWriter {
	return &progressGateWriter{
		needle: needle, reached: make(chan struct{}), release: make(chan struct{}),
	}
}

func (writer *progressGateWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	_, _ = writer.buffer.Write(data)
	match := strings.Contains(string(data), writer.needle)
	writer.mu.Unlock()
	if match {
		writer.once.Do(func() {
			close(writer.reached)
			<-writer.release
		})
	}
	return len(data), nil
}

func (writer *progressGateWriter) String() string {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.buffer.String()
}

func newSkillsCLIFixture(t *testing.T) (string, string) {
	t.Helper()
	t.Setenv("DO_NOT_TRACK", "1")
	previousRoot := configformat.RootPath()
	configRoot := filepath.Join(t.TempDir(), "config")
	t.Setenv(configformat.EnvConfigDir, configRoot)
	if err := configformat.SetRootPath(configRoot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = configformat.SetRootPath(previousRoot) })

	workspaceRoot := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	manager := workspace.NewManager(workspace.DefaultStorePath())
	if _, err := manager.Register(workspaceRoot); err != nil {
		t.Fatal(err)
	}
	previousCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workspaceRoot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousCWD) })
	return configRoot, workspaceRoot
}

func executeSkillsCLI(t *testing.T, configRoot string, args ...string) string {
	t.Helper()
	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(presentation.WrapWriter(&stdout, presentation.Capabilities{Width: 120, Unicode: true, Interactive: true}))
	cmd.SetErr(&stderr)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs(append([]string{"--config-dir", configRoot}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("cm %s: %v\nstderr=%s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String()
}

func writeCLISkill(t *testing.T, root, name, description string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\nDo the work.\n"
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func skillViewByName(t *testing.T, values []application.SkillView, name string) application.SkillView {
	t.Helper()
	for _, value := range values {
		if value.Name == name {
			return value
		}
	}
	t.Fatalf("skill %q not found in %#v", name, values)
	return application.SkillView{}
}

func skillViewNames(values []application.SkillView) []string {
	names := make([]string, 0, len(values))
	for _, value := range values {
		names = append(names, value.Name)
	}
	sort.Strings(names)
	return names
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func createCLISkillGitRepository(t *testing.T, skillName string) (string, string) {
	t.Helper()
	repository := t.TempDir()
	writeCLISkill(t, filepath.Join(repository, skillName), skillName, "Managed CLI skill")
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "CodeMCP Test"},
		{"add", "."},
		{"commit", "--quiet", "-m", "fixture"},
	} {
		if _, err := gitpkg.OrThrow(t.Context(), repository, args...); err != nil {
			t.Fatal(err)
		}
	}
	result, err := gitpkg.OrThrow(t.Context(), repository, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return repository, strings.TrimSpace(result.Stdout)
}

func configureCLIGitHubRewrite(t *testing.T, repository, owner, name string) {
	t.Helper()
	fileURL := "file://" + filepath.ToSlash(repository)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+fileURL+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/"+owner+"/"+name+".git")
	t.Setenv("GIT_ALLOW_PROTOCOL", "file")
}
