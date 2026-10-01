package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/secretstore"
)

func TestLLMCLICommandTreeReachesCanonicalOperations(t *testing.T) {
	root := newRootCommand()
	tests := map[string]capability.ID{
		"llm status":             capability.LLMStatus,
		"llm use":                capability.LLMProviderSelect,
		"llm models":             capability.LLMProviderModels,
		"llm probe":              capability.LLMProviderProbe,
		"llm provider list":      capability.LLMProviderList,
		"llm provider show":      capability.LLMProviderGet,
		"llm provider add":       capability.LLMProviderAdd,
		"llm provider configure": capability.LLMProviderConfigure,
		"llm provider remove":    capability.LLMProviderRemove,
		"llm provider key set":   capability.LLMProviderCredentialSet,
		"llm provider key clear": capability.LLMProviderCredentialClear,
		"llm ollama status":      capability.LLMProviderGet,
		"llm ollama use":         capability.LLMProviderSelect,
		"llm ollama models":      capability.LLMProviderModels,
		"llm ollama mode":        capability.LLMProviderConfigure,
		"llm ollama model":       capability.LLMProviderConfigure,
		"llm ollama key set":     capability.LLMProviderCredentialSet,
		"llm ollama key clear":   capability.LLMProviderCredentialClear,
		"request explain":        capability.RequestExplain,
		"request explain retry":  capability.RequestExplain,
		"request explain status": capability.RequestExplainStatus,
		"request explain mode":   capability.ConfigSet,
	}
	for path, want := range tests {
		command := commandByRelativePath(root, path)
		if command == nil || !command.Runnable() {
			t.Errorf("command %q missing or not runnable", path)
			continue
		}
		got, ok := canonicalCommandOperation(command)
		if !ok || got != want {
			t.Errorf("command %q operation=%q,%t want=%q,true", path, got, ok, want)
		}
	}
}

func TestLLMCLICustomProviderCRUDAndCoreRemovalProtection(t *testing.T) {
	root := isolateLLMCLI(t)

	stdout, stderr, err := executeLLMCLI(root, nil,
		"llm", "provider", "add", "fixture-provider",
		"--protocol", "openai", "--base-url", "http://127.0.0.1:65534/v1", "--model", "fixture-v1", "--json",
	)
	if err != nil || stderr != "" {
		t.Fatalf("add err=%v stderr=%q stdout=%q", err, stderr, stdout)
	}
	var added application.LLMProviderResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &added); err != nil || string(added.ID) != "fixture-provider" || added.Model != "fixture-v1" {
		t.Fatalf("add output=%q provider=%#v err=%v", stdout, added, err)
	}

	if _, _, err := executeLLMCLI(root, nil, "llm", "use", "fixture-provider", "--json"); err != nil {
		t.Fatal(err)
	}
	stdout, _, err = executeLLMCLI(root, nil, "llm", "provider", "configure", "fixture-provider", "--model", "fixture-v2", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var configured application.LLMProviderResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &configured); err != nil || configured.Model != "fixture-v2" || !configured.Selected {
		t.Fatalf("configure output=%q provider=%#v err=%v", stdout, configured, err)
	}

	service := application.NewLLMService(root)
	before, err := service.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, stderr, removeErr := executeLLMCLI(root, nil, "llm", "provider", "remove", "ollama", "--yes")
	if removeErr == nil || !strings.Contains(strings.ToLower(removeErr.Error()+" "+stderr), "core provider") {
		t.Fatalf("core remove err=%v stderr=%q", removeErr, stderr)
	}
	after, err := service.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("core removal mutated catalog: before=%#v after=%#v", before, after)
	}

	if _, _, err := executeLLMCLI(root, nil, "llm", "use", "ollama", "--json"); err != nil {
		t.Fatal(err)
	}
	stdout, _, err = executeLLMCLI(root, nil, "llm", "provider", "remove", "fixture-provider", "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var removed application.LLMProviderRemoveResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &removed); err != nil || !removed.Removed || string(removed.ProviderID) != "fixture-provider" {
		t.Fatalf("remove output=%q result=%#v err=%v", stdout, removed, err)
	}
}

func TestLLMCLIProtectedAPIKeyNeverAppearsInArgumentsOutputOrConfigRoot(t *testing.T) {
	root := isolateLLMCLI(t)
	const secret = "sk-or-v1-cli-secret-that-must-never-leak"
	args := []string{"llm", "provider", "key", "set", "ollama", "--json"}
	for _, arg := range args {
		if strings.Contains(arg, secret) {
			t.Fatalf("secret entered argv: %q", args)
		}
	}
	stdout, stderr, err := executeLLMCLI(root, strings.NewReader(secret+"\n"), args...)
	combined := stdout + "\n" + stderr
	if err != nil {
		combined += "\n" + err.Error()
	}
	if strings.Contains(combined, secret) {
		t.Fatalf("secret leaked to command output/error: %q", combined)
	}
	if err != nil {
		t.Fatalf("set key err=%v stderr=%q", err, stderr)
	}
	var result application.LLMCredentialResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &result); err != nil || !result.Configured || result.Preview == "" || strings.Contains(result.Preview, secret) {
		t.Fatalf("credential output=%q result=%#v err=%v", stdout, result, err)
	}
	if leakedFile := findStringInTree(t, root, secret); leakedFile != "" {
		t.Fatalf("secret leaked into config/log/trace file %s", leakedFile)
	}

	const envName = "CODEMCP_TEST_LLM_API_KEY"
	const envSecret = "sk-or-v1-env-secret-that-must-never-leak"
	t.Setenv(envName, envSecret)
	stdout, stderr, err = executeLLMCLI(root, nil, "llm", "ollama", "key", "set", "--from-env", envName, "--json")
	combined = stdout + "\n" + stderr
	if err != nil {
		combined += "\n" + err.Error()
	}
	if strings.Contains(combined, envSecret) {
		t.Fatalf("environment-sourced secret leaked to command output/error: %q", combined)
	}
	if err != nil {
		t.Fatalf("environment key set err=%v stderr=%q", err, stderr)
	}
	if leakedFile := findStringInTree(t, root, envSecret); leakedFile != "" {
		t.Fatalf("environment-sourced secret leaked into config/log/trace file %s", leakedFile)
	}
}

func TestLLMCLICoreRemovalFailureHasActionableRemediation(t *testing.T) {
	root := isolateLLMCLI(t)
	stdout, stderr, err := executeLLMCLI(root, nil, "llm", "provider", "remove", "ollama", "--yes")
	if err == nil {
		t.Fatal("core provider removal unexpectedly succeeded")
	}
	output := stdout + "\n" + stderr
	for _, want := range []string{"Core LLM provider is protected", "cm llm provider list", "cm llm status"} {
		if !strings.Contains(output, want) {
			t.Fatalf("core removal remediation missing %q: stdout=%q stderr=%q err=%v", want, stdout, stderr, err)
		}
	}
}

func TestLLMCLIProviderAndModelCompletionNeverCallRemoteEndpoint(t *testing.T) {
	root := isolateLLMCLI(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	if _, _, err := executeLLMCLI(root, nil,
		"llm", "provider", "add", "offline-completion",
		"--protocol", "openai", "--base-url", server.URL+"/v1", "--model", "cached-model", "--json",
	); err != nil {
		t.Fatal(err)
	}

	completionRoot := newRootCommand()
	completionRoot.SetContext(context.Background())
	completionRoot.SetArgs([]string{"--config-dir", root})
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	use := commandByRelativePath(completionRoot, "llm use")
	values, _ := use.ValidArgsFunction(use, nil, "offline")
	if !containsCompletion(values, "offline-completion") {
		t.Fatalf("provider completion=%v", values)
	}
	configure := commandByRelativePath(completionRoot, "llm provider configure")
	values, _ = completeConfiguredLLMModel(configure, []string{"offline-completion"}, "cached")
	if !containsCompletion(values, "cached-model") {
		t.Fatalf("model completion=%v", values)
	}
	model := commandByRelativePath(completionRoot, "llm ollama model")
	if model == nil || model.ValidArgsFunction == nil {
		t.Fatal("ollama model completion is unavailable")
	}
	_, _ = model.ValidArgsFunction(model, nil, "ollama/")
	if got := requests.Load(); got != 0 {
		t.Fatalf("completion performed %d remote request(s)", got)
	}
}

func TestLLMCLIModelQueryGrammarIsSharedAcrossProviderNamespaces(t *testing.T) {
	root := newRootCommand()
	for _, path := range []string{"llm models", "llm ollama models", "llm ollama models"} {
		command := commandByRelativePath(root, path)
		if command == nil {
			t.Fatalf("command %q missing", path)
		}
		for _, flag := range []string{
			"search", "id", "author", "free", "paid", "min-context", "max-context",
			"min-prompt-price", "max-prompt-price", "min-completion-price", "max-completion-price",
			"capability", "parameter", "input", "output", "family", "format", "quantization",
			"min-parameters", "max-parameters", "min-size", "max-size", "sort", "rank", "window",
			"recommend-for", "offset", "limit", "range", "count", "all", "refresh", "check-access",
		} {
			if command.Flags().Lookup(flag) == nil {
				t.Fatalf("command %q missing --%s", path, flag)
			}
		}
	}
}

func TestLLMCLIModelQueryEnumCompletionsAreLocal(t *testing.T) {
	root := newRootCommand()
	models := commandByRelativePath(root, "llm models")
	if models == nil {
		t.Fatal("llm models command missing")
	}
	tests := map[string][]string{
		"sort":       {"id:asc", "context:desc", "parameter-size:asc"},
		"rank":       {"usage", "trending", "coding"},
		"window":     {"day", "week", "month"},
		"capability": {"structured-output", "tools"},
		"parameter":  {"structured_outputs", "tool_choice"},
		"input":      {"text", "image", "audio"},
		"output":     {"text", "audio"},
	}
	for flag, expected := range tests {
		completion, ok := models.GetFlagCompletionFunc(flag)
		if !ok {
			t.Fatalf("--%s completion missing", flag)
		}
		values, directive := completion(models, []string{"ollama"}, "")
		if directive != cobra.ShellCompDirectiveNoFileComp {
			t.Fatalf("--%s directive=%v", flag, directive)
		}
		for _, value := range expected {
			if !containsCompletion(values, value) {
				t.Fatalf("--%s completion=%v missing %q", flag, values, value)
			}
		}
	}
}

func TestLLMCLIModelQueryJSONUsesCanonicalFilteringSortingAndRange(t *testing.T) {
	root := isolateLLMCLI(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"data":[
			{"id":"acme/a","name":"A","context_length":64000,"pricing":{"prompt":"0.000002","completion":"0.000004"}},
			{"id":"acme/b","name":"B","context_length":128000,"pricing":{"prompt":"0.000001","completion":"0.000003"}},
			{"id":"other/c","name":"C","context_length":256000,"pricing":{"prompt":"0","completion":"0"}}
		]}`)
	}))
	defer server.Close()
	if _, _, err := executeLLMCLI(root, nil,
		"llm", "provider", "add", "query-fixture", "--protocol", "openai", "--base-url", server.URL+"/v1",
		"--model", "acme/b", "--auth", "none", "--discovery", "openai-models", "--json",
	); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := executeLLMCLI(root, nil,
		"llm", "models", "query-fixture", "--author", "acme", "--max-prompt-price", "0.000002",
		"--sort", "context:desc", "--range", "1:1", "--json",
	)
	if err != nil || stderr != "" {
		t.Fatalf("query err=%v stderr=%q stdout=%q", err, stderr, stdout)
	}
	var page application.LLMModelPage
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &page); err != nil {
		t.Fatal(err)
	}
	if page.ProviderID != "query-fixture" || page.TotalCatalog != 3 || page.Matched != 2 || page.Offset != 0 || page.Limit != 1 || page.Returned != 1 || !page.HasMore || len(page.Models) != 1 || page.Models[0].ID != "acme/b" {
		t.Fatalf("query page=%#v", page)
	}

	stdout, stderr, err = executeLLMCLI(root, nil, "llm", "models", "query-fixture", "--author", "acme", "--count", "--json")
	if err != nil || stderr != "" {
		t.Fatalf("count err=%v stderr=%q stdout=%q", err, stderr, stdout)
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &page); err != nil || page.Matched != 2 || page.Returned != 0 || len(page.Models) != 0 {
		t.Fatalf("count page=%#v err=%v", page, err)
	}

	if _, _, err := executeLLMCLI(root, nil, "llm", "models", "query-fixture", "--all", "--limit", "1", "--json"); err == nil {
		t.Fatal("mutually exclusive --all and --limit were accepted")
	}
}

func TestLLMCLIStatusHumanPlainAndJSONAreDeterministic(t *testing.T) {
	root := isolateLLMCLI(t)
	plainA, _, err := executeLLMCLI(root, nil, "llm", "status")
	if err != nil {
		t.Fatal(err)
	}
	plainB, _, err := executeLLMCLI(root, nil, "llm", "status")
	if err != nil || plainA != plainB {
		t.Fatalf("plain output changed: err=%v\nA=%q\nB=%q", err, plainA, plainB)
	}
	jsonA, _, err := executeLLMCLI(root, nil, "llm", "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	jsonB, _, err := executeLLMCLI(root, nil, "llm", "status", "--json")
	if err != nil || jsonA != jsonB || !json.Valid([]byte(strings.TrimSpace(jsonA))) {
		t.Fatalf("json output changed/invalid: err=%v\nA=%q\nB=%q", err, jsonA, jsonB)
	}
	humanA, err := executeInteractiveLifecycleCommand(root, "llm", "status")
	if err != nil {
		t.Fatal(err)
	}
	humanB, err := executeInteractiveLifecycleCommand(root, "llm", "status")
	if err != nil || humanA != humanB || !strings.Contains(humanA, "┌  LLM") || !strings.Contains(humanA, "└  Done") {
		t.Fatalf("human output changed/invalid: err=%v\nA=%q\nB=%q", err, humanA, humanB)
	}
}

func TestLLMCLIUsesOnlyIsolatedConfigRoot(t *testing.T) {
	root := isolateLLMCLI(t)
	if _, _, err := executeLLMCLI(root, nil, "llm", "provider", "add", "root-proof", "--protocol", "openai", "--base-url", "http://127.0.0.1:65534/v1", "--json"); err != nil {
		t.Fatal(err)
	}
	if got := filepath.Clean(config.RootPath()); got != filepath.Clean(root) {
		t.Fatalf("config root=%q want isolated %q", got, root)
	}
	if _, err := os.Stat(filepath.Join(root, "llm", "providers.json")); err != nil {
		t.Fatalf("isolated provider store missing: %v", err)
	}
}

func isolateLLMCLI(t *testing.T) string {
	t.Helper()
	root := isolateUniversalConfigCLI(t)
	t.Setenv(configformat.EnvConfigDir, root)
	restore := secretstore.UseMemoryForTesting()
	t.Cleanup(restore)
	return root
}

func executeLLMCLI(root string, input io.Reader, args ...string) (string, string, error) {
	var stdout, stderr bytes.Buffer
	cmd := newRootCommand()
	cmd.SetContext(context.Background())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if input != nil {
		cmd.SetIn(input)
	}
	cmd.SetArgs(append([]string{"--config-dir", root}, args...))
	err := executeCommand(cmd)
	return stdout.String(), stderr.String(), err
}

func findStringInTree(t *testing.T, root, needle string) string {
	t.Helper()
	var found string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(needle)) {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func containsCompletion(values []string, want string) bool {
	for _, value := range values {
		value, _, _ = strings.Cut(value, "\t")
		if value == want {
			return true
		}
	}
	return false
}
