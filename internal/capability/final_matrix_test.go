package capability

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/doctor"
)

func TestStrictProductContractDefaultsHumanOperationsRequired(t *testing.T) {
	for _, spec := range All() {
		if len(spec.Surfaces) != len(ProductSurfaces) {
			t.Fatalf("operation %s product contracts=%d want=%d", spec.ID, len(spec.Surfaces), len(ProductSurfaces))
		}
		seen := map[Surface]bool{}
		for _, contract := range spec.Surfaces {
			if seen[contract.Surface] {
				t.Fatalf("operation %s duplicates product surface %s", spec.ID, contract.Surface)
			}
			seen[contract.Surface] = true
			if spec.Audience != AudienceOperator && spec.Audience != AudienceReviewer {
				if contract.State != SurfaceExempt || contract.Exemption != SurfaceExemptionProtocolOnly {
					t.Fatalf("non-human operation %s/%s contract=%#v", spec.ID, contract.Surface, contract)
				}
				continue
			}
			switch contract.State {
			case SurfaceRequired:
				if contract.Exemption != "" || contract.Reason != "" || contract.ExemptionOwner != "" || contract.Guard != "" || contract.SafeAlternative != "" {
					t.Fatalf("required operation %s/%s carries exemption metadata: %#v", spec.ID, contract.Surface, contract)
				}
			case SurfaceExempt:
				assertValidExemption(t, spec, contract)
			default:
				t.Fatalf("human operation %s/%s has non-final state %q", spec.ID, contract.Surface, contract.State)
			}
		}
		for _, surface := range ProductSurfaces {
			if !seen[surface] {
				t.Fatalf("operation %s omitted product surface %s", spec.ID, surface)
			}
		}
	}
}

func TestNewHumanOperationDefaultsToAllProductSurfacesRequired(t *testing.T) {
	spec := Spec{
		ID: "test.synthetic.operator", Kind: KindQuery, Audience: AudienceOperator, Authorization: AuthorizationOperator,
		Risk: RiskNone, Confirmation: ConfirmationPolicy{Mode: ConfirmationNone},
		Effects: SemanticEffects{Key: "test.synthetic.operator", ReadOnly: true, Idempotent: true},
	}
	contracts := surfaceContracts(spec)
	if len(contracts) != len(ProductSurfaces) {
		t.Fatalf("contracts=%d want=%d", len(contracts), len(ProductSurfaces))
	}
	for _, contract := range contracts {
		if contract.State != SurfaceRequired || contract.Exemption != "" || contract.Reason != "" {
			t.Fatalf("new operator operation did not default required on %s: %#v", contract.Surface, contract)
		}
	}
}

func TestStrictExemptionsUseOnlyBoundedExecutableClasses(t *testing.T) {
	for _, spec := range All() {
		for _, contract := range spec.Surfaces {
			if contract.State != SurfaceExempt {
				continue
			}
			assertValidExemption(t, spec, contract)
		}
	}
}

func TestFreeFormOrImplementationAbsenceCannotBecomeProductExemption(t *testing.T) {
	spec := Spec{
		ID: "test.synthetic.operator", Kind: KindQuery, Audience: AudienceOperator, Authorization: AuthorizationOperator,
		Risk: RiskNone, Confirmation: ConfirmationPolicy{Mode: ConfirmationNone},
		Effects: SemanticEffects{Key: "test.synthetic.operator", ReadOnly: true, Idempotent: true},
	}
	contract := SurfaceContract{
		Surface:        SurfaceBrowser,
		State:          SurfaceExempt,
		Exemption:      SurfaceExemptionClass("surface-specific"),
		Reason:         "not exposed by design",
		ExemptionOwner: productSurfacePolicyOwner,
	}
	if validSurfaceExemption(contract.Exemption) || validSurfaceReason(contract.Reason) || exemptionGuardMatches(spec, contract) {
		t.Fatalf("free-form implementation exemption unexpectedly passed strict validation: %#v", contract)
	}
}

func TestHumanExemptionsHaveSafeAlternatives(t *testing.T) {
	for _, spec := range All() {
		if spec.Audience != AudienceOperator && spec.Audience != AudienceReviewer {
			continue
		}
		for _, contract := range spec.Surfaces {
			if contract.State != SurfaceExempt {
				continue
			}
			if strings.TrimSpace(contract.SafeAlternative) == "" {
				t.Fatalf("human exemption %s/%s lacks safe alternative: %#v", spec.ID, contract.Surface, contract)
			}
		}
	}
}

func TestProductParityReportIsDeterministicCompleteAndSorted(t *testing.T) {
	report := ProductParityReportSnapshot()
	if report.Version != ProductParityReportVersion {
		t.Fatalf("version=%d", report.Version)
	}
	if !reflect.DeepEqual(report.Surfaces, ProductSurfaces) {
		t.Fatalf("surfaces=%v want=%v", report.Surfaces, ProductSurfaces)
	}
	if reportHasUnclassifiedSurface(report) {
		t.Fatal("product parity report contains unclassified operation/surface state")
	}
	wantCount := 0
	for _, spec := range All() {
		if spec.Audience == AudienceOperator || spec.Audience == AudienceReviewer {
			wantCount++
		}
	}
	if len(report.Operations) != wantCount {
		t.Fatalf("report operations=%d want human inventory=%d", len(report.Operations), wantCount)
	}
	for index, row := range report.Operations {
		if row.Audience != AudienceOperator && row.Audience != AudienceReviewer {
			t.Fatalf("non-human operation leaked into report: %s/%s", row.Operation, row.Audience)
		}
		if strings.TrimSpace(row.CanonicalOwner) == "" {
			t.Fatalf("operation %s lacks canonical owner", row.Operation)
		}
		if index > 0 && report.Operations[index-1].Operation > row.Operation {
			t.Fatalf("report is not sorted at %s", row.Operation)
		}
	}
	raw1, err := ProductParityReportJSON()
	if err != nil {
		t.Fatal(err)
	}
	raw2, err := ProductParityReportJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(raw1) != string(raw2) {
		t.Fatal("product parity report JSON is not deterministic")
	}
	var decoded ProductParityReport
	if err := json.Unmarshal(raw1, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, report) {
		t.Fatal("product parity report JSON round-trip drifted")
	}
}

func TestProductParityReportKeepsMissingAdaptersAsRequiredGaps(t *testing.T) {
	report := ProductParityReportSnapshot()

	browser, ok := parityMapping(report, WorkspaceRelocate, SurfaceBrowser)
	if !ok || browser.State != SurfaceRequired || browser.Reachable || browser.Gap == "" {
		t.Fatalf("workspace relocate Browser mapping=%#v ok=%t", browser, ok)
	}
	spec, _ := Lookup(WorkspaceRelocate)
	if len(spec.Admin) == 0 {
		t.Fatal("workspace relocate lost Admin API transport binding")
	}
	if browser.Reachable {
		t.Fatal("Admin API transport binding incorrectly satisfied Browser product reachability")
	}

	telegram, ok := parityMapping(report, ProcessList, SurfaceTelegram)
	if !ok || telegram.State != SurfaceRequired || telegram.Reachable || telegram.Gap == "" {
		t.Fatalf("process list Telegram mapping=%#v ok=%t", telegram, ok)
	}
}

func TestProductContractsDoNotTreatAdminAPIOrMCPAsProductSurfaces(t *testing.T) {
	for _, spec := range All() {
		if _, ok := spec.Surface(SurfaceAdminAPI); ok {
			t.Fatalf("operation %s has Admin API product-surface contract", spec.ID)
		}
		if _, ok := spec.Surface(SurfaceMCP); ok {
			t.Fatalf("operation %s has MCP product-surface contract", spec.ID)
		}
	}
}

func TestCanonicalOwnerMetadataMatchesMutationAuthority(t *testing.T) {
	for _, spec := range All() {
		if spec.Audience != AudienceOperator && spec.Audience != AudienceReviewer {
			continue
		}
		owner, ok := CanonicalOwnerFor(spec.ID)
		if !ok || strings.TrimSpace(owner) == "" {
			t.Fatalf("human operation %s lacks canonical owner", spec.ID)
		}
		if ownership, mutation := MutationOwnershipFor(spec.ID); mutation && owner != string(ownership.ValidationOwner) {
			t.Fatalf("operation %s report owner=%q mutation authority=%q", spec.ID, owner, ownership.ValidationOwner)
		}
	}
}

func TestLLMAndApprovalExplainRemainFourSurfaceRequiredBaseline(t *testing.T) {
	ids := []ID{
		LLMStatus, LLMProviderList, LLMProviderGet, LLMProviderAdd, LLMProviderConfigure,
		LLMProviderRemove, LLMProviderSelect, LLMProviderModels, LLMProviderProbe,
		LLMProviderCredentialSet, LLMProviderCredentialClear,
		RequestExplain, RequestExplanationView, RequestExplainStatus,
	}
	for _, id := range ids {
		spec, ok := Lookup(id)
		if !ok {
			t.Fatalf("operation %s missing", id)
		}
		for _, surface := range ProductSurfaces {
			contract, ok := spec.Surface(surface)
			if !ok || contract.State != SurfaceRequired {
				t.Fatalf("operation %s/%s contract=%#v ok=%t", id, surface, contract, ok)
			}
		}
	}
}

func TestAgentInstructionOperationsRemainProtocolOnlyOnProductSurfaces(t *testing.T) {
	for _, id := range []ID{InstructionRuleCreate, InstructionSkillCreate, SkillList, SkillLoad, RulesLoadPath} {
		spec, ok := Lookup(id)
		if !ok {
			t.Fatalf("operation %s missing", id)
		}
		if spec.Audience != AudienceAgent {
			t.Fatalf("operation %s audience=%s", id, spec.Audience)
		}
		if len(spec.MCPTools) == 0 {
			t.Fatalf("operation %s lost MCP tool binding", id)
		}
		for _, surface := range ProductSurfaces {
			contract, ok := spec.Surface(surface)
			if !ok || contract.State != SurfaceExempt || contract.Exemption != SurfaceExemptionProtocolOnly {
				t.Fatalf("operation %s/%s contract=%#v", id, surface, contract)
			}
		}
	}
}

func TestDoctorOwnsCheckpointAndArchiveHealth(t *testing.T) {
	if _, ok := doctor.DefinitionFor(doctor.ComponentCheckpointHistory); !ok {
		t.Fatal("aggregate doctor inventory lost checkpoint/history health")
	}
	for _, spec := range All() {
		value := strings.ToLower(string(spec.ID))
		if strings.HasPrefix(value, "checkpoint.health") || strings.HasPrefix(value, "archive.health") {
			t.Fatalf("aggregate doctor health was split into interface-specific operation %s", spec.ID)
		}
	}
}

func TestProtocolProfilesDoNotCreateOperations(t *testing.T) {
	for _, spec := range All() {
		value := strings.ToLower(string(spec.ID))
		for _, forbidden := range []string{"mcp.base", "mcp.openai", ".profile.", "tunnel.instance", "tunnel.admin.profile", "admin.profile"} {
			if strings.Contains(value, forbidden) {
				t.Fatalf("presentation/profile architecture leaked into canonical operation ID %s", spec.ID)
			}
		}
	}
	base, ok := ProjectProtocol(ShellRun, "base", map[string]any{"profile": "base"})
	if !ok {
		t.Fatal("base protocol projection missing")
	}
	openai, ok := ProjectProtocol(ShellRun, "openai", map[string]any{"profile": "openai"})
	if !ok {
		t.Fatal("openai protocol projection missing")
	}
	if base.Operation != ShellRun || openai.Operation != ShellRun ||
		base.Operation != openai.Operation ||
		base.Effects != openai.Effects ||
		base.Authorization != openai.Authorization ||
		base.Confirmation != openai.Confirmation {
		t.Fatalf("protocol profile changed canonical contract: base=%#v openai=%#v", base, openai)
	}
}

func assertValidExemption(t *testing.T, spec Spec, contract SurfaceContract) {
	t.Helper()
	if !validSurfaceExemption(contract.Exemption) || !validSurfaceReason(contract.Reason) {
		t.Fatalf("operation %s/%s has invalid exemption: %#v", spec.ID, contract.Surface, contract)
	}
	if strings.TrimSpace(contract.ExemptionOwner) == "" || !validSurfaceExemptionGuard(contract.Guard) {
		t.Fatalf("operation %s/%s lacks typed exemption ownership/evidence: %#v", spec.ID, contract.Surface, contract)
	}
	if !exemptionGuardMatches(spec, contract) {
		t.Fatalf("operation %s/%s exemption guard does not prove exemption: %#v", spec.ID, contract.Surface, contract)
	}
}

func TestRequiredOperationsAreDerivedAndSorted(t *testing.T) {
	for _, surface := range ProductSurfaces {
		got := RequiredOperations(surface)
		want := append([]ID(nil), got...)
		sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("required operations for %s are not sorted", surface)
		}
	}
}
