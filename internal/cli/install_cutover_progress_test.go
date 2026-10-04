package cli

import (
	"bytes"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
)

func TestInstallIntegrationProgressIsTransientAndSummaryIsCanonical(t *testing.T) {
	var output bytes.Buffer
	session := presentation.NewProgressSession(&output, presentation.ModeHuman, presentation.Capabilities{
		Width: 100, Unicode: true, RawUnicode: true, Interactive: true, CursorControl: true, Animation: true,
	})
	session.Begin("Install CodeMCP")
	observe := installIntegrationProgressObserver(session)
	observe(application.IntegrationEnsureEvent{Integration: "rtk", Phase: "check", State: "running"})
	if !strings.Contains(output.String(), "Checking rtk") {
		t.Fatalf("integration check did not enter loading state: %q", output.String())
	}
	observe(application.IntegrationEnsureEvent{Integration: "rtk", Phase: "check", State: "success"})
	observe(application.IntegrationEnsureEvent{Integration: "rtk", Phase: "install", State: "running"})
	if !strings.Contains(output.String(), "Installing rtk") {
		t.Fatalf("managed install did not enter loading state: %q", output.String())
	}
	renderSupplementalInstallSummaryToSession(session, application.SupplementalBootstrapResult{
		Telemetry: application.TelemetryBootstrapResult{Enabled: true, EndpointAvailable: true, IdentityPresent: true},
		Integrations: []application.IntegrationEnsureResult{
			{Integration: "rtk", State: "installed", Source: "managed"},
			{Integration: "codegraph", State: "available", Source: "system"},
			{Integration: "cf-tunnel", State: "skipped", Source: "unavailable", Detail: "managed installation disabled for this invocation"},
		},
	})
	session.CloseWith("Done")

	got := output.String()
	for _, want := range []string{"Install supplements", "Telemetry · ready", "rtk · installed · managed", "codegraph · available · system", "cf-tunnel · skipped", "detail", "managed installation disabled for this invocation"} {
		if !strings.Contains(got, want) {
			t.Fatalf("supplement summary missing %q: %q", want, got)
		}
	}
	if strings.Count(got, "rtk · installed · managed") != 1 || strings.Count(got, "Install supplements") != 1 {
		t.Fatalf("supplement summary duplicated: %q", got)
	}
	if strings.Contains(got, "cf-tunnel · skipped · unavailable") || strings.Contains(got, "Install supplements processed") {
		t.Fatalf("redundant supplement output leaked: %q", got)
	}
}

func TestSupplementalInstallSummaryDeduplicatesWarningsAndPreservesPlainJSONContracts(t *testing.T) {
	result := application.SupplementalBootstrapResult{
		Telemetry:    application.TelemetryBootstrapResult{Enabled: true},
		Integrations: []application.IntegrationEnsureResult{{Integration: "rtk", State: "failed", Source: "unavailable", Detail: "offline", Retry: "cm integration rtk install"}},
		Warnings:     []string{"rtk bootstrap failed: offline", "telemetry bootstrap failed: endpoint unavailable"},
	}

	var human bytes.Buffer
	humanSession := presentation.NewProgressSession(&human, presentation.ModeHuman, presentation.Capabilities{Width: 100, Unicode: true, RawUnicode: true})
	humanSession.Begin("Install CodeMCP")
	renderSupplementalInstallSummaryToSession(humanSession, result)
	humanSession.CloseWith("Done")
	humanOutput := human.String()
	if strings.Count(humanOutput, "offline") != 1 {
		t.Fatalf("integration failure warning was duplicated: %q", humanOutput)
	}
	for _, want := range []string{"Telemetry · unavailable", "telemetry bootstrap failed: endpoint unavailable", "rtk · failed", "retry", "cm integration rtk install"} {
		if !strings.Contains(humanOutput, want) {
			t.Fatalf("human output missing %q: %q", want, humanOutput)
		}
	}

	var plain bytes.Buffer
	plainSession := presentation.NewProgressSession(&plain, presentation.ModePlain, presentation.Capabilities{Width: 100})
	plainSession.Begin("Install CodeMCP")
	renderSupplementalInstallSummaryToSession(plainSession, result)
	plainSession.CloseWith("Done")
	if strings.ContainsAny(plain.String(), "\r\x1b") || strings.Count(plain.String(), "Install supplements") != 1 {
		t.Fatalf("plain output is not deterministic/cursor-free: %q", plain.String())
	}

	var jsonOutput bytes.Buffer
	jsonSession := presentation.NewProgressSession(&jsonOutput, presentation.ModeJSON, presentation.Capabilities{})
	renderSupplementalInstallSummaryToSession(jsonSession, result)
	jsonSession.CloseWith("Done")
	if jsonOutput.Len() != 0 {
		t.Fatalf("JSON presentation emitted human supplement output: %q", jsonOutput.String())
	}
}

func TestSupplementalInstallSummaryCoversAllIntegrationOutcomeStates(t *testing.T) {
	result := application.SupplementalBootstrapResult{
		Telemetry: application.TelemetryBootstrapResult{Enabled: true, EndpointAvailable: true, IdentityPresent: true},
		Integrations: []application.IntegrationEnsureResult{
			{Integration: "ready", State: "available", Source: "system"},
			{Integration: "installed", State: "installed", Source: "managed"},
			{Integration: "skipped", State: "skipped", Detail: "disabled for this invocation"},
			{Integration: "unavailable", State: "unavailable", Detail: "unsupported", Retry: "cm integration cf install"},
			{Integration: "failed", State: "failed", Detail: "offline", Retry: "cm integration rtk install"},
		},
	}
	var output bytes.Buffer
	session := presentation.NewProgressSession(&output, presentation.ModeHuman, presentation.Capabilities{Width: 100, Unicode: true, RawUnicode: true})
	session.Begin("Install CodeMCP")
	renderSupplementalInstallSummaryToSession(session, result)
	session.CloseWith("Done")
	got := output.String()
	for _, want := range []string{
		"ready · available · system", "installed · installed · managed", "skipped · skipped",
		"unavailable · unavailable", "failed · failed", "unsupported", "offline",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q: %q", want, got)
		}
	}
	if strings.Count(got, "Install supplements") != 1 {
		t.Fatalf("supplements group count mismatch: %q", got)
	}
}

func TestInstallCutoverUnsupportedStateUsesReadableHierarchy(t *testing.T) {
	var output bytes.Buffer
	session := presentation.NewProgressSession(&output, presentation.ModeHuman, presentation.Capabilities{Width: 100, Unicode: true, RawUnicode: true})
	session.SetTitle("Install CodeMCP")
	observe := installCutoverProgressObserver(session)
	observe(application.InstallCutoverEvent{Stage: "detect", State: "warning", Message: "Previous CodeMCP state requires a clean install"})
	observe(application.InstallCutoverEvent{Stage: "detect", State: "warning", Message: "7 unsupported artifacts cannot be migrated", Child: true})
	observe(application.InstallCutoverEvent{Stage: "cleanup", State: "running", Message: "Removing previous CodeMCP state"})
	observe(application.InstallCutoverEvent{Stage: "cleanup", State: "success", Message: "Previous CodeMCP state removed"})
	session.CloseWith("Done")

	got := output.String()
	want := "!  Previous CodeMCP state requires a clean install\n│\n│  ! 7 unsupported artifacts cannot be migrated\n◇  Finalize installation\n│\n◆  Previous CodeMCP state removed"
	if !strings.Contains(got, want) {
		t.Fatalf("unsupported-state presentation mismatch: %q", got)
	}
}
