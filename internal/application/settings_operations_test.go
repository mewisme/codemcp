package application

import (
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
)

func TestSettingOperationsUseCanonicalMetadataSearchAndAtomicApply(t *testing.T) {
	isolateSettingServiceConfig(t)
	dispatcher := NewDispatcher()
	if err := BindSettingOperations(dispatcher, NewSettingService()); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []capability.ID{capability.ConfigExport, capability.ConfigList, capability.ConfigGet, capability.ConfigSet} {
		if _, ok := dispatcher.handlers[operation]; !ok {
			t.Fatalf("missing canonical setting binding %s", operation)
		}
	}

	searched, err := dispatcher.Dispatch(t.Context(), DispatchRequest{
		Operation: capability.ConfigList,
		Input:     ConfigListInput{Query: "port"},
	})
	if err != nil {
		t.Fatal(err)
	}
	items := searched.Value.([]SettingResult)
	if len(items) == 0 {
		t.Fatal("settings search returned no matches")
	}
	found := false
	for _, item := range items {
		if item.Spec.Key == "server.port" {
			found = true
		}
	}
	if !found {
		t.Fatalf("server.port not found in search: %#v", items)
	}

	applied, err := dispatcher.Dispatch(t.Context(), DispatchRequest{
		Operation: capability.ConfigSet,
		Input: ConfigSetInput{Action: "apply", Changes: []SettingChange{
			{Key: "server.port", Value: "40125"},
			{Key: "admin.enabled", Value: "false"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := applied.Value.(SettingApplyResult)
	if len(result.Results) != 2 {
		t.Fatalf("atomic apply result=%#v", result)
	}
	for _, item := range result.Results {
		current, err := NewSettingService().Present(t.Context(), item.Spec.Key)
		if err != nil {
			t.Fatal(err)
		}
		if current.Value != item.Value {
			t.Fatalf("atomic post-state drift for %s: result=%q current=%q", item.Spec.Key, item.Value, current.Value)
		}
	}
	read, err := dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: capability.ConfigGet, Input: ConfigGetInput{Key: "server.port"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := read.Value.(SettingResult).Value; got != "40125" {
		t.Fatalf("server.port=%q want=40125", got)
	}
}

func TestConfigExportOperationExcludesManagedSecrets(t *testing.T) {
	isolateSettingServiceConfig(t)
	service := NewSettingService()
	const secret = "telegram-export-secret-sentinel"
	if _, err := service.Set(t.Context(), "telegram.token", secret); err != nil {
		t.Fatal(err)
	}
	dispatcher := NewDispatcher()
	if err := BindSettingOperations(dispatcher, service); err != nil {
		t.Fatal(err)
	}
	result, err := dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: capability.ConfigExport})
	if err != nil {
		t.Fatal(err)
	}
	document := result.Value.(ConfigExportDocument)
	if len(document.Data) == 0 || !strings.HasSuffix(document.FileName, ".json") {
		t.Fatalf("export document=%#v", document)
	}
	if strings.Contains(string(document.Data), secret) {
		t.Fatalf("managed secret leaked into portable export: %s", document.Data)
	}
}

func TestSettingSemanticStateSeparatesConfiguredGeneratedAndDerivedValues(t *testing.T) {
	isolateSettingServiceConfig(t)
	service := NewSettingService()
	const secret = "telegram-semantic-secret"
	written, err := service.Set(t.Context(), "telegram.token", secret)
	if err != nil {
		t.Fatal(err)
	}
	current, err := service.Present(t.Context(), "telegram.token")
	if err != nil {
		t.Fatal(err)
	}
	if written.Value != current.Value || strings.Contains(current.Value, secret) ||
		current.Configured == nil || !*current.Configured ||
		!current.Spec.Secret || !current.Spec.Writable ||
		current.Spec.ValueRole != config.SettingValueConfigured ||
		current.Spec.Presentation != config.SettingPresentationMaskedPreview {
		t.Fatalf("configured secret semantic state=%#v written=%#v", current, written)
	}

	for _, check := range []struct {
		key      string
		role     config.SettingValueRole
		writable bool
		derived  bool
	}{
		{key: "telegram.token", role: config.SettingValueConfigured, writable: true},
		{key: "auth.mcp_token", role: config.SettingValueGenerated, writable: false},
		{key: "tunnel.admin.verified", role: config.SettingValueDerived, writable: false, derived: true},
	} {
		spec, ok := config.SettingByKey(check.key)
		if !ok || spec.ValueRole != check.role || spec.Writable != check.writable || spec.Derived != check.derived {
			t.Fatalf("setting %s semantic metadata=%#v ok=%t", check.key, spec, ok)
		}
	}
}

func TestConfigSetRejectsUnsupportedActionAsInvalidArgument(t *testing.T) {
	isolateSettingServiceConfig(t)
	dispatcher := NewDispatcher()
	if err := BindSettingOperations(dispatcher, NewSettingService()); err != nil {
		t.Fatal(err)
	}
	_, err := dispatcher.Dispatch(t.Context(), DispatchRequest{
		Operation: capability.ConfigSet,
		Input:     ConfigSetInput{Action: "presentation-only-action", Key: "server.port"},
	})
	if got := ErrorSemanticsOf(err); got.Code != ErrorInvalidArgument || got.Retryable || got.Stale {
		t.Fatalf("unsupported setting action semantics=%#v err=%v", got, err)
	}
}

func TestConfigSetOperationRejectsStaleExpectedValue(t *testing.T) {
	isolateSettingServiceConfig(t)
	service := NewSettingService()
	dispatcher := NewDispatcher()
	if err := BindSettingOperations(dispatcher, service); err != nil {
		t.Fatal(err)
	}
	before, err := service.Present(t.Context(), "telegram.allowed_user_ids")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Set(t.Context(), "telegram.allowed_user_ids", "42"); err != nil {
		t.Fatal(err)
	}
	_, err = dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: capability.ConfigSet, Input: ConfigSetInput{
		Action: "set", Key: "telegram.allowed_user_ids", Value: "99",
		ExpectedValue: before.Value, CheckExpected: true,
	}})
	semantics := ErrorSemanticsOf(err)
	if semantics.Code != ErrorConflict || !semantics.Stale || !semantics.Retryable {
		t.Fatalf("stale setting mutation error=%v semantics=%#v", err, semantics)
	}
	current, err := service.Present(t.Context(), "telegram.allowed_user_ids")
	if err != nil {
		t.Fatal(err)
	}
	if current.Value != "42" {
		t.Fatalf("stale setting mutation changed current value: %q", current.Value)
	}
}
