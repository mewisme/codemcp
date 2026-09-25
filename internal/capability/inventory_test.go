package capability

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestInventoryIsMachineReadableAndComplete(t *testing.T) {
	snapshot := Inventory()
	if snapshot.Version != InventoryVersion {
		t.Fatalf("version=%d want=%d", snapshot.Version, InventoryVersion)
	}
	if len(snapshot.Operations) != len(All()) {
		t.Fatalf("operations=%d want=%d", len(snapshot.Operations), len(All()))
	}
	if len(snapshot.Surfaces) != len(AllSurfaces) {
		t.Fatalf("surfaces=%d want=%d", len(snapshot.Surfaces), len(AllSurfaces))
	}
	raw, err := InventoryJSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded InventorySnapshot
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, snapshot) {
		t.Fatalf("inventory JSON round-trip drifted")
	}
	for index := 1; index < len(snapshot.Operations); index++ {
		if snapshot.Operations[index-1].ID > snapshot.Operations[index].ID {
			t.Fatalf("inventory is not sorted: %s before %s", snapshot.Operations[index-1].ID, snapshot.Operations[index].ID)
		}
	}
}

func TestSurfaceLifecyclePreventsPlannedStateOnActiveInterfaces(t *testing.T) {
	seen := map[Surface]bool{}
	for _, lifecycle := range SurfaceLifecycles() {
		if seen[lifecycle.Surface] {
			t.Fatalf("duplicate lifecycle for %s", lifecycle.Surface)
		}
		seen[lifecycle.Surface] = true
	}
	for _, surface := range AllSurfaces {
		if !seen[surface] {
			t.Fatalf("surface %s has no lifecycle declaration", surface)
		}
	}
	for _, spec := range All() {
		for _, contract := range spec.Surfaces {
			if contract.State == SurfacePlanned && SurfaceActive(contract.Surface) {
				t.Fatalf("operation %s keeps planned state on active surface %s", spec.ID, contract.Surface)
			}
			if contract.State == SurfaceRequired && !SurfaceActive(contract.Surface) {
				t.Fatalf("operation %s requires inactive surface %s", spec.ID, contract.Surface)
			}
			if contract.State != SurfaceRequired && !validSurfaceReason(contract.Reason) {
				t.Fatalf("operation %s surface %s has unbounded reason %q", spec.ID, contract.Surface, contract.Reason)
			}
		}
	}
}

func TestRequiredDeclaredMappingsHaveEntryPoints(t *testing.T) {
	for _, row := range ParityMatrix() {
		for _, mapping := range row.Surfaces {
			if mapping.State != SurfaceRequired {
				continue
			}
			if len(mapping.EntryPoints) == 0 {
				t.Fatalf("required operation %s lacks declared adapter entry point for %s", row.Operation, mapping.Surface)
			}
			for _, entry := range mapping.EntryPoints {
				if strings.TrimSpace(entry) == "" {
					t.Fatalf("operation %s surface %s has empty entry point", row.Operation, mapping.Surface)
				}
			}
		}
	}
}

func TestRequiredOperationsAreStableAndSorted(t *testing.T) {
	for _, surface := range AllSurfaces {
		got := RequiredOperations(surface)
		want := append([]ID(nil), got...)
		sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("required operations for %s are not sorted", surface)
		}
		for _, id := range got {
			spec, ok := Lookup(id)
			if !ok {
				t.Fatalf("unknown required operation %s", id)
			}
			contract, ok := spec.Surface(surface)
			if !ok || contract.State != SurfaceRequired {
				t.Fatalf("required list drift for %s/%s: %#v", id, surface, contract)
			}
		}
	}
}

func TestSecurityPolicyIsOwnedByCanonicalOperationAcrossBindings(t *testing.T) {
	for _, spec := range All() {
		want, ok := SecurityPolicyFor(spec.ID)
		if !ok {
			t.Fatalf("missing policy for %s", spec.ID)
		}
		assert := func(binding string, id ID, ok bool) {
			t.Helper()
			if !ok || id != spec.ID {
				t.Fatalf("%s resolves to %s,%t want %s,true", binding, id, ok, spec.ID)
			}
			got, found := SecurityPolicyFor(id)
			if !found || got != want {
				t.Fatalf("%s policy=%#v,%t want=%#v", binding, got, found, want)
			}
		}
		for _, path := range spec.CLIPaths() {
			id, found := ForPath(path)
			assert("CLI "+path, id, found)
		}
		for _, binding := range spec.Admin {
			id, found := ForAdmin(binding.Method, binding.Path)
			assert("Admin "+binding.Method+" "+binding.Path, id, found)
		}
		for _, tool := range spec.MCPTools {
			id, found := ForMCPTool(tool)
			assert("MCP "+tool, id, found)
		}
	}
}

func TestProtocolProfileProjectionCannotChangeCanonicalPolicy(t *testing.T) {
	baseMetadata := map[string]any{"title": "Base title", "compatibility": "portable"}
	openAIMetadata := map[string]any{"title": "OpenAI title", "compatibility": "openai"}
	base, ok := ProjectProtocol(ShellRun, "base", baseMetadata)
	if !ok {
		t.Fatal("base projection missing")
	}
	openai, ok := ProjectProtocol(ShellRun, "openai", openAIMetadata)
	if !ok {
		t.Fatal("openai projection missing")
	}
	if reflect.DeepEqual(base.Metadata, openai.Metadata) {
		t.Fatal("host profile metadata unexpectedly identical")
	}
	if base.Operation != openai.Operation || base.Effects != openai.Effects || base.Authorization != openai.Authorization || base.Confirmation != openai.Confirmation {
		t.Fatalf("host projection changed canonical contract: base=%#v openai=%#v", base, openai)
	}
	baseMetadata["title"] = "mutated"
	if base.Metadata["title"] != "Base title" {
		t.Fatal("projection retained caller metadata alias")
	}
}

func TestParityMatrixAllowsPresentationDifferencesButSharesCanonicalEffects(t *testing.T) {
	var workspace *ParityRow
	rows := ParityMatrix()
	for index := range rows {
		row := rows[index]
		if row.Operation == WorkspaceList {
			workspace = &row
			break
		}
	}
	if workspace == nil {
		t.Fatal("workspace.list parity row missing")
	}
	spec, _ := Lookup(WorkspaceList)
	if workspace.Effects != spec.Effects || workspace.Authorization != spec.Authorization || workspace.Confirmation != spec.Confirmation {
		t.Fatalf("parity row drifted from canonical contract: %#v", workspace)
	}
	entryPoints := map[string]bool{}
	for _, mapping := range workspace.Surfaces {
		if mapping.State == SurfaceRequired {
			for _, entry := range mapping.EntryPoints {
				entryPoints[entry] = true
			}
		}
	}
	if len(entryPoints) < 2 {
		t.Fatalf("representative operation does not preserve presentation differences: %#v", workspace.Surfaces)
	}
}

func TestParityMatrixCannotOverrideCanonicalSecurityPolicy(t *testing.T) {
	for _, row := range ParityMatrix() {
		policy, ok := SecurityPolicyFor(row.Operation)
		if !ok {
			t.Fatalf("missing canonical security policy for %s", row.Operation)
		}
		if row.Authorization != policy.Authorization || row.Risk != policy.Risk ||
			row.Effects.Destructive != policy.Destructive || row.Confirmation != policy.Confirmation {
			t.Fatalf("parity row %s overrides canonical security policy: row=%#v policy=%#v", row.Operation, row, policy)
		}
		for _, mapping := range row.Surfaces {
			if mapping.State == SurfaceRequired && len(mapping.EntryPoints) == 0 {
				t.Fatalf("required mapping %s/%s is missing while policy is canonical", row.Operation, mapping.Surface)
			}
		}
	}
}

func TestDiagnosticsSummarizesInventoryWithoutOperationPayloads(t *testing.T) {
	diagnostics := Diagnostics()
	if diagnostics.Version != InventoryVersion || diagnostics.Operations != len(All()) {
		t.Fatalf("diagnostics=%#v", diagnostics)
	}
	for _, lifecycle := range SurfaceLifecycles() {
		counts, ok := diagnostics.SurfaceSummary[lifecycle.Surface]
		if !ok || counts.Active != lifecycle.Active {
			t.Fatalf("surface diagnostics missing/drifted for %s: %#v", lifecycle.Surface, counts)
		}
		if counts.Required+counts.Planned+counts.Exempt != len(All()) {
			t.Fatalf("surface %s counts=%#v operations=%d", lifecycle.Surface, counts, len(All()))
		}
	}
}

func TestApprovalReviewAndNotificationCapabilitiesAreDeclared(t *testing.T) {
	for _, id := range []ID{RequestList, RequestView, RequestApprove, RequestDeny, RequestStream, RequestGrantList, RequestGrantRevoke, NotificationStatus} {
		spec, ok := Lookup(id)
		if !ok {
			t.Fatalf("approval capability %s missing from inventory", id)
		}
		if spec.Audience != AudienceOperator && spec.Audience != AudienceReviewer {
			t.Fatalf("approval capability %s audience=%s", id, spec.Audience)
		}
	}
	for _, id := range []ID{RequestList, RequestView, RequestApprove, RequestDeny} {
		spec, _ := Lookup(id)
		for _, surface := range []Surface{SurfaceCLI, SurfaceTUI, SurfaceBrowser, SurfaceAdminAPI} {
			contract, ok := spec.Surface(surface)
			if !ok || contract.State != SurfaceRequired {
				t.Fatalf("approval review %s surface %s contract=%#v", id, surface, contract)
			}
		}
	}
	status, _ := Lookup(NotificationStatus)
	admin, ok := status.Surface(SurfaceAdminAPI)
	if !ok || admin.State != SurfaceRequired {
		t.Fatalf("notification status Admin API contract=%#v", admin)
	}
}
