package capability

import (
	"strings"
	"testing"
)

func TestCatalogContractsAreCompleteAndUnique(t *testing.T) {
	ids := map[ID]bool{}
	paths := map[string]ID{}
	effects := map[string]ID{}
	admin := map[AdminBinding]ID{}
	tools := map[string]ID{}

	for _, spec := range All() {
		if strings.TrimSpace(string(spec.ID)) == "" {
			t.Fatal("operation id is empty")
		}
		if ids[spec.ID] {
			t.Fatalf("duplicate operation id: %s", spec.ID)
		}
		ids[spec.ID] = true

		if spec.Kind == "" || spec.Audience == "" || spec.Authorization == "" || spec.Risk == "" || spec.Confirmation.Mode == "" {
			t.Fatalf("operation %s has incomplete metadata: %#v", spec.ID, spec)
		}
		if strings.TrimSpace(spec.Effects.Key) == "" {
			t.Fatalf("operation %s has no semantic effect key", spec.ID)
		}
		if previous, ok := effects[spec.Effects.Key]; ok && previous != spec.ID {
			t.Fatalf("semantic effect %q is owned by both %s and %s", spec.Effects.Key, previous, spec.ID)
		}
		effects[spec.Effects.Key] = spec.ID
		if spec.Effects.ReadOnly && spec.Effects.Destructive {
			t.Fatalf("operation %s is both read-only and destructive", spec.ID)
		}
		if spec.Effects.Destructive && spec.Risk != RiskDestructive {
			t.Fatalf("operation %s marks destructive effects with risk %q", spec.ID, spec.Risk)
		}

		for _, path := range spec.CLIPaths() {
			path = NormalizePath(path)
			if path == "" {
				t.Fatalf("operation %s has an empty CLI path", spec.ID)
			}
			if previous, ok := paths[path]; ok && previous != spec.ID {
				t.Fatalf("CLI path %q maps to both %s and %s", path, previous, spec.ID)
			}
			paths[path] = spec.ID
			if got, ok := ForPath(path); !ok || got != spec.ID {
				t.Fatalf("ForPath(%q)=%q,%t want %q", path, got, ok, spec.ID)
			}
		}

		for _, binding := range spec.Admin {
			binding = normalizeAdminBinding(binding)
			if binding.Method == "" || binding.Path == "/" {
				t.Fatalf("operation %s has invalid Admin binding %#v", spec.ID, binding)
			}
			if previous, ok := admin[binding]; ok && previous != spec.ID {
				t.Fatalf("Admin binding %#v maps to both %s and %s", binding, previous, spec.ID)
			}
			admin[binding] = spec.ID
			if got, ok := ForAdmin(binding.Method, binding.Path); !ok || got != spec.ID {
				t.Fatalf("ForAdmin(%q,%q)=%q,%t want %q", binding.Method, binding.Path, got, ok, spec.ID)
			}
		}

		for _, tool := range spec.MCPTools {
			tool = strings.TrimSpace(tool)
			if tool == "" {
				t.Fatalf("operation %s has empty MCP tool binding", spec.ID)
			}
			if previous, ok := tools[tool]; ok && previous != spec.ID {
				t.Fatalf("MCP tool %q maps to both %s and %s", tool, previous, spec.ID)
			}
			tools[tool] = spec.ID
			if got, ok := ForMCPTool(tool); !ok || got != spec.ID {
				t.Fatalf("ForMCPTool(%q)=%q,%t want %q", tool, got, ok, spec.ID)
			}
		}
		for _, tool := range spec.PlannedMCPTools {
			tool = strings.TrimSpace(tool)
			if tool == "" {
				t.Fatalf("operation %s has empty planned MCP tool binding", spec.ID)
			}
			if previous, ok := tools[tool]; ok && previous != spec.ID {
				t.Fatalf("MCP tool %q maps to both %s and %s", tool, previous, spec.ID)
			}
			tools[tool] = spec.ID
			if got, ok := ForPlannedMCPTool(tool); !ok || got != spec.ID {
				t.Fatalf("ForPlannedMCPTool(%q)=%q,%t want %q", tool, got, ok, spec.ID)
			}
			if got, ok := ForMCPTool(tool); ok {
				t.Fatalf("planned MCP tool %q became active via %s", tool, got)
			}
		}

		assertSurfaceContractComplete(t, spec)
	}
}

func TestAgentAuthoringOperationsAreAgentMCPOnly(t *testing.T) {
	tests := []struct {
		id   ID
		tool string
	}{
		{id: InstructionRuleCreate, tool: "create_rule"},
		{id: InstructionSkillCreate, tool: "create_skill"},
		{id: PlanCreate, tool: "create_plan"},
	}
	for _, test := range tests {
		mapped, ok := ForMCPTool(test.tool)
		if !ok || mapped != test.id {
			t.Fatalf("ForMCPTool(%q)=(%q,%t), want %q", test.tool, mapped, ok, test.id)
		}
		spec, ok := Lookup(test.id)
		if !ok {
			t.Fatalf("missing operation %q", test.id)
		}
		if spec.Audience != AudienceAgent || spec.Kind != KindMutation {
			t.Fatalf("operation %q contract=%#v", test.id, spec)
		}
		if spec.Authorization != AuthorizationAgent || spec.Risk != RiskState || spec.Effects.OpenWorld || spec.Effects.ReadOnly {
			t.Fatalf("operation %q semantic contract=%#v", test.id, spec)
		}
		if len(spec.MCPTools) == 0 || spec.MCPTools[0] != test.tool {
			t.Fatalf("operation %q MCP tools=%v", test.id, spec.MCPTools)
		}
		for _, surface := range ProductSurfaces {
			contract, ok := spec.Surface(surface)
			if !ok || contract.State != SurfaceExempt || contract.Exemption != SurfaceExemptionProtocolOnly {
				t.Fatalf("operation %q surface %q=%#v", test.id, surface, contract)
			}
		}
	}
}

func assertSurfaceContractComplete(t *testing.T, spec Spec) {
	t.Helper()
	seen := map[Surface]bool{}
	for _, contract := range spec.Surfaces {
		if seen[contract.Surface] {
			t.Fatalf("operation %s repeats surface %s", spec.ID, contract.Surface)
		}
		seen[contract.Surface] = true
		switch contract.State {
		case SurfaceRequired:
			if !SurfaceActive(contract.Surface) {
				t.Fatalf("operation %s requires inactive surface %s", spec.ID, contract.Surface)
			}
			if strings.TrimSpace(contract.Reason) != "" {
				t.Fatalf("operation %s required surface %s has exemption reason %q", spec.ID, contract.Surface, contract.Reason)
			}
		case SurfacePlanned:
			t.Fatalf("operation %s keeps planned product-surface state for %s", spec.ID, contract.Surface)
		case SurfaceExempt:
			if !validSurfaceExemption(contract.Exemption) || !validSurfaceReason(contract.Reason) ||
				!validSurfaceExemptionGuard(contract.Guard) || !exemptionGuardMatches(spec, contract) {
				t.Fatalf("operation %s exempt surface %s has invalid typed metadata: %#v", spec.ID, contract.Surface, contract)
			}
		default:
			t.Fatalf("operation %s has invalid surface state %q for %s", spec.ID, contract.State, contract.Surface)
		}
	}
	for _, surface := range AllSurfaces {
		if !seen[surface] {
			t.Fatalf("operation %s does not classify surface %s", spec.ID, surface)
		}
	}
}

func TestAgentAndProtocolOnlyOperationsAreExplicit(t *testing.T) {
	var agents, protocols int
	for _, spec := range All() {
		switch spec.Audience {
		case AudienceAgent:
			agents++
			if len(spec.MCPTools)+len(spec.PlannedMCPTools) == 0 || spec.HasCLI() || len(spec.Admin) != 0 {
				t.Fatalf("agent-only operation has non-agent binding: %#v", spec)
			}
		case AudienceProtocol:
			protocols++
			if spec.HasCLI() || len(spec.MCPTools) != 0 || len(spec.PlannedMCPTools) != 0 {
				t.Fatalf("protocol-only operation has human/agent binding: %#v", spec)
			}
		}
	}
	if agents == 0 || protocols == 0 {
		t.Fatalf("expected explicit agent/protocol operations, got agents=%d protocols=%d", agents, protocols)
	}
}

func TestLookupReturnsDetachedSpec(t *testing.T) {
	spec, ok := Lookup(WorkspaceList)
	if !ok {
		t.Fatal("workspace.list missing")
	}
	spec.CLI.Aliases = append(spec.CLI.Aliases, "mutated")
	spec.Admin = append(spec.Admin, AdminBinding{Method: "GET", Path: "/mutated"})
	spec.MCPTools = append(spec.MCPTools, "mutated")
	spec.PlannedMCPTools = append(spec.PlannedMCPTools, "planned-mutated")
	spec.Surfaces[0].State = SurfacePlanned

	again, _ := Lookup(WorkspaceList)
	if len(again.CLI.Aliases) != len(spec.CLI.Aliases)-1 || len(again.Admin) != len(spec.Admin)-1 || len(again.MCPTools) != len(spec.MCPTools)-1 || len(again.PlannedMCPTools) != len(spec.PlannedMCPTools)-1 || again.Surfaces[0].State == SurfacePlanned {
		t.Fatal("Lookup returned mutable catalog backing data")
	}
}

func TestCatalogAdminRequestLookupMatchesConcreteResourcePaths(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   ID
	}{
		{"GET", "/api/workspaces/ws_123", WorkspaceShow},
		{"POST", "/api/workspaces/ws_123/relocate", WorkspaceRelocate},
		{"GET", "/api/workspaces/ws_123/executions/exec_456", ExecutionView},
		{"DELETE", "/api/workspace-containers/wsc_123/workspaces", WorkspaceContainerRemove},
		{"GET", "/api/upstream/server_a/auth/status", UpstreamAuthStatus},
		{"PUT", "/api/tunnel/managed/tun_123", TunnelUpdate},
		{"GET", "/oauth/callback/server_a", OAuthCallbackComplete},
	}
	for _, tc := range cases {
		got, ok := ForAdminRequest(tc.method, tc.path)
		if !ok || got != tc.want {
			t.Errorf("%s %s = %q, %t; want %q, true", tc.method, tc.path, got, ok, tc.want)
		}
	}
	if _, ok := ForAdminRequest("POST", "/api/health"); ok {
		t.Fatal("unsupported method unexpectedly resolved")
	}
	if _, ok := ForAdminRequest("GET", "/api/workspaces/ws_123/unknown"); ok {
		t.Fatal("unknown concrete route unexpectedly resolved")
	}
}

func TestFirstPartyIntegrationsHaveCanonicalCapabilities(t *testing.T) {
	groups := map[string][]ID{
		"ponytail": {IntegrationPonytailTurn},
		"caveman":  {IntegrationCavemanTurn},
		"fanout":   {IntegrationFanoutTurn},
		"rtk": {
			IntegrationRTKStatus,
			IntegrationRTKEnable,
			IntegrationRTKDisable,
			IntegrationRTKProbe,
			IntegrationRTKInstall,
		},
		"codegraph": {
			IntegrationCodeGraphStatus,
			IntegrationCodeGraphProbe,
			IntegrationCodeGraphInstall,
			IntegrationCodeGraphWorkspaceStatus,
			IntegrationCodeGraphWorkspaceInit,
			IntegrationCodeGraphWorkspaceSync,
			IntegrationCodeGraphExplore,
		},
		"typesafe": {
			IntegrationTypeSafeStatus,
			IntegrationTypeSafeEnable,
			IntegrationTypeSafeDisable,
			IntegrationTypeSafeProbe,
		},
		"browser": {
			IntegrationBrowserStatus,
			IntegrationBrowserDoctor,
		},
		"chatgpt-web": {
			IntegrationChatGPTWebStatus,
			IntegrationChatGPTWebLogin,
			IntegrationChatGPTWebLogout,
			IntegrationChatGPTWebDoctor,
		},
	}
	for integration, ids := range groups {
		if len(ids) == 0 {
			t.Fatalf("integration %s has no canonical capability", integration)
		}
		prefix := "integration." + integration + "."
		for _, id := range ids {
			if !strings.HasPrefix(string(id), prefix) {
				t.Fatalf("integration %s capability %q does not use prefix %q", integration, id, prefix)
			}
			if _, ok := Lookup(id); !ok {
				t.Fatalf("integration %s capability %q is missing from catalog", integration, id)
			}
		}
	}
}

func TestUpstreamOperationsUseOneCanonicalIdentity(t *testing.T) {
	want := []ID{
		UpstreamServerList,
		UpstreamServerAdd,
		UpstreamServerConfigure,
		UpstreamServerShow,
		UpstreamServerRemove,
		UpstreamServerEnable,
		UpstreamServerDisable,
		UpstreamServerStatus,
		UpstreamServerTools,
		UpstreamAuthLogin,
		UpstreamAuthStatus,
		UpstreamAuthLogout,
		UpstreamCall,
	}
	seen := map[ID]bool{}
	for _, id := range want {
		if !strings.HasPrefix(string(id), "upstream.") {
			t.Fatalf("upstream operation has non-upstream identity: %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate upstream operation identity: %q", id)
		}
		seen[id] = true
		if _, ok := Lookup(id); !ok {
			t.Fatalf("upstream operation missing from catalog: %q", id)
		}
	}
	for _, spec := range All() {
		if strings.HasPrefix(string(spec.ID), "mcp.server.") || strings.HasPrefix(string(spec.ID), "mcp.auth.") {
			t.Fatalf("legacy managed-upstream operation identity remains: %q", spec.ID)
		}
		for _, path := range spec.CLIPaths() {
			if strings.HasPrefix(NormalizePath(path), "mcp server") {
				t.Fatalf("legacy managed-upstream CLI path remains on %q: %q", spec.ID, path)
			}
		}
	}
}
