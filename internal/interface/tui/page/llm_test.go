package page

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/llm"
)

func TestLLMCoreProviderDetailProtectsIdentity(t *testing.T) {
	service := application.NewLLMService(t.TempDir())
	page, err := newLLMRouteAction(t.Context(), "openrouter", "", "", service)
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := page.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	page = updated.(*LLMPage)
	view := ansi.Strip(page.View(80, 24))
	for _, want := range []string{"core · immutable", "m models", "p probe", "k set key"} {
		if !strings.Contains(view, want) {
			t.Fatalf("core provider detail missing %q: %q", want, view)
		}
	}
	for _, forbidden := range []string{"e edit", "d remove"} {
		if strings.Contains(view, forbidden) {
			t.Fatalf("core provider detail exposed %q: %q", forbidden, view)
		}
	}
}

func TestLLMCredentialEditorNeverRendersRawSecret(t *testing.T) {
	service := application.NewLLMService(t.TempDir())
	provider, err := service.ProviderResult(t.Context(), "openrouter")
	if err != nil {
		t.Fatal(err)
	}
	editor, data := newLLMCredentialEditor(provider)
	const secret = "TUI_SECRET_DO_NOT_RENDER"
	data.Value = secret
	editor.Resize(70, 20)
	if view := ansi.Strip(editor.View()); strings.Contains(view, secret) {
		t.Fatalf("credential editor leaked secret: %q", view)
	}
}

func TestLLMModelQueryUsesCanonicalModesAndFilters(t *testing.T) {
	data := &llmModelQueryFormData{
		Search: "vision", ExactIDs: "a,b", Price: "free", Authors: "openai,anthropic",
		MinContext: "8192", Capabilities: "tools,vision", Sort: "context_length:desc",
		PageMode: "page", Offset: "5", Limit: "10",
	}
	query, err := data.Query()
	if err != nil {
		t.Fatal(err)
	}
	if query.Search != "vision" || len(query.ExactIDs) != 2 || len(query.Authors) != 2 || query.MinContext == nil || *query.MinContext != 8192 || query.Free == nil || !*query.Free || query.Offset != 5 || query.Limit != 10 || len(query.Sort) != 1 {
		t.Fatalf("page query=%#v", query)
	}

	rangeQuery, err := (&llmModelQueryFormData{PageMode: "range", Range: "10:20"}).Query()
	if err != nil {
		t.Fatal(err)
	}
	if rangeQuery.Range == nil || rangeQuery.Range.Start != 10 || rangeQuery.Range.End != 20 {
		t.Fatalf("range query=%#v", rangeQuery)
	}

	allQuery, err := (&llmModelQueryFormData{PageMode: "all"}).Query()
	if err != nil || !allQuery.All {
		t.Fatalf("all query=%#v err=%v", allQuery, err)
	}

	countQuery, err := (&llmModelQueryFormData{PageMode: "count"}).Query()
	if err != nil || !countQuery.CountOnly {
		t.Fatalf("count query=%#v err=%v", countQuery, err)
	}
	if _, err := (&llmModelQueryFormData{PageMode: "count", Sort: "name:asc"}).Query(); err == nil {
		t.Fatal("count-only query accepted sorting")
	}
}

func TestLLMModelBrowserRestoresSelectionAfterAsyncLoad(t *testing.T) {
	page := &LLMPage{ctx: t.Context(), resourceID: "openrouter", section: "models", query: application.LLMModelQuery{Limit: 25}, restoreSelected: "model-b"}
	page.finishModels(llmModelsMsg{page: application.LLMModelPage{
		ProviderID: "openrouter", Matched: 2, Returned: 2, Limit: 25,
		Models: []llm.Model{{ID: "model-a"}, {ID: "model-b"}},
	}})
	row, ok := page.browser.Selected()
	if !ok || row.ID != "model-b" {
		t.Fatalf("selected=%#v ok=%t", row, ok)
	}
	if page.restoreSelected != "" {
		t.Fatalf("restore selection not cleared: %q", page.restoreSelected)
	}
}

func TestLLMExactModelEditorSupportsUndiscoveredIDs(t *testing.T) {
	service := application.NewLLMService(t.TempDir())
	page, err := newLLMRouteAction(t.Context(), "openrouter", "models", "set", service)
	if err != nil {
		t.Fatal(err)
	}
	const modelID = "vendor/model-not-in-current-page"
	page.modelForm.Model = modelID
	cmd := page.submitEditor()
	if cmd == nil {
		t.Fatal("exact model editor returned no submit command")
	}
	msg, ok := cmd().(llmMutationMsg)
	if !ok || msg.err != nil {
		t.Fatalf("exact model mutation=%#v", msg)
	}
	provider, err := service.ProviderResult(t.Context(), "openrouter")
	if err != nil {
		t.Fatal(err)
	}
	if provider.Model != modelID {
		t.Fatalf("model=%q want=%q", provider.Model, modelID)
	}
}
