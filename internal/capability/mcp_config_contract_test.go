package capability

import "testing"

func TestMCPConfigCapabilitiesKeepOperatorAndAgentAuthoritySeparate(t *testing.T) {
	for _, id := range []ID{ConfigList, ConfigGet, ConfigSet} {
		spec, ok := Lookup(id)
		if !ok {
			t.Fatalf("operator config capability %q missing", id)
		}
		if spec.Audience != AudienceOperator || spec.Authorization != AuthorizationOperator {
			t.Fatalf("operator config capability changed authority: %#v", spec)
		}
		if len(spec.MCPTools) != 0 || len(spec.PlannedMCPTools) != 0 {
			t.Fatalf("operator config capability acquired agent MCP binding: %#v", spec)
		}
	}

	for _, tc := range []struct {
		id   ID
		tool string
	}{
		{AgentConfigList, "config_list"},
		{AgentConfigGet, "config_get"},
	} {
		spec, ok := Lookup(tc.id)
		if !ok {
			t.Fatalf("agent config capability %q missing", tc.id)
		}
		if spec.Audience != AudienceAgent || spec.Authorization != AuthorizationAgent || spec.Kind != KindQuery {
			t.Fatalf("agent config capability metadata=%#v", spec)
		}
		if len(spec.MCPTools) != 1 || spec.MCPTools[0] != tc.tool || len(spec.PlannedMCPTools) != 0 {
			t.Fatalf("agent config binding=%#v", spec)
		}
		if got, active := ForMCPTool(tc.tool); !active || got != tc.id {
			t.Fatalf("active binding %q => %q,%t want %q,true", tc.tool, got, active, tc.id)
		}
		if _, planned := ForPlannedMCPTool(tc.tool); planned {
			t.Fatalf("active config tool %q remained in planned bindings", tc.tool)
		}
	}
	set, ok := Lookup(AgentConfigSet)
	if !ok {
		t.Fatal("agent config set capability missing")
	}
	if set.Audience != AudienceAgent || set.Authorization != AuthorizationAgent || set.Kind != KindMutation {
		t.Fatalf("config_set capability metadata=%#v", set)
	}
	if len(set.MCPTools) != 1 || set.MCPTools[0] != "config_set" || len(set.PlannedMCPTools) != 0 {
		t.Fatalf("config_set binding=%#v", set)
	}
	if got, active := ForMCPTool("config_set"); !active || got != AgentConfigSet {
		t.Fatalf("active config_set binding => %q,%t", got, active)
	}
	if _, planned := ForPlannedMCPTool("config_set"); planned {
		t.Fatal("active config_set binding remained planned")
	}
	if set.Risk != RiskSensitive || set.Confirmation.Mode != ConfirmationRequired || !set.Confirmation.ControlApproval || set.Effects.ReadOnly {
		t.Fatalf("config_set security contract=%#v", set)
	}
	for _, id := range []ID{AgentConfigList, AgentConfigGet} {
		spec, _ := Lookup(id)
		if !spec.Effects.ReadOnly || !spec.Effects.Idempotent || spec.Risk != RiskNone || spec.Confirmation.Mode != ConfirmationNone {
			t.Fatalf("config read security contract=%#v", spec)
		}
	}
}

func TestMCPConfigCapabilityInventoryPreservesAuthorityAndBindings(t *testing.T) {
	operations := map[ID]OperationInventory{}
	for _, operation := range Inventory().Operations {
		operations[operation.ID] = operation
	}
	for _, id := range []ID{ConfigList, ConfigGet, ConfigSet} {
		operation, ok := operations[id]
		if !ok {
			t.Fatalf("operator config operation %q missing from inventory", id)
		}
		if operation.Audience != AudienceOperator || operation.Authorization != AuthorizationOperator ||
			len(operation.MCPTools) != 0 || len(operation.PlannedMCPTools) != 0 {
			t.Fatalf("operator config inventory authority drifted: %#v", operation)
		}
	}
	for _, tc := range []struct {
		id   ID
		tool string
		kind Kind
	}{
		{AgentConfigList, "config_list", KindQuery},
		{AgentConfigGet, "config_get", KindQuery},
		{AgentConfigSet, "config_set", KindMutation},
	} {
		operation, ok := operations[tc.id]
		if !ok {
			t.Fatalf("agent config operation %q missing from inventory", tc.id)
		}
		if operation.Audience != AudienceAgent || operation.Authorization != AuthorizationAgent || operation.Kind != tc.kind ||
			len(operation.MCPTools) != 1 || operation.MCPTools[0] != tc.tool || len(operation.PlannedMCPTools) != 0 {
			t.Fatalf("agent config inventory drifted: %#v", operation)
		}
	}
	set := operations[AgentConfigSet]
	if set.Risk != RiskSensitive || set.Confirmation.Mode != ConfirmationRequired || !set.Confirmation.ControlApproval ||
		set.Effects.ReadOnly || set.Effects.Idempotent {
		t.Fatalf("config_set inventory security drifted: %#v", set)
	}
	for _, id := range []ID{AgentConfigList, AgentConfigGet} {
		read := operations[id]
		if read.Risk != RiskNone || read.Confirmation.Mode != ConfirmationNone || !read.Effects.ReadOnly || !read.Effects.Idempotent {
			t.Fatalf("config read inventory security drifted: %#v", read)
		}
	}
}
