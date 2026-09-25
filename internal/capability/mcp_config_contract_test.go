package capability

import "testing"

func TestMCPConfigCapabilitiesFreezePlannedAgentBindingsWithoutChangingOperatorAuthority(t *testing.T) {
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

	cases := []struct {
		id   ID
		tool string
		kind Kind
	}{
		{AgentConfigList, "config_list", KindQuery},
		{AgentConfigGet, "config_get", KindQuery},
		{AgentConfigSet, "config_set", KindMutation},
	}
	for _, tc := range cases {
		spec, ok := Lookup(tc.id)
		if !ok {
			t.Fatalf("agent config capability %q missing", tc.id)
		}
		if spec.Audience != AudienceAgent || spec.Authorization != AuthorizationAgent || spec.Kind != tc.kind {
			t.Fatalf("agent config capability metadata=%#v", spec)
		}
		if len(spec.MCPTools) != 0 || len(spec.PlannedMCPTools) != 1 || spec.PlannedMCPTools[0] != tc.tool {
			t.Fatalf("agent config binding=%#v", spec)
		}
		if _, active := ForMCPTool(tc.tool); active {
			t.Fatalf("planned config tool %q is already active", tc.tool)
		}
		if got, planned := ForPlannedMCPTool(tc.tool); !planned || got != tc.id {
			t.Fatalf("planned binding %q => %q,%t want %q,true", tc.tool, got, planned, tc.id)
		}
	}
	set, _ := Lookup(AgentConfigSet)
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
