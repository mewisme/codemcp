package application

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const internalImportPrefix = "go.mewis.me/codemcp/internal/"

var canonicalTopLevelScopes = map[string]string{
	"app":                    "composition",
	"application":            "application",
	"approval":               "domain",
	"auth":                   "domain",
	"backgroundcontinuation": "application",
	"backgrounddelivery":     "application",
	"capability":             "application",
	"checkpoint":             "history",
	"cli":                    "interface",
	"commandalias":           "domain",
	"commandpattern":         "domain",
	"config":                 "domain",
	"configbundle":           "persistence",
	"configformat":           "persistence",
	"controlguard":           "domain",
	"controlplane":           "runtime",
	"doctor":                 "application",
	"explain":                "domain",
	"git":                    "platform",
	"history":                "history",
	"idgen":                  "domain",
	"install":                "platform",
	"instance":               "persistence",
	"instructioncontext":     "application",
	"instructionpolicy":      "domain",
	"instructionsource":      "domain",
	"integrations":           "integration",
	"interface":              "interface",
	"jsruntime":              "runtime",
	"llm":                    "domain",
	"logger":                 "runtime",
	"mcp":                    "runtime",
	"mcpauth":                "runtime",
	"mcpconfig":              "application",
	"memory":                 "domain",
	"migration":              "persistence",
	"network":                "platform",
	"notification":           "application",
	"oauth":                  "runtime",
	"oslock":                 "platform",
	"outboundpolicy":         "domain",
	"patch":                  "domain",
	"plan":                   "domain",
	"projectcontext":         "application",
	"productadapter":         "application",
	"releaseverify":          "tooling",
	"rules":                  "domain",
	"runtime":                "runtime",
	"sequence":               "domain",
	"secretinventory":        "persistence",
	"secretstore":            "persistence",
	"service":                "platform",
	"skills":                 "domain",
	"state":                  "persistence",
	"systeminfo":             "platform",
	"telegram":               "integration",
	"telemetry":              "runtime",
	"testutil":               "test",
	"tools":                  "application",
	"trace":                  "runtime",
	"tunnel":                 "integration",
	"update":                 "platform",
	"upstream":               "integration",
	"version":                "domain",
	"workspace":              "domain",
}

func TestCanonicalInternalPackageMapCoversTopLevelRoots(t *testing.T) {
	root := architectureRepositoryRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "internal"))
	if err != nil {
		t.Fatal(err)
	}
	actual := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			actual = append(actual, entry.Name())
		}
	}
	sort.Strings(actual)
	expected := make([]string, 0, len(canonicalTopLevelScopes))
	for name := range canonicalTopLevelScopes {
		expected = append(expected, name)
	}
	sort.Strings(expected)
	if strings.Join(actual, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("internal package map drifted\nactual:\n%s\nexpected:\n%s", strings.Join(actual, "\n"), strings.Join(expected, "\n"))
	}
}

func TestLowerLayersDoNotImportInterfaceAdapters(t *testing.T) {
	root := architectureRepositoryRoot(t)
	for name, scope := range canonicalTopLevelScopes {
		if scope == "interface" || scope == "composition" || scope == "test" {
			continue
		}
		assertNoImportsWithPrefix(t, filepath.Join(root, "internal", name), internalImportPrefix+"interface/")
	}
}

func TestInterfaceAdaptersDoNotImportSiblingAdapters(t *testing.T) {
	root := architectureRepositoryRoot(t)
	interfaceRoot := filepath.Join(root, "internal", "interface")
	entries, err := os.ReadDir(interfaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		adapter := entry.Name()
		err := filepath.WalkDir(filepath.Join(interfaceRoot, adapter), func(path string, item os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if item.IsDir() || !strings.HasSuffix(item.Name(), ".go") {
				return nil
			}
			for _, imported := range goFileImports(t, path) {
				const prefix = internalImportPrefix + "interface/"
				if !strings.HasPrefix(imported, prefix) {
					continue
				}
				remainder := strings.TrimPrefix(imported, prefix)
				sibling, _, _ := strings.Cut(remainder, "/")
				if sibling != adapter {
					t.Errorf("%s adapter imports sibling interface %q via %s", adapter, sibling, imported)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestProductTelemetryOwnerCannotDependOnRichLocalObservabilityOrInterfaces(t *testing.T) {
	root := architectureRepositoryRoot(t)
	productRoot := filepath.Join(root, "internal", "telemetry", "product")
	if info, err := os.Stat(productRoot); err != nil || !info.IsDir() {
		t.Fatalf("product telemetry owner is missing: path=%s info=%v err=%v", productRoot, info, err)
	}
	for _, forbidden := range []string{
		internalImportPrefix + "interface/",
		internalImportPrefix + "logger",
		internalImportPrefix + "runtime/activity",
		internalImportPrefix + "telemetry",
	} {
		assertNoImportsWithPrefix(t, productRoot, forbidden)
	}
}

func TestLLMDomainDoesNotDependOnApprovalSemanticPolicyOrInterfaces(t *testing.T) {
	root := architectureRepositoryRoot(t)
	llmRoot := filepath.Join(root, "internal", "llm")
	for _, forbidden := range []string{
		internalImportPrefix + "approval",
		internalImportPrefix + "integrations/semantic",
		internalImportPrefix + "interface/",
	} {
		assertNoImportsWithPrefix(t, llmRoot, forbidden)
	}
}

func TestLLMProviderManagementDoesNotDependOnIntegrationOrUpstreamRegistration(t *testing.T) {
	root := architectureRepositoryRoot(t)
	for _, name := range []string{
		"internal/application/llm_settings.go",
		"internal/application/llm_providers.go",
		"internal/application/llm_models.go",
	} {
		for _, imported := range goFileImports(t, filepath.Join(root, filepath.FromSlash(name))) {
			if imported == internalImportPrefix+"upstream" || strings.HasPrefix(imported, internalImportPrefix+"integrations/") {
				t.Errorf("%s must not register LLM providers through %s", name, imported)
			}
		}
	}
}

func TestHumanInterfacesCannotBypassLLMApplicationPersistenceOwner(t *testing.T) {
	root := architectureRepositoryRoot(t)
	for _, relativeRoot := range []string{"internal/cli", "internal/interface/admin", "internal/interface/tui", "internal/telegram"} {
		err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(relativeRoot)), func(path string, item os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if item.IsDir() || !strings.HasSuffix(item.Name(), ".go") || strings.HasSuffix(item.Name(), "_test.go") {
				return nil
			}
			for _, imported := range goFileImports(t, path) {
				if imported == internalImportPrefix+"llm" {
					t.Errorf("%s imports the LLM persistence/domain package directly; interfaces must use application.LLMService", filepath.ToSlash(path))
				}
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, forbidden := range []string{"secretstore.DomainLLM", "llm/providers.json", "llm.NewStore(", "llm.RemoveProvider(", "llm.CredentialChange("} {
				if strings.Contains(string(body), forbidden) {
					t.Errorf("%s bypasses the LLM application persistence owner via %q", filepath.ToSlash(path), forbidden)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestLLMApplicationDoesNotRegisterApprovalOrSemanticPolicy(t *testing.T) {
	root := architectureRepositoryRoot(t)
	applicationRoot := filepath.Join(root, "internal", "application")
	entries, err := os.ReadDir(applicationRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "llm_") || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(applicationRoot, entry.Name())
		for _, imported := range goFileImports(t, path) {
			if imported == internalImportPrefix+"approval" || strings.HasPrefix(imported, internalImportPrefix+"integrations/semantic") {
				t.Errorf("%s couples LLM orchestration to policy package %s", filepath.ToSlash(path), imported)
			}
		}
	}
	for _, relativeRoot := range []string{"internal/approval", "internal/integrations/semantic"} {
		path := filepath.Join(root, filepath.FromSlash(relativeRoot))
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			continue
		}
		assertNoImportsWithPrefix(t, path, internalImportPrefix+"llm")
	}
}

func TestApprovalExplainDoesNotPersistOrTraceModelPayloads(t *testing.T) {
	root := architectureRepositoryRoot(t)
	path := filepath.Join(root, "internal", "application", "approval_explain.go")
	for _, imported := range goFileImports(t, path) {
		for _, forbidden := range []string{
			internalImportPrefix + "logger",
			internalImportPrefix + "telemetry",
			internalImportPrefix + "state",
			internalImportPrefix + "history",
			internalImportPrefix + "integrations/semantic",
		} {
			if imported == forbidden || strings.HasPrefix(imported, forbidden+"/") {
				t.Errorf("approval explanation service imports forbidden payload/policy sink %s", imported)
			}
		}
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"tracepkg.Start(", "tracepkg.Emit(", "tracepkg.Any(", "tracepkg.String("} {
		if strings.Contains(string(body), forbidden) {
			t.Errorf("approval explanation service can trace model payloads via %q", forbidden)
		}
	}
}

func TestRepresentativeWorkspaceAdaptersCannotBypassApplicationMutationOwner(t *testing.T) {
	root := architectureRepositoryRoot(t)
	files := []string{
		"internal/cli/workspace.go",
		"internal/interface/admin/workspaces.go",
		"internal/interface/admin/workspace_containers.go",
		"internal/interface/tui/page/workspace.go",
		"internal/interface/tui/page/workspace_editor.go",
		"internal/telegram/workspace_approval.go",
	}
	directMutation := regexp.MustCompile(`(?:page\.)?manager\.(?:Register|Unregister|Relocate|CreateContainer|RenameContainer|DeleteContainer|AddAllowDir|RemoveAllowDir|AddWorkspacesToContainer|RemoveWorkspacesFromContainer|AddWorkspaceToContainers|RemoveWorkspaceFromContainers)\s*\(`)
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if match := directMutation.Find(data); match != nil {
			t.Errorf("%s bypasses canonical workspace application owner via %q", name, string(match))
		}
	}
}

func TestReleasedCompatibilityReadersStayBehindMigrationBoundary(t *testing.T) {
	root := architectureRepositoryRoot(t)
	legacyPackages := []string{
		internalImportPrefix + "migration/bundle024",
		internalImportPrefix + "migration/configformat",
		internalImportPrefix + "migration/credentials024",
		internalImportPrefix + "migration/integrations024",
		internalImportPrefix + "migration/upstream024",
		internalImportPrefix + "migration/workspace024",
	}
	internalRoot := filepath.Join(root, "internal")
	err := filepath.WalkDir(internalRoot, func(path string, item os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if item.IsDir() {
			if path == filepath.Join(internalRoot, "migration") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(item.Name(), ".go") || strings.HasSuffix(item.Name(), "_test.go") {
			return nil
		}
		for _, imported := range goFileImports(t, path) {
			for _, forbidden := range legacyPackages {
				if imported == forbidden || strings.HasPrefix(imported, forbidden+"/") {
					t.Errorf("%s imports released compatibility package %s directly", filepath.ToSlash(path), imported)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, currentRuntimeRoot := range []string{"config", "runtime", "service", "secretstore", "upstream", "workspace"} {
		assertNoImportsWithPrefix(t, filepath.Join(internalRoot, currentRuntimeRoot), internalImportPrefix+"migration/")
	}
}

func assertNoImportsWithPrefix(t *testing.T, root, forbiddenPrefix string) {
	t.Helper()
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return
	}
	err := filepath.WalkDir(root, func(path string, item os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".go") {
			return nil
		}
		for _, imported := range goFileImports(t, path) {
			if strings.HasPrefix(imported, forbiddenPrefix) {
				t.Errorf("%s imports forbidden interface adapter %s", filepath.ToSlash(path), imported)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func goFileImports(t *testing.T, path string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	imports := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		value, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("decode import in %s: %v", path, err)
		}
		imports = append(imports, value)
	}
	return imports
}

func architectureRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve architecture test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
