package capability

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/doctor"
)

const finalOperationSurfaceFingerprint = "45fb48a32ab774d5fe7a1f078751ff91ec88f0b94bc058a89d321725c7899ee9"

func TestFinalOperationSurfaceMatrixFingerprint(t *testing.T) {
	lines := make([]string, 0, len(All())*len(AllSurfaces))
	for _, row := range ParityMatrix() {
		for _, mapping := range row.Surfaces {
			lines = append(lines, fmt.Sprintf("%s|%s|%s|%s", row.Operation, mapping.Surface, mapping.State, mapping.Exemption))
		}
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(lines, "\n"))))
	if fingerprint != finalOperationSurfaceFingerprint {
		t.Fatalf("final operation/surface matrix changed: fingerprint=%s rows=%d; reconcile every required adapter/exemption before updating finalOperationSurfaceFingerprint", fingerprint, len(lines))
	}
}

func TestFinalMatrixUsesExplicitExemptionClasses(t *testing.T) {
	seen := map[SurfaceExemptionClass]bool{}
	for _, row := range ParityMatrix() {
		for _, mapping := range row.Surfaces {
			if mapping.State != SurfaceRequired {
				seen[mapping.Exemption] = true
			}
		}
	}
	for _, class := range []SurfaceExemptionClass{
		SurfaceExemptionLocalOnly,
		SurfaceExemptionAgentOnly,
		SurfaceExemptionProtocolOnly,
		SurfaceExemptionUnsupportedRemote,
		SurfaceExemptionSurfaceSpecific,
	} {
		if !seen[class] {
			t.Fatalf("final matrix does not exercise exemption class %q", class)
		}
	}
}

func TestFinalAdministrativeSurfaceContractsAreExplicit(t *testing.T) {
	for _, spec := range All() {
		if spec.Audience != AudienceOperator && spec.Audience != AudienceReviewer {
			continue
		}
		for _, surface := range []Surface{SurfaceCLI, SurfaceTUI, SurfaceBrowser, SurfaceAdminAPI, SurfaceTelegram} {
			contract, ok := spec.Surface(surface)
			if !ok {
				t.Fatalf("operation %s has no %s contract", spec.ID, surface)
			}
			switch contract.State {
			case SurfaceRequired:
				if contract.Exemption != "" || contract.Reason != "" {
					t.Fatalf("required operation %s/%s carries exemption metadata: %#v", spec.ID, surface, contract)
				}
			case SurfaceExempt:
				if !validSurfaceExemption(contract.Exemption) || !validSurfaceReason(contract.Reason) {
					t.Fatalf("operation %s/%s has unbounded exemption: %#v", spec.ID, surface, contract)
				}
			default:
				t.Fatalf("active administrative surface %s keeps non-final state for %s: %#v", surface, spec.ID, contract)
			}
		}
	}
}

func TestFinalTelemetryProjectionUsesProductOperationsOnly(t *testing.T) {
	for _, id := range []ID{TelemetryStatus, TelemetryEnable, TelemetryDisable} {
		for _, surface := range []Surface{SurfaceCLI, SurfaceTUI, SurfaceBrowser, SurfaceAdminAPI, SurfaceTelegram} {
			assertFinalSurface(t, id, surface, SurfaceRequired, "")
		}
		assertFinalSurface(t, id, SurfaceMCP, SurfaceExempt, SurfaceExemptionSurfaceSpecific)
	}
	for _, surface := range []Surface{SurfaceCLI, SurfaceTUI, SurfaceBrowser, SurfaceAdminAPI} {
		assertFinalSurface(t, TelemetryShow, surface, SurfaceRequired, "")
	}
	assertFinalSurface(t, TelemetryShow, SurfaceTelegram, SurfaceExempt, SurfaceExemptionSurfaceSpecific)

	for _, spec := range All() {
		value := string(spec.ID)
		if strings.HasPrefix(value, "telemetry.") {
			switch spec.ID {
			case TelemetryStatus, TelemetryEnable, TelemetryDisable, TelemetryShow:
			default:
				t.Fatalf("telemetry transport/internal identity leaked into public operation inventory: %s", spec.ID)
			}
		}
	}
}

func TestFinalWorkspaceRelocationIsOneCanonicalOperation(t *testing.T) {
	for _, surface := range []Surface{SurfaceCLI, SurfaceTUI, SurfaceAdminAPI, SurfaceTelegram} {
		assertFinalSurface(t, WorkspaceRelocate, surface, SurfaceRequired, "")
	}
	assertFinalSurface(t, WorkspaceRelocate, SurfaceBrowser, SurfaceExempt, SurfaceExemptionUnsupportedRemote)
	assertFinalSurface(t, WorkspaceRelocate, SurfaceMCP, SurfaceExempt, SurfaceExemptionSurfaceSpecific)

	for _, surface := range []Surface{SurfaceCLI, SurfaceTUI, SurfaceBrowser, SurfaceAdminAPI, SurfaceMCP, SurfaceTelegram} {
		if surface == SurfaceMCP {
			assertFinalSurface(t, WorkspaceRegister, surface, SurfaceRequired, "")
			continue
		}
		assertFinalSurface(t, WorkspaceRegister, surface, SurfaceRequired, "")
	}
	for _, spec := range All() {
		value := strings.ToLower(string(spec.ID))
		if strings.Contains(value, "workspace.rebind") || strings.Contains(value, "workspace.duplicate") || strings.Contains(value, "workspace.conflict.resolve") {
			t.Fatalf("workspace relocation outcome escaped into a second canonical operation: %s", spec.ID)
		}
	}
}

func TestFinalNativeRuleAndSkillAuthoringStayAgentOwned(t *testing.T) {
	for _, id := range []ID{InstructionRuleCreate, InstructionSkillCreate} {
		spec, ok := Lookup(id)
		if !ok {
			t.Fatalf("authoring operation %s missing", id)
		}
		if spec.Audience != AudienceAgent || spec.Authorization != AuthorizationAgent {
			t.Fatalf("authoring operation %s ownership=%s/%s", id, spec.Audience, spec.Authorization)
		}
		assertFinalSurface(t, id, SurfaceMCP, SurfaceRequired, "")
		for _, surface := range []Surface{SurfaceCLI, SurfaceTUI, SurfaceBrowser, SurfaceAdminAPI, SurfaceTelegram} {
			assertFinalSurface(t, id, surface, SurfaceExempt, SurfaceExemptionAgentOnly)
		}
	}

	for _, id := range []ID{SkillList, SkillLoad, RulesLoadPath} {
		spec, ok := Lookup(id)
		if !ok {
			t.Fatalf("provider-native instruction source operation %s missing", id)
		}
		if !spec.Effects.ReadOnly || spec.Audience != AudienceAgent {
			t.Fatalf("provider-native instruction source %s is not read-only agent-owned: %#v", id, spec)
		}
		assertFinalSurface(t, id, SurfaceMCP, SurfaceRequired, "")
		for _, surface := range []Surface{SurfaceCLI, SurfaceTUI, SurfaceBrowser, SurfaceAdminAPI, SurfaceTelegram} {
			assertFinalSurface(t, id, surface, SurfaceExempt, SurfaceExemptionAgentOnly)
		}
	}
}

func TestFinalDoctorOwnsCheckpointAndArchiveHealth(t *testing.T) {
	for _, surface := range []Surface{SurfaceCLI, SurfaceTUI, SurfaceBrowser, SurfaceAdminAPI, SurfaceTelegram} {
		assertFinalSurface(t, DoctorRead, surface, SurfaceRequired, "")
	}
	assertFinalSurface(t, DoctorRead, SurfaceMCP, SurfaceExempt, SurfaceExemptionSurfaceSpecific)

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

func TestFinalTunnelMatrixKeepsSingleRuntimeLifecycle(t *testing.T) {
	for _, surface := range []Surface{SurfaceCLI, SurfaceTUI, SurfaceBrowser, SurfaceAdminAPI, SurfaceTelegram} {
		assertFinalSurface(t, TunnelStatus, surface, SurfaceRequired, "")
	}
	assertFinalSurface(t, TunnelStatus, SurfaceMCP, SurfaceExempt, SurfaceExemptionSurfaceSpecific)

	for _, id := range []ID{TunnelList, TunnelGet, TunnelUse, TunnelCreate, TunnelUpdate, TunnelDelete} {
		assertFinalSurface(t, id, SurfaceCLI, SurfaceRequired, "")
		assertFinalSurface(t, id, SurfaceBrowser, SurfaceRequired, "")
		assertFinalSurface(t, id, SurfaceAdminAPI, SurfaceRequired, "")
		assertFinalSurface(t, id, SurfaceTUI, SurfaceExempt, SurfaceExemptionSurfaceSpecific)
		assertFinalSurface(t, id, SurfaceTelegram, SurfaceExempt, SurfaceExemptionUnsupportedRemote)
	}
}

func TestFinalBrowserAdminOnlyDifferencesAreBounded(t *testing.T) {
	for _, id := range []ID{WorkspaceRelocate, RequestGrantList, RequestGrantRevoke, NotificationStatus} {
		spec, ok := Lookup(id)
		if !ok {
			t.Fatalf("operation %s missing", id)
		}
		admin, _ := spec.Surface(SurfaceAdminAPI)
		browser, _ := spec.Surface(SurfaceBrowser)
		if admin.State != SurfaceRequired || browser.State != SurfaceExempt || browserExemptionReason(id) == "" || browser.Reason != browserExemptionReason(id) {
			t.Fatalf("Browser/Admin difference for %s is not explicitly bounded: admin=%#v browser=%#v", id, admin, browser)
		}
	}
	assertFinalSurface(t, OAuthCallbackComplete, SurfaceAdminAPI, SurfaceRequired, "")
	assertFinalSurface(t, OAuthCallbackComplete, SurfaceBrowser, SurfaceExempt, SurfaceExemptionProtocolOnly)
}

func TestFinalProtocolProfilesDoNotCreateOperations(t *testing.T) {
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

func assertFinalSurface(t *testing.T, id ID, surface Surface, state SurfaceState, exemption SurfaceExemptionClass) {
	t.Helper()
	spec, ok := Lookup(id)
	if !ok {
		t.Fatalf("operation %s missing", id)
	}
	contract, ok := spec.Surface(surface)
	if !ok {
		t.Fatalf("operation %s has no %s contract", id, surface)
	}
	if contract.State != state || contract.Exemption != exemption {
		t.Fatalf("operation %s/%s contract=%#v want state=%s exemption=%s", id, surface, contract, state, exemption)
	}
	if state == SurfaceRequired && contract.Reason != "" {
		t.Fatalf("required operation %s/%s carries reason %q", id, surface, contract.Reason)
	}
	if state != SurfaceRequired && !validSurfaceReason(contract.Reason) {
		t.Fatalf("operation %s/%s has unbounded reason %q", id, surface, contract.Reason)
	}
}
