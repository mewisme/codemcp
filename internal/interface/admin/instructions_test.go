package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/instructionpolicy"
	"go.mewis.me/codemcp/internal/memory"
	"go.mewis.me/codemcp/internal/projectcontext"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestGlobalInstructionsAndWorkspaceContextAPI(t *testing.T) {
	configRoot := t.TempDir()
	home := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", configRoot)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "CLAUDE.md"), []byte("USER CLAUDE CONTEXT"), 0644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("PROJECT CONTEXT"), 0644); err != nil {
		t.Fatal(err)
	}
	manager := workspace.NewManager(filepath.Join(configRoot, "workspaces.json"))
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(API{Workspaces: manager})

	putBody := `{"context":"MANAGED GLOBAL CONTEXT","rules":[{"id":"rule_global","name":"Global","enabled":true,"content":"MANAGED GLOBAL RULE"}],"source_policy":{"claude":{"context":false}}}`
	put := httptest.NewRequest(http.MethodPut, "/api/instructions/global", strings.NewReader(putBody))
	putRecorder := httptest.NewRecorder()
	handler.ServeHTTP(putRecorder, put)
	if putRecorder.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", putRecorder.Code, putRecorder.Body.String())
	}
	var settings instructionSettingsResponse
	if err := json.Unmarshal(putRecorder.Body.Bytes(), &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Context != "MANAGED GLOBAL CONTEXT" || len(settings.Rules) != 1 || len(settings.DetectedSources) != 1 {
		t.Fatalf("settings = %#v", settings)
	}
	source := settings.DetectedSources[0]
	if source.Provider != "claude" || source.Kind != string(instructionpolicy.ResourceContext) || source.Enabled {
		t.Fatalf("source = %#v", source)
	}

	preview := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+item.ID+"/context?include_git=false", nil)
	previewRecorder := httptest.NewRecorder()
	handler.ServeHTTP(previewRecorder, preview)
	if previewRecorder.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", previewRecorder.Code, previewRecorder.Body.String())
	}
	var result projectcontext.Result
	if err := json.Unmarshal(previewRecorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"MANAGED GLOBAL CONTEXT", "MANAGED GLOBAL RULE", "PROJECT CONTEXT"} {
		if !strings.Contains(result.InstructionContext.InstructionsText, expected) {
			t.Fatalf("missing %q: %s", expected, result.InstructionContext.InstructionsText)
		}
	}
	if strings.Contains(result.InstructionContext.InstructionsText, "USER CLAUDE CONTEXT") {
		t.Fatalf("disabled user context leaked: %s", result.InstructionContext.InstructionsText)
	}
	if result.InstructionContext.Git.Skipped != true || len(result.InstructionContext.Sources) != 2 || result.Summary.Rules != 1 || result.Summary.Skills != 2 {
		t.Fatalf("preview = %#v", result)
	}
}

func TestWorkspaceContextAPISupportsSubprojectAndLimits(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := t.TempDir()
	sub := filepath.Join(root, "packages", "app")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "AGENTS.md"), []byte("first line\nsecond line"), 0644); err != nil {
		t.Fatal(err)
	}
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	New(API{Workspaces: manager}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/workspaces/"+item.ID+"/context?path=packages/app&include_git=false&max_section_bytes=8&max_lines_per_section=1", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var result projectcontext.Result
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	resultRootInfo, err := os.Stat(result.Root)
	if err != nil {
		t.Fatal(err)
	}
	subInfo, err := os.Stat(sub)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(resultRootInfo, subInfo) || len(result.InstructionContext.ProjectMemory.Sections) != 1 {
		t.Fatalf("result = %#v", result)
	}
	section := result.InstructionContext.ProjectMemory.Sections[0]
	if !section.Truncated || section.LoadedBytes > 8 || strings.Contains(section.Content, "second") {
		t.Fatalf("section = %#v", section)
	}
}

func TestWorkspaceContextAPISupportsSelectiveMemory(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", configRoot)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(configRoot, "workspaces.json"))
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	store := memory.NewWorkspaceStore(configRoot, manager)
	for _, entry := range []memory.Entry{
		{Scope: "tui", Key: "theme", Note: "Use Charm default component styles."},
		{Scope: "release", Key: "ci", Note: "Publish with GitHub Actions."},
	} {
		if _, err := store.Upsert(item.ID, entry.Scope, entry.Key, entry.Note); err != nil {
			t.Fatal(err)
		}
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+item.ID+"/context?include_git=false&memory_query=tui+theme&max_memory_entries=1&max_memory_bytes=8192", nil)
	New(API{Workspaces: manager}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var result projectcontext.Result
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	auto := result.InstructionContext.AutoMemory
	if !auto.Loaded || auto.Query != "tui theme" || auto.Entries != 1 || !auto.Truncated || !strings.Contains(auto.Content, "### theme") || strings.Contains(auto.Content, "### ci") {
		t.Fatalf("auto memory = %#v", auto)
	}
}

func TestInstructionSettingsGETOnlyReportsDetectedSources(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	store := instructionpolicy.DefaultStore()
	value := instructionpolicy.DefaultConfig()
	disabled := false
	value.Sources["cursor"] = instructionpolicy.SourcePolicy{Skills: &disabled}
	if err := store.Save(value); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	New(API{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/instructions/global", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response instructionSettingsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.DetectedSources) != 0 || response.SourcePolicy["cursor"].Skills == nil {
		t.Fatalf("response = %#v", response)
	}
}
