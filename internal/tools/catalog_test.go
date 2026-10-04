package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCatalogHashIsOrderIndependentAndSchemaSensitive(t *testing.T) {
	first := Schema{Name: "a", Description: "one", InputSchema: json.RawMessage(`{"type":"object"}`)}
	second := Schema{Name: "b", Description: "two", InputSchema: json.RawMessage(`{"type":"object"}`)}
	left, err := CatalogHash([]Schema{first, second})
	if err != nil {
		t.Fatal(err)
	}
	right, err := CatalogHash([]Schema{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if left != right || left == "" {
		t.Fatalf("hashes = %q / %q", left, right)
	}
	second.Description = "changed"
	changed, err := CatalogHash([]Schema{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if changed == left {
		t.Fatal("catalog hash did not change with schema")
	}
	second.Description = "two"
	second.Capability = &CapabilityMetadata{Domain: "git"}
	withCapability, err := CatalogHash([]Schema{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if withCapability == left {
		t.Fatal("catalog hash did not change with capability metadata")
	}
	second.Capability = nil
	second.Approval = inlineApprovalMetadata()
	withApproval, err := CatalogHash([]Schema{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if withApproval == left {
		t.Fatal("catalog hash did not change with approval metadata")
	}
}

func TestCapabilityGroupingIsDeterministicBoundedAndExplicitlyUnclassified(t *testing.T) {
	schemas := []Schema{
		{Name: "git_push", Capability: &CapabilityMetadata{Domain: "git"}},
		{Name: "grep", Capability: &CapabilityMetadata{Domain: "filesystem"}},
		{Name: "git_log", Capability: &CapabilityMetadata{Domain: "git"}},
		{Name: "mystery_c"},
		{Name: "mystery_a"},
		{Name: "mystery_b"},
	}
	left := GroupCapabilitiesWithLimits(schemas, CapabilityLimits{
		MaxGroups: 8, MaxToolsPerGroup: 8, MaxTools: 16, MaxBytes: 1024, MaxUnclassifiedTools: 2,
	})
	reversed := append([]Schema(nil), schemas...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	right := GroupCapabilitiesWithLimits(reversed, CapabilityLimits{
		MaxGroups: 8, MaxToolsPerGroup: 8, MaxTools: 16, MaxBytes: 1024, MaxUnclassifiedTools: 2,
	})
	leftJSON, err := json.Marshal(left)
	if err != nil {
		t.Fatal(err)
	}
	rightJSON, err := json.Marshal(right)
	if err != nil {
		t.Fatal(err)
	}
	if string(leftJSON) != string(rightJSON) {
		t.Fatalf("grouping depends on registration order:\n%s\n%s", leftJSON, rightJSON)
	}
	if left.TotalTools != 6 || left.IncludedTools != 5 || !left.Truncated {
		t.Fatalf("capability index = %#v", left)
	}
	wantDomains := []string{"filesystem", "git", CapabilityDomainUnclassified}
	if len(left.Groups) != len(wantDomains) {
		t.Fatalf("groups = %#v", left.Groups)
	}
	for index, domain := range wantDomains {
		if left.Groups[index].Domain != domain {
			t.Fatalf("group[%d] domain=%q want=%q", index, left.Groups[index].Domain, domain)
		}
	}
	if got := left.Groups[1].Tools; len(got) != 2 || got[0] != "git_log" || got[1] != "git_push" {
		t.Fatalf("git tools = %#v", got)
	}
	unclassified := left.Groups[2]
	if !unclassified.Truncated || len(unclassified.Tools) != 2 || unclassified.Tools[0] != "mystery_a" || unclassified.Tools[1] != "mystery_b" {
		t.Fatalf("unclassified = %#v", unclassified)
	}
}

func TestCapabilityGroupingBoundsGroupsAndTools(t *testing.T) {
	schemas := []Schema{
		{Name: "a_one", Capability: &CapabilityMetadata{Domain: "alpha"}},
		{Name: "a_two", Capability: &CapabilityMetadata{Domain: "alpha"}},
		{Name: "b_one", Capability: &CapabilityMetadata{Domain: "beta"}},
		{Name: "c_one", Capability: &CapabilityMetadata{Domain: "charlie"}},
		{Name: "unknown"},
	}
	index := GroupCapabilitiesWithLimits(schemas, CapabilityLimits{
		MaxGroups: 2, MaxToolsPerGroup: 1, MaxTools: 2, MaxBytes: 128, MaxUnclassifiedTools: 1,
	})
	if !index.Truncated || index.IncludedTools != 2 || len(index.Groups) != 2 {
		t.Fatalf("bounded index = %#v", index)
	}
	if index.Groups[0].Domain != "alpha" || len(index.Groups[0].Tools) != 1 || !index.Groups[0].Truncated {
		t.Fatalf("classified group = %#v", index.Groups[0])
	}
	if index.Groups[1].Domain != CapabilityDomainUnclassified || len(index.Groups[1].Tools) != 1 {
		t.Fatalf("unclassified group = %#v", index.Groups[1])
	}
}

func TestCapabilityGroupingBoundsBytesAndHandlesEmptyInventory(t *testing.T) {
	empty := GroupCapabilities(nil)
	if empty.TotalTools != 0 || empty.IncludedTools != 0 || len(empty.Groups) != 0 || empty.Truncated {
		t.Fatalf("empty capability index = %#v", empty)
	}

	index := GroupCapabilitiesWithLimits([]Schema{
		{Name: "short", Capability: &CapabilityMetadata{Domain: "alpha"}},
		{Name: "tool_that_will_not_fit", Capability: &CapabilityMetadata{Domain: "alpha"}},
	}, CapabilityLimits{
		MaxGroups: 8, MaxToolsPerGroup: 8, MaxTools: 8, MaxBytes: len("alpha") + len("short") + 1, MaxUnclassifiedTools: 8,
	})
	if !index.Truncated || index.TotalTools != 2 || index.IncludedTools != 1 || len(index.Groups) != 1 {
		t.Fatalf("byte-bounded index = %#v", index)
	}
	if got := index.Groups[0]; got.Domain != "alpha" || len(got.Tools) != 1 || got.Tools[0] != "short" || !got.Truncated {
		t.Fatalf("byte-bounded group = %#v", got)
	}
}

func TestFirstPartyRuntimeToolsHaveCapabilities(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := NewRuntime()
	defer runtime.CompletionHooks.Stop()
	missing := []string{}
	for _, schema := range runtime.Registry.ListSchemas() {
		if schema.Capability == nil || strings.TrimSpace(schema.Capability.Domain) == "" {
			missing = append(missing, schema.Name)
		}
	}
	if len(missing) != 0 {
		t.Fatalf("first-party tools missing capability metadata: %v", missing)
	}

	want := map[string]string{
		"grep":                  CapabilityDomainFilesystem,
		"git_push":              CapabilityDomainGit,
		"run_command":           CapabilityDomainShell,
		"workspace_status":      CapabilityDomainWorkspace,
		"project_context":       CapabilityDomainContext,
		"load_path_rules":       CapabilityDomainRules,
		"list_skills":           CapabilityDomainSkills,
		"remember":              CapabilityDomainMemory,
		CreatePlanToolName:      CapabilityDomainPlans,
		ApprovalRequestToolName: CapabilityDomainApprovals,
		AgentSpawnToolName:      CapabilityDomainAgents,
		"fanout_turn":           CapabilityDomainIntegrations,
	}
	for name, domain := range want {
		schema, ok := runtime.Registry.Schema(name)
		if !ok || schema.Capability == nil || schema.Capability.Domain != domain {
			t.Fatalf("%s capability = %#v want %q", name, schema.Capability, domain)
		}
	}
}

func TestFirstPartyApprovableToolsAdvertiseInlineApproval(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := NewRuntime()
	defer runtime.CompletionHooks.Stop()

	want := map[string]bool{
		"config_set":    true,
		"git_push":      true,
		"git_reset":     true,
		"git_restore":   true,
		"rewind":        true,
		"run_command":   true,
		"start_process": true,
	}
	got := map[string]bool{}
	for _, schema := range runtime.Registry.ListSchemas() {
		if schema.Approval != nil && schema.Approval.Inline {
			got[schema.Name] = true
			if strings.Contains(string(schema.InputSchema), InlineApprovalArgumentKey) {
				t.Fatalf("canonical business schema for %q contains reserved approval envelope: %s", schema.Name, schema.InputSchema)
			}
		}
	}
	if len(got) != len(want) {
		t.Fatalf("inline approval tool inventory=%v want=%v", got, want)
	}
	for name := range want {
		if !got[name] {
			t.Fatalf("approvable tool %q missing inline approval metadata: got=%v", name, got)
		}
	}
}

func TestRuntimeDoesNotExposeWorkspaceRelocateTool(t *testing.T) {
	runtime := NewRuntime()
	for _, schema := range runtime.ListTools() {
		name := strings.ToLower(strings.TrimSpace(schema.Name))
		if name == "workspace_relocate" || name == "workspace.relocate" || name == "workspace_move" {
			t.Fatalf("control-plane workspace relocate leaked into MCP tool catalog as %q", schema.Name)
		}
	}
}

func TestToolAnnotationsReflectEffects(t *testing.T) {
	for _, test := range []struct {
		risk                                         Risk
		readOnly, destructive, openWorld, idempotent bool
	}{
		{risk: RiskRead, readOnly: true, idempotent: true},
		{risk: RiskEdit},
		{risk: RiskDestructive, destructive: true},
		{risk: RiskCommand, destructive: true, openWorld: true},
	} {
		annotations := ToolAnnotations(test.risk)
		for key, want := range map[string]bool{"readOnlyHint": test.readOnly, "destructiveHint": test.destructive, "openWorldHint": test.openWorld, "idempotentHint": test.idempotent} {
			if got, ok := annotations[key].(bool); !ok || got != want {
				t.Fatalf("risk=%s %s=%#v want=%t", test.risk, key, annotations[key], want)
			}
		}
	}
	open := ToolAnnotationsOpenWorld(RiskEdit)
	if readOnly, _ := open["readOnlyHint"].(bool); readOnly {
		t.Fatal("open-world edit became read-only")
	}
	if openWorld, _ := open["openWorldHint"].(bool); !openWorld {
		t.Fatal("open-world edit did not set openWorldHint")
	}
}

func TestCoreToolSchemasMatchHandlerContracts(t *testing.T) {
	runtime, _, _ := newToolTestRuntime(t)
	schemas := map[string]Schema{}
	for _, schema := range runtime.List() {
		if !json.Valid(schema.InputSchema) {
			t.Fatalf("%s has invalid input schema", schema.Name)
		}
		if len(schema.OutputSchema) > 0 && !json.Valid(schema.OutputSchema) {
			t.Fatalf("%s has invalid output schema", schema.Name)
		}
		schemas[schema.Name] = schema
	}
	property := func(tool, name string, output bool) map[string]any {
		schema := schemas[tool]
		raw := schema.InputSchema
		if output {
			raw = schema.OutputSchema
		}
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		properties, _ := value["properties"].(map[string]any)
		item, _ := properties[name].(map[string]any)
		return item
	}
	required := func(tool, name string) bool {
		var value map[string]any
		if err := json.Unmarshal(schemas[tool].InputSchema, &value); err != nil {
			t.Fatal(err)
		}
		items, _ := value["required"].([]any)
		for _, item := range items {
			if item == name {
				return true
			}
		}
		return false
	}
	if !required("load_path_rules", "path") {
		t.Fatal("load_path_rules.path is not required")
	}
	if property("git_log", "count", false)["maximum"] != float64(10000) {
		t.Fatalf("git_log.count schema=%#v", property("git_log", "count", false))
	}
	if property("directory_tree", "max_depth", false)["maximum"] != float64(128) {
		t.Fatalf("directory_tree.max_depth schema=%#v", property("directory_tree", "max_depth", false))
	}
	var processOutput map[string]any
	if err := json.Unmarshal(schemas["process_status"].OutputSchema, &processOutput); err != nil {
		t.Fatal(err)
	}
	processes := processOutput["properties"].(map[string]any)["processes"].(map[string]any)
	items := processes["items"].(map[string]any)
	if _, ok := items["properties"].(map[string]any)["execution_id"]; !ok {
		t.Fatal("process_status output schema missing execution_id")
	}
	expectedDescriptions := map[string][]string{
		"start_process":  {"lifecycle-driven", "do not poll process_status/process_output"},
		"process_status": {"diagnosis or recovery", "waiting loop"},
		"process_output": {"diagnosis or recovery", "waiting loop"},
		"stop_process":   {"cancel or intervene", "completion-wait mechanism"},
	}
	for name, expected := range expectedDescriptions {
		description := schemas[name].Description
		if len(description) > 320 {
			t.Fatalf("%s description is too verbose (%d bytes): %s", name, len(description), description)
		}
		for _, fragment := range expected {
			if !strings.Contains(description, fragment) {
				t.Fatalf("%s description missing %q: %s", name, fragment, description)
			}
		}
	}
}
