package application

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/backgrounddelivery"
	"go.mewis.me/codemcp/internal/checkpoint"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/doctor"
	codegraph "go.mewis.me/codemcp/internal/integrations/codegraph"
	rtk "go.mewis.me/codemcp/internal/integrations/rtk"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	"go.mewis.me/codemcp/internal/tunnel"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestDefaultDoctorProvidersSatisfyCanonicalInventory(t *testing.T) {
	providers := defaultDoctorProviders(DoctorDependencies{})
	if err := doctor.ValidateInventoryCoverage(providers); err != nil {
		t.Fatalf("default doctor providers drifted from canonical inventory: %v", err)
	}
	registered := map[doctor.ComponentID]bool{}
	for _, provider := range providers {
		registered[provider.Spec().ID] = true
	}
	for _, definition := range doctor.Inventory() {
		if definition.Source == doctor.SourceDeferred {
			if registered[definition.ID] {
				t.Fatalf("deferred provider %q was registered by aggregate doctor", definition.ID)
			}
			continue
		}
		if !registered[definition.ID] {
			t.Fatalf("required provider %q is missing", definition.ID)
		}
	}
}

func TestBackgroundDeliveryDiagnosticFieldsRequireExplicitDoctorReconciliation(t *testing.T) {
	component := backgroundDeliveryDoctorComponent(backgrounddelivery.Diagnostics{})
	actualMetrics := metricIDs(component)
	reflected := jsonFieldNames(reflect.TypeOf(backgrounddelivery.Diagnostics{}))
	if !reflect.DeepEqual(actualMetrics, reflected) {
		t.Fatalf("background delivery fields are not fully reconciled with doctor metrics\nmetrics=%v\nfields=%v", actualMetrics, reflected)
	}
}

func TestRuntimeDiagnosticFieldsRequireExplicitDoctorReconciliation(t *testing.T) {
	const (
		dispositionMetric  = "metric"
		dispositionFlag    = "flag"
		dispositionState   = "state"
		dispositionContext = "context"
	)
	expected := map[string]string{
		"pid":                dispositionMetric,
		"run_id":             dispositionContext,
		"lifecycle":          dispositionState,
		"starting":           dispositionFlag,
		"managed":            dispositionFlag,
		"service_id":         dispositionContext,
		"service_scope":      dispositionContext,
		"started_at":         dispositionContext,
		"config_root":        dispositionContext,
		"config_fingerprint": dispositionContext,
		"server_enabled":     dispositionFlag,
		"server_port":        dispositionMetric,
		"admin_enabled":      dispositionFlag,
		"admin_port":         dispositionMetric,
		"exposure":           dispositionContext,
		"tunnel_enabled":     dispositionFlag,
		"tunnel_configured":  dispositionFlag,
		"tunnel_running":     dispositionFlag,
		"tunnel_ready":       dispositionFlag,
		"tunnel_restarting":  dispositionFlag,
		"tunnel_id":          dispositionContext,
		"tunnel_last_error":  dispositionContext,
		"tool_profile":       dispositionContext,
		"tool_count":         dispositionMetric,
		"readiness":          dispositionContext,
	}
	assertReflectedFieldInventory(t, reflect.TypeOf(runtimecontrol.RuntimeStatus{}), expected)

	component := runtimeControlDoctorComponent(runtimecontrol.RuntimeStatus{Lifecycle: "ready"}, true)
	metricSet := stringSet(metricIDs(component))
	flagSet := stringSet(flagIDs(component))
	for field, disposition := range expected {
		switch disposition {
		case dispositionMetric:
			if !metricSet[field] {
				t.Fatalf("runtime metric %q is reconciled but not projected", field)
			}
		case dispositionFlag:
			if !flagSet[field] {
				t.Fatalf("runtime flag %q is reconciled but not projected", field)
			}
		}
	}
}

func TestServiceDiagnosticFieldsRequireExplicitDoctorReconciliation(t *testing.T) {
	expected := map[string]string{
		"Scope":      "context",
		"Supported":  "state",
		"ID":         "context",
		"Backend":    "context",
		"Installed":  "flag",
		"Running":    "flag",
		"PID":        "metric",
		"ConfigRoot": "context",
		"Warning":    "state",
		"Err":        "state",
	}
	actual := map[string]bool{}
	typ := reflect.TypeOf(ServiceOverview{})
	for index := 0; index < typ.NumField(); index++ {
		actual[typ.Field(index).Name] = true
	}
	if len(actual) != len(expected) {
		t.Fatalf("service diagnostic field inventory changed: actual=%v expected=%v", sortedBoolKeys(actual), sortedMapKeys(expected))
	}
	for name := range actual {
		if _, ok := expected[name]; !ok {
			t.Fatalf("service diagnostic field %q requires explicit doctor reconciliation", name)
		}
	}
	component := serviceDoctorComponent(ServiceOverview{Supported: true})
	if !stringSet(metricIDs(component))["pid"] || !stringSet(flagIDs(component))["installed"] || !stringSet(flagIDs(component))["running"] {
		t.Fatalf("service projection is incomplete: %#v", component)
	}
}

func TestDoctorDegradedFixtures(t *testing.T) {
	tests := []struct {
		name      string
		component doctor.Component
	}{
		{
			name:      "rtk unavailable",
			component: rtkDoctorComponent(rtk.Status{Enabled: true, Source: rtk.SourceUnavailable}),
		},
		{
			name: "codegraph unavailable",
			component: codeGraphDoctorComponent(codegraph.Status{
				Enabled:    true,
				Resolution: codegraph.Resolution{Source: codegraph.ExecutableUnavailable},
			}),
		},
		{
			name:      "typesafe missing credential",
			component: typeSafeDoctorComponent(true, false),
		},
		{
			name:      "secure tunnel not ready",
			component: tunnelDoctorFixture(t),
		},
		{
			name: "checkpoint archive orphan",
			component: checkpointHistoryDoctorComponent([]checkpoint.StorageHealth{{
				Status: checkpoint.HealthDegraded, ArchiveIndexPresent: true, OrphanArchivePayloads: 1,
			}}),
		},
		{
			name: "workspace lock",
			component: workspaceLocalStateDoctorComponent([]workspace.LocalStateDiagnostic{{
				Health: workspace.LocalStateHealthy, Available: true, Locked: true,
			}}, 0),
		},
		{
			name: "telegram reconnecting",
			component: telegramDoctorComponent(TelegramHealthSnapshot{
				Enabled: true, TokenConfigured: true, AuthorizationConfigured: true,
				Running: true, PollingHealthy: false, Reconnecting: true, ReconnectCount: 2,
			}),
		},
		{
			name: "runtime starting",
			component: runtimeControlDoctorComponent(runtimecontrol.RuntimeStatus{
				Lifecycle: "starting", Starting: true, Managed: true,
			}, true),
		},
		{
			name: "service warning",
			component: serviceDoctorComponent(ServiceOverview{
				Supported: true, Installed: true, Running: true, Warning: "persistence warning",
			}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.component.State != doctor.StateDegraded || test.component.Severity != doctor.SeverityWarning {
				t.Fatalf("component=%#v", test.component)
			}
		})
	}
}

func TestTelegramDoctorProjectsTransportNavigationAndMiniAppHealth(t *testing.T) {
	component := telegramDoctorComponent(TelegramHealthSnapshot{
		Enabled: true, TokenConfigured: true, AuthorizationConfigured: true,
		Running: true, PollingHealthy: true,
		DeliveryDegraded: true, DeliveryRateLimited: true, DeliveryFailures: 3, DeliveryRetryAfterMS: 1250,
		RichMessageSupported: true, RichMessageFallback: true,
		TopicsConfigured: true, TopicsSupported: true, TopicsEffective: true, TopicStoreHealthy: true, TopicCount: 4,
		CommandsPublished: true, CommandCount: 14, MenuReconciled: true, MenuDriftCount: 2,
		AllowedUpdateCount: 2, MaxFileTransferBytes: 20 << 20,
		LogsMiniAppEnabled: true, LogsMiniAppState: "ready", LogsMiniAppDependency: true,
		LogsMiniAppListenerReady: true, LogsMiniAppTunnelRunning: true, LogsMiniAppIngressReady: true, LogsMiniAppGeneration: 9,
	})
	if component.State != doctor.StateDegraded || component.Severity != doctor.SeverityWarning {
		t.Fatalf("telegram component=%#v", component)
	}
	flags := stringSet(flagIDs(component))
	for _, id := range []string{
		"delivery_degraded", "delivery_rate_limited", "rich_message_supported", "rich_message_fallback",
		"topics_configured", "topics_supported", "topics_effective", "topic_store_healthy", "topic_reconcile_pending", "commands_published", "menu_reconciled",
		"logs_mini_app_listener_ready", "logs_mini_app_tunnel_running", "logs_mini_app_ingress_ready",
	} {
		if !flags[id] {
			t.Fatalf("telegram doctor missing flag %q: %#v", id, component.Flags)
		}
	}
	metrics := stringSet(metricIDs(component))
	for _, id := range []string{"delivery_failures", "delivery_retry_after_ms", "command_count", "menu_drift", "allowed_updates", "max_file_transfer_bytes", "topic_count", "logs_mini_app_generation"} {
		if !metrics[id] {
			t.Fatalf("telegram doctor missing metric %q: %#v", id, component.Metrics)
		}
	}
}

func TestTelegramDoctorTreatsUnsupportedTopicsAsCapabilityAbsenceNotFailure(t *testing.T) {
	component := telegramDoctorComponent(TelegramHealthSnapshot{
		Enabled: true, TokenConfigured: true, AuthorizationConfigured: true,
		Running: true, PollingHealthy: true,
		TopicsConfigured: true, TopicsSupported: false, TopicsEffective: false, TopicStoreHealthy: true,
		CommandsPublished: true, MenuReconciled: true,
	})
	if component.State != doctor.StateHealthy || component.Severity != doctor.SeverityInfo {
		t.Fatalf("unsupported topics degraded Telegram baseline: %#v", component)
	}
}

func TestTelegramDoctorSurfacesTopicReconciliationFailure(t *testing.T) {
	component := telegramDoctorComponent(TelegramHealthSnapshot{
		Enabled: true, TokenConfigured: true, AuthorizationConfigured: true,
		Running: true, PollingHealthy: true,
		TopicsConfigured: true, TopicsSupported: true, TopicsEffective: false,
		TopicStoreHealthy: false, TopicLastError: "telegram topic metadata is corrupt; explicit repair is required",
		CommandsPublished: true, MenuReconciled: true,
	})
	if component.State != doctor.StateDegraded || component.Severity != doctor.SeverityWarning || component.Summary != "Telegram topic routing requires attention" {
		t.Fatalf("topic reconciliation failure component=%#v", component)
	}
}

func TestShellDoctorCoversPlatformProviderKinds(t *testing.T) {
	tests := []struct {
		name string
		kind shellruntime.ProviderKind
		flag string
	}{
		{name: "posix", kind: shellruntime.ProviderPOSIX, flag: "posix"},
		{name: "git bash", kind: shellruntime.ProviderGitBash, flag: "git_bash"},
		{name: "powershell 7", kind: shellruntime.ProviderPowerShell7, flag: "powershell7"},
		{name: "windows powershell", kind: shellruntime.ProviderWindowsPowerShell, flag: "windows_powershell"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			component := shellDoctorComponent(shellruntime.ProviderDiagnostic{
				Available: true, Source: shellruntime.ProviderSourceSystem, Kind: test.kind,
			})
			if component.State != doctor.StateHealthy || !flagValue(component, test.flag) {
				t.Fatalf("component=%#v", component)
			}
		})
	}

	unavailable := shellDoctorComponent(shellruntime.ProviderDiagnostic{
		Available: false, ErrorCode: "invalid_configuration", ConfiguredPaths: 1,
	})
	if unavailable.State != doctor.StateDegraded || !flagValue(unavailable, "invalid_configuration") {
		t.Fatalf("unavailable shell component=%#v", unavailable)
	}
}

func TestServiceDoctorCoversSupportedAndUnsupportedPlatforms(t *testing.T) {
	unsupported := serviceDoctorComponent(ServiceOverview{Supported: false})
	if unsupported.State != doctor.StateDisabled {
		t.Fatalf("unsupported service component=%#v", unsupported)
	}
	supported := serviceDoctorComponent(ServiceOverview{Supported: true, Installed: true, Running: true, PID: 42})
	if supported.State != doctor.StateHealthy || !flagValue(supported, "installed") || !flagValue(supported, "running") {
		t.Fatalf("supported service component=%#v", supported)
	}
}

func tunnelDoctorFixture(t *testing.T) doctor.Component {
	t.Helper()
	inspection := config.Inspection{Exists: true, Config: config.Default(), TunnelRuntimeKeyConfigured: true}
	inspection.Config.Tunnel.Enabled = true
	inspection.Config.Tunnel.ID = "tunnel_fixture"
	status := tunnel.Status{Enabled: true, Running: true, Ready: false}
	return tunnelDoctorComponent(inspection, &status)
}

func metricIDs(component doctor.Component) []string {
	result := make([]string, 0, len(component.Metrics))
	for _, metric := range component.Metrics {
		result = append(result, metric.ID)
	}
	sort.Strings(result)
	return result
}

func flagIDs(component doctor.Component) []string {
	result := make([]string, 0, len(component.Flags))
	for _, flag := range component.Flags {
		result = append(result, flag.ID)
	}
	sort.Strings(result)
	return result
}

func flagValue(component doctor.Component, id string) bool {
	for _, flag := range component.Flags {
		if flag.ID == id {
			return flag.Value
		}
	}
	return false
}

func jsonFieldNames(typ reflect.Type) []string {
	result := make([]string, 0, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		tag := strings.Split(typ.Field(index).Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		result = append(result, tag)
	}
	sort.Strings(result)
	return result
}

func assertReflectedFieldInventory(t *testing.T, typ reflect.Type, expected map[string]string) {
	t.Helper()
	actual := jsonFieldNames(typ)
	want := sortedMapKeys(expected)
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("diagnostic field inventory changed and requires explicit doctor reconciliation\nactual=%v\nexpected=%v", actual, want)
	}
}

func sortedMapKeys(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func sortedBoolKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}
