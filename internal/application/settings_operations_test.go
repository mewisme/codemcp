package application

import (
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
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
	if ErrorCodeOf(err) != ErrorConflict {
		t.Fatalf("stale setting mutation error=%v code=%s", err, ErrorCodeOf(err))
	}
	current, err := service.Present(t.Context(), "telegram.allowed_user_ids")
	if err != nil {
		t.Fatal(err)
	}
	if current.Value != "42" {
		t.Fatalf("stale setting mutation changed current value: %q", current.Value)
	}
}
