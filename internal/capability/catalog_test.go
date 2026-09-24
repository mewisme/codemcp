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

		assertSurfaceContractComplete(t, spec)
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
			if SurfaceActive(contract.Surface) {
				t.Fatalf("operation %s keeps planned state on active surface %s", spec.ID, contract.Surface)
			}
			if !validSurfaceReason(contract.Reason) {
				t.Fatalf("operation %s planned surface %s has unbounded reason %q", spec.ID, contract.Surface, contract.Reason)
			}
		case SurfaceExempt:
			if !validSurfaceReason(contract.Reason) {
				t.Fatalf("operation %s exempt surface %s has unbounded reason %q", spec.ID, contract.Surface, contract.Reason)
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
			if len(spec.MCPTools) == 0 || spec.HasCLI() || len(spec.Admin) != 0 {
				t.Fatalf("agent-only operation has non-agent binding: %#v", spec)
			}
		case AudienceProtocol:
			protocols++
			if spec.HasCLI() || len(spec.MCPTools) != 0 {
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
	spec.Surfaces[0].State = SurfacePlanned

	again, _ := Lookup(WorkspaceList)
	if len(again.CLI.Aliases) != len(spec.CLI.Aliases)-1 || len(again.Admin) != len(spec.Admin)-1 || len(again.MCPTools) != len(spec.MCPTools)-1 || again.Surfaces[0].State == SurfacePlanned {
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
		{"GET", "/api/upstream/server_a/auth/status", MCPAuthStatus},
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
