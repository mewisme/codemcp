package application

import (
	"context"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/doctor"
)

func TestDoctorOperationUsesCanonicalCapabilityAndAcceptsAdditionalProvider(t *testing.T) {
	configDef, ok := doctor.DefinitionFor(doctor.ComponentConfigOverview)
	if !ok {
		t.Fatal("config doctor definition missing")
	}
	migrationDef, ok := doctor.DefinitionFor(doctor.ComponentMigrationReadiness)
	if !ok {
		t.Fatal("migration doctor definition missing")
	}
	service, err := NewDoctorServiceWithProviders(
		doctor.ProviderFunc{
			Definition: configDef.ProviderSpec(),
			Run: func(context.Context) (doctor.Component, error) {
				return doctor.Component{State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "configuration is readable"}, nil
			},
		},
		doctor.ProviderFunc{
			Definition: migrationDef.ProviderSpec(),
			Run: func(context.Context) (doctor.Component, error) {
				return doctor.Component{State: doctor.StateDisabled, Severity: doctor.SeverityInfo, Summary: "migration readiness is not required"}, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Operation != capability.DoctorRead || len(result.Value.Components) != 2 {
		t.Fatalf("result=%#v", result)
	}
	if result.Value.Components[1].ID != doctor.ComponentMigrationReadiness {
		t.Fatalf("migration provider did not plug into aggregate report: %#v", result.Value.Components)
	}

	dispatcher := NewDispatcher()
	if err := BindDoctorOperations(dispatcher, service); err != nil {
		t.Fatal(err)
	}
	dispatched, err := dispatcher.Dispatch(context.Background(), DispatchRequest{Operation: capability.DoctorRead})
	if err != nil {
		t.Fatal(err)
	}
	report, ok := dispatched.Value.(doctor.Report)
	if !ok || len(report.Components) != 2 || dispatched.Metadata.ID != capability.DoctorRead {
		t.Fatalf("dispatch=%#v report=%#v ok=%t", dispatched, report, ok)
	}
}

func TestDoctorRemediationOperationsAreCanonicalCapabilities(t *testing.T) {
	for _, id := range []capability.ID{
		capability.ConfigInit,
		capability.ConfigSet,
		capability.WorkspaceShow,
		capability.UpstreamServerStatus,
		capability.TunnelConfigure,
		capability.HealthRead,
		capability.CompletionDoctor,
		capability.TelegramSetup,
	} {
		if _, ok := capability.Lookup(id); !ok {
			t.Fatalf("doctor remediation references unknown capability %q", id)
		}
	}
}
