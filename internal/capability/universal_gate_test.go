package capability

import (
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const applicationExportFingerprint = "af16248e122d9fae267e66a7217e248f844e04bca56fd0b74d520a51f2b82fed"

func TestUniversalOperationAndSurfaceGate(t *testing.T) {
	operations := All()
	rows := ParityMatrix()
	if len(rows) != len(operations) {
		t.Fatalf("parity rows=%d operations=%d", len(rows), len(operations))
	}

	lifecycles := map[Surface]SurfaceLifecycle{}
	for _, lifecycle := range SurfaceLifecycles() {
		if _, exists := lifecycles[lifecycle.Surface]; exists {
			t.Fatalf("duplicate surface lifecycle: %s", lifecycle.Surface)
		}
		lifecycles[lifecycle.Surface] = lifecycle
	}
	for _, surface := range AllSurfaces {
		if _, ok := lifecycles[surface]; !ok {
			t.Fatalf("surface %s has no lifecycle declaration", surface)
		}
	}

	seenOperations := map[ID]bool{}
	for _, row := range rows {
		if seenOperations[row.Operation] {
			t.Fatalf("duplicate parity row for operation %s", row.Operation)
		}
		seenOperations[row.Operation] = true
		spec, ok := Lookup(row.Operation)
		if !ok {
			t.Fatalf("parity row references unknown operation %s", row.Operation)
		}
		if len(row.Surfaces) != len(AllSurfaces) {
			t.Fatalf("operation %s surface mappings=%d want=%d", row.Operation, len(row.Surfaces), len(AllSurfaces))
		}

		seenSurfaces := map[Surface]bool{}
		for _, mapping := range row.Surfaces {
			if seenSurfaces[mapping.Surface] {
				t.Fatalf("operation %s duplicates surface %s", row.Operation, mapping.Surface)
			}
			seenSurfaces[mapping.Surface] = true
			contract, ok := spec.Surface(mapping.Surface)
			if !ok {
				t.Fatalf("operation %s parity mapping has no canonical surface contract for %s", row.Operation, mapping.Surface)
			}
			if mapping.State != contract.State || mapping.Exemption != contract.Exemption || mapping.Reason != contract.Reason {
				t.Fatalf("operation %s/%s parity drift: mapping=%#v contract=%#v", row.Operation, mapping.Surface, mapping, contract)
			}

			lifecycle, ok := lifecycles[mapping.Surface]
			if !ok {
				t.Fatalf("operation %s references undeclared surface %s", row.Operation, mapping.Surface)
			}
			switch mapping.State {
			case SurfaceRequired:
				if mapping.Exemption != "" || mapping.Reason != "" {
					t.Fatalf("required operation %s/%s carries exemption metadata: %#v", row.Operation, mapping.Surface, mapping)
				}
				if !lifecycle.Active {
					t.Fatalf("operation %s requires inactive surface %s", row.Operation, mapping.Surface)
				}
				if len(mapping.EntryPoints) == 0 {
					t.Fatalf("required operation %s lacks production adapter entry point for %s", row.Operation, mapping.Surface)
				}
				for _, entry := range mapping.EntryPoints {
					if strings.TrimSpace(entry) == "" {
						t.Fatalf("required operation %s/%s has empty production entry point", row.Operation, mapping.Surface)
					}
				}
			case SurfacePlanned:
				if !validSurfaceExemption(mapping.Exemption) {
					t.Fatalf("planned operation %s/%s has invalid exemption class %q", row.Operation, mapping.Surface, mapping.Exemption)
				}
				if lifecycle.Active {
					t.Fatalf("operation %s keeps planned state on active surface %s", row.Operation, mapping.Surface)
				}
				if !validSurfaceReason(mapping.Reason) {
					t.Fatalf("planned operation %s/%s has unbounded lifecycle reason %q", row.Operation, mapping.Surface, mapping.Reason)
				}
				if len(mapping.EntryPoints) != 0 {
					t.Fatalf("planned operation %s/%s advertises live entry points: %v", row.Operation, mapping.Surface, mapping.EntryPoints)
				}
			case SurfaceExempt:
				if !validSurfaceExemption(mapping.Exemption) {
					t.Fatalf("exempt operation %s/%s has invalid exemption class %q", row.Operation, mapping.Surface, mapping.Exemption)
				}
				if !validSurfaceReason(mapping.Reason) {
					t.Fatalf("exempt operation %s/%s has unbounded lifecycle reason %q", row.Operation, mapping.Surface, mapping.Reason)
				}
			default:
				t.Fatalf("operation %s/%s has unknown lifecycle state %q", row.Operation, mapping.Surface, mapping.State)
			}
		}
		for _, surface := range AllSurfaces {
			if !seenSurfaces[surface] {
				t.Fatalf("operation %s parity row omitted surface %s", row.Operation, surface)
			}
		}
	}
	for _, spec := range operations {
		if !seenOperations[spec.ID] {
			t.Fatalf("canonical operation %s is missing from parity matrix", spec.ID)
		}
	}
}

func TestCanonicalOperationDeclarationsMatchInventory(t *testing.T) {
	root := capabilityRepositoryRoot(t)
	declared := declaredOperationIDs(t, filepath.Join(root, "internal", "capability"))
	inventory := map[ID]bool{}
	for _, spec := range All() {
		inventory[spec.ID] = true
	}

	var missing, stale []string
	for id := range declared {
		if !inventory[id] {
			missing = append(missing, string(id))
		}
	}
	for id := range inventory {
		if !declared[id] {
			stale = append(stale, string(id))
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("canonical operation declarations and inventory diverged: missing=%v stale=%v", missing, stale)
	}
}

func TestExportedApplicationSurfaceRequiresCapabilityReconciliation(t *testing.T) {
	root := capabilityRepositoryRoot(t)
	dir := filepath.Join(root, "internal", "application")
	set := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	exports := []string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(set, filepath.Join(dir, entry.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || !fn.Name.IsExported() {
				continue
			}
			name := "func:" + fn.Name.Name
			if fn.Recv != nil && len(fn.Recv.List) > 0 {
				receiver := receiverName(fn.Recv.List[0].Type)
				name = "method:" + receiver + "." + fn.Name.Name
			}
			exports = append(exports, name)
		}
	}
	sort.Strings(exports)
	fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(exports, "\n"))))
	if fingerprint != applicationExportFingerprint {
		t.Fatalf("exported application surface changed: fingerprint=%s exports=%d; reconcile canonical capability/application inventory before updating applicationExportFingerprint", fingerprint, len(exports))
	}
}

func TestUniversalSurfaceAdapterGuardsRemainPresent(t *testing.T) {
	root := capabilityRepositoryRoot(t)
	guards := map[string][]string{
		"internal/cli/capability_parity_test.go": {
			"TestPublicCommandsHaveCanonicalCapabilities",
			"TestRunnableCLICommandsCarryCanonicalOperationAnnotations",
		},
		"internal/interface/tui/capability_parity_test.go": {
			"TestEveryPublicCapabilityHasTUIRepresentation",
			"TestEveryTUIActionCapabilityIsDeclaredRequired",
			"TestExecutableTUIActionsCarryCanonicalOperationIDs",
		},
		"internal/interface/admin/operation_contract_test.go": {
			"TestPublicAdminOperationsHaveCanonicalIDs",
		},
		"internal/capability/adapter_enforcement_test.go": {
			"TestBrowserRequiredOperationsHaveFrontendAdapters",
		},
		"internal/tools/capability_contract_test.go": {
			"TestBuiltInToolsHaveCanonicalOperationIDs",
		},
		"internal/telegram/navigation_test.go": {
			"TestCompletedTelegramNavigationEntryPointsAreRegistered",
		},
		"internal/capability/telegram_rollout_test.go": {
			"TestTelegramRolloutFutureOwnershipIsExplicit",
		},
		"internal/application/operation_test.go": {
			"TestDispatcherReturnsCanonicalMetadataAndTypedErrors",
		},
	}
	paths := make([]string, 0, len(guards))
	for path := range guards {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, relative := range paths {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("production adapter guard %s missing: %v", relative, err)
		}
		for _, marker := range guards[relative] {
			if !strings.Contains(string(body), marker) {
				t.Errorf("production adapter guard %s lost marker %q", relative, marker)
			}
		}
	}
}

func TestUniversalInventoryDoesNotReintroduceRejectedTunnelModels(t *testing.T) {
	var corpus strings.Builder
	for _, spec := range All() {
		fmt.Fprintf(&corpus, "%s %s", spec.ID, spec.CLI.CanonicalPath)
		for _, alias := range spec.CLI.Aliases {
			fmt.Fprintf(&corpus, " %s", alias)
		}
		for _, binding := range spec.Admin {
			fmt.Fprintf(&corpus, " %s %s", binding.Method, binding.Path)
		}
		for _, tool := range spec.MCPTools {
			fmt.Fprintf(&corpus, " %s", tool)
		}
		corpus.WriteByte('\n')
	}
	for _, item := range TelegramRolloutInventory() {
		fmt.Fprintf(&corpus, "%s %s %s", item.ID, item.Stage, item.Owner)
		for _, entry := range item.EntryPoints {
			fmt.Fprintf(&corpus, " %s %s", entry.Kind, entry.Value)
		}
		corpus.WriteByte('\n')
	}
	normalized := strings.ToLower(corpus.String())
	for _, forbidden := range []string{
		"cfquick", "cf_quick",
		"admin_profile", "admin-profile", "admin.profile", "admin/profile", "admin profile",
		"tunnel.instance", "tunnel_instance", "tunnel-instance", "tunnel/instance", "tunnel instance",
		"tunnel.profile", "tunnel_profile", "tunnel-profile", "tunnel/profile", "tunnel profile",
	} {
		if strings.Contains(normalized, forbidden) {
			t.Fatalf("rejected tunnel architecture %q reappeared in capability inventory:\n%s", forbidden, normalized)
		}
	}
}

func capabilityRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve capability test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
}

func declaredOperationIDs(t *testing.T, dir string) map[ID]bool {
	t.Helper()
	set := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	result := map[ID]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(set, filepath.Join(dir, entry.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.CONST {
				continue
			}
			for _, raw := range general.Specs {
				valueSpec, ok := raw.(*ast.ValueSpec)
				if !ok {
					continue
				}
				typeName, ok := valueSpec.Type.(*ast.Ident)
				if !ok || typeName.Name != "ID" {
					continue
				}
				for _, value := range valueSpec.Values {
					literal, ok := value.(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						continue
					}
					text, err := strconv.Unquote(literal.Value)
					if err != nil {
						t.Fatalf("decode operation ID %s: %v", literal.Value, err)
					}
					id := ID(text)
					if result[id] {
						t.Fatalf("duplicate declared operation ID %s", id)
					}
					result[id] = true
				}
			}
		}
	}
	return result
}

func receiverName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		if ident, ok := value.X.(*ast.Ident); ok {
			return ident.Name
		}
	}
	return "unknown"
}
