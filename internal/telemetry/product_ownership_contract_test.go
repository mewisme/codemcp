package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/configformat"
)

type productTelemetryOwnershipContract struct {
	Ownership struct {
		RemoteOwner                    string `json:"remote_owner"`
		LocalObservabilityOwner        string `json:"local_observability_owner"`
		PrimaryOperationBoundary       string `json:"primary_operation_boundary"`
		RawLocalObservabilityReuse     bool   `json:"raw_local_observability_reuse"`
		InterfaceAdapterImportsAllowed bool   `json:"interface_adapter_imports_allowed"`
		RawLoggerEventInputAllowed     bool   `json:"raw_logger_event_input_allowed"`
		RawActivityEventInputAllowed   bool   `json:"raw_activity_event_input_allowed"`
		BusinessTruthOwner             bool   `json:"business_truth_owner"`
	} `json:"ownership"`
	Events struct {
		Catalog                  map[string][]string `json:"catalog"`
		ClientFields             []string            `json:"client_fields"`
		RepresentativeOperations []struct {
			Operation     string `json:"operation"`
			Event         string `json:"event"`
			Source        string `json:"source"`
			EmissionCount int    `json:"emission_count"`
		} `json:"representative_operations"`
		ArgumentsPassed bool `json:"arguments_passed"`
		ResultsPassed   bool `json:"results_passed"`
	} `json:"events"`
	Lifecycle struct {
		QueueBounded                       bool   `json:"queue_bounded"`
		QueueOverflow                      string `json:"queue_overflow"`
		PersistentSpool                    bool   `json:"persistent_spool"`
		PeriodicFlush                      bool   `json:"periodic_flush"`
		FinalFlushBounded                  bool   `json:"final_flush_bounded"`
		ShutdownWaitUnbounded              bool   `json:"shutdown_wait_unbounded"`
		DisableClearsPending               bool   `json:"disable_clears_pending"`
		EndpointlessWorker                 bool   `json:"endpointless_worker"`
		DeliveryFailureChangesParentResult bool   `json:"delivery_failure_changes_parent_result"`
	} `json:"lifecycle"`
	Administration struct {
		Commands      []string `json:"commands"`
		SelfObserve   bool     `json:"self_observe"`
		StatusNetwork bool     `json:"status_network"`
		ShowNetwork   bool     `json:"show_network"`
	} `json:"administration"`
	InstallHandoff struct {
		TelemetryOwnsBinaryInstall                    bool `json:"telemetry_owns_binary_install"`
		TelemetryOwnsIntegrationInstall               bool `json:"telemetry_owns_integration_install"`
		BootstrapIsSupplemental                       bool `json:"bootstrap_is_supplemental"`
		SupplementalFailureChangesBinaryInstallResult bool `json:"supplemental_failure_changes_binary_install_result"`
	} `json:"install_handoff"`
}

func TestProductTelemetryOwnershipAndEventCatalogContract(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	contract := loadProductTelemetryOwnershipContract(t)
	privacy := loadProductTelemetryContract(t)

	if contract.Ownership.RemoteOwner != "internal/telemetry/product" ||
		contract.Ownership.LocalObservabilityOwner != "internal/telemetry" ||
		contract.Ownership.PrimaryOperationBoundary != "application.dispatch" {
		t.Fatalf("ownership contract drifted: %#v", contract.Ownership)
	}
	if contract.Ownership.RawLocalObservabilityReuse || contract.Ownership.InterfaceAdapterImportsAllowed ||
		contract.Ownership.RawLoggerEventInputAllowed || contract.Ownership.RawActivityEventInputAllowed ||
		contract.Ownership.BusinessTruthOwner {
		t.Fatalf("product telemetry authority widened: %#v", contract.Ownership)
	}

	wantCatalog := []string{
		"operation.completed",
		"runtime.started",
		"runtime.stopped",
		"approval.requested",
		"approval.resolved",
		"background.completed",
		"install.completed",
		"integration.bootstrap.completed",
	}
	gotCatalog := make([]string, 0, len(contract.Events.Catalog))
	for name := range contract.Events.Catalog {
		gotCatalog = append(gotCatalog, name)
	}
	slices.Sort(gotCatalog)
	slices.Sort(wantCatalog)
	if !reflect.DeepEqual(gotCatalog, wantCatalog) {
		t.Fatalf("event catalog drifted\nwant=%v\ngot=%v", wantCatalog, gotCatalog)
	}
	if contract.Events.ArgumentsPassed || contract.Events.ResultsPassed {
		t.Fatalf("operation arguments/results entered product telemetry contract: %#v", contract.Events)
	}

	allowed := append([]string(nil), privacy.Privacy.AllowedEventFields...)
	for _, fields := range contract.Events.Catalog {
		for _, field := range fields {
			if !slices.Contains(allowed, field) {
				t.Fatalf("event catalog field %q is outside the privacy allowlist", field)
			}
		}
	}
	for _, field := range contract.Events.ClientFields {
		if !slices.Contains(allowed, field) {
			t.Fatalf("client field %q is outside the privacy allowlist", field)
		}
	}

	seenOperations := map[string]bool{}
	for _, representative := range contract.Events.RepresentativeOperations {
		if representative.Operation == "" || representative.Event != "operation.completed" ||
			representative.Source != "application.dispatch" || representative.EmissionCount != 1 {
			t.Fatalf("representative operation does not freeze single dispatch emission: %#v", representative)
		}
		if seenOperations[representative.Operation] {
			t.Fatalf("representative operation %q appears more than once", representative.Operation)
		}
		if _, ok := capability.Lookup(capability.ID(representative.Operation)); !ok {
			t.Fatalf("representative operation %q is not a canonical capability", representative.Operation)
		}
		seenOperations[representative.Operation] = true
	}
}

func TestProductTelemetryLifecycleCannotOwnFunctionalOutcomes(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	contract := loadProductTelemetryOwnershipContract(t)

	if !contract.Lifecycle.QueueBounded || contract.Lifecycle.QueueOverflow != "drop" ||
		contract.Lifecycle.PersistentSpool || !contract.Lifecycle.PeriodicFlush ||
		!contract.Lifecycle.FinalFlushBounded || contract.Lifecycle.ShutdownWaitUnbounded ||
		!contract.Lifecycle.DisableClearsPending || contract.Lifecycle.EndpointlessWorker ||
		contract.Lifecycle.DeliveryFailureChangesParentResult {
		t.Fatalf("lifecycle contract drifted: %#v", contract.Lifecycle)
	}
	if !reflect.DeepEqual(contract.Administration.Commands, []string{
		"telemetry status", "telemetry enable", "telemetry disable", "telemetry show",
	}) || contract.Administration.SelfObserve || contract.Administration.StatusNetwork || contract.Administration.ShowNetwork {
		t.Fatalf("administration contract drifted: %#v", contract.Administration)
	}
	if contract.InstallHandoff.TelemetryOwnsBinaryInstall || contract.InstallHandoff.TelemetryOwnsIntegrationInstall ||
		!contract.InstallHandoff.BootstrapIsSupplemental ||
		contract.InstallHandoff.SupplementalFailureChangesBinaryInstallResult {
		t.Fatalf("install handoff contract drifted: %#v", contract.InstallHandoff)
	}
}

func loadProductTelemetryOwnershipContract(t *testing.T) productTelemetryOwnershipContract {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve telemetry ownership test path")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(current), "testdata", "product-telemetry-ownership-contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contract productTelemetryOwnershipContract
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	return contract
}
