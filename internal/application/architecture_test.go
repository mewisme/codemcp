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
	"projectcontext":         "application",
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

func TestRepresentativeWorkspaceAdaptersCannotBypassApplicationMutationOwner(t *testing.T) {
	root := architectureRepositoryRoot(t)
	files := []string{
		"internal/cli/workspace.go",
		"internal/interface/admin/workspaces.go",
		"internal/interface/admin/workspace_containers.go",
		"internal/interface/tui/page/workspace.go",
		"internal/interface/tui/page/workspace_editor.go",
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
