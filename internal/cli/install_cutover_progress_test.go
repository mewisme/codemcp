package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func TestInstallIntegrationProgressIsTransientAndSummaryIsCanonical(t *testing.T) {
	var output bytes.Buffer
	cmd := &cobra.Command{Use: "install"}
	cmd.SetOut(presentation.WrapWriter(&output, presentation.Capabilities{
		Width: 100, Unicode: true, RawUnicode: true, Interactive: true, CursorControl: true, Animation: true,
	}))
	setCommandPresentationTitle(cmd, "Install CodeMCP")
	session := commandProgressSession(cmd)
	observe := commandTraceObserver(cmd)
	observe(tracepkg.Event{Component: "INTEGRATION", Name: "integration.ensure.rtk.check.started", Message: "Checking rtk", Phase: tracepkg.PhaseStart})
	if !strings.Contains(output.String(), "Checking rtk") {
		t.Fatalf("integration check did not enter loading state: %q", output.String())
	}
	observe(tracepkg.Event{Component: "INTEGRATION", Name: "integration.ensure.rtk.check.completed", Message: "Checked rtk", Phase: tracepkg.PhaseEnd})
	observe(tracepkg.Event{Component: "INTEGRATION", Name: "integration.ensure.rtk.install.started", Message: "Installing rtk", Phase: tracepkg.PhaseStart})
	if !strings.Contains(output.String(), "Installing rtk") {
		t.Fatalf("managed install did not enter loading state: %q", output.String())
	}
	observe(tracepkg.Event{Component: "INTEGRATION", Name: "integration.ensure.rtk.install.completed", Message: "Installed rtk", Phase: tracepkg.PhaseEnd})
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
	cmd := &cobra.Command{Use: "install"}
	cmd.SetOut(presentation.WrapWriter(&output, presentation.Capabilities{Width: 100, Unicode: true, RawUnicode: true, Interactive: true}))
	setCommandPresentationTitle(cmd, "Install CodeMCP")
	session := commandProgressSession(cmd)
	observe := installCutoverProgressObserver(session)
	observe(application.InstallCutoverEvent{Stage: "detect", State: "warning", Message: "Previous CodeMCP state requires a clean install"})
	observe(application.InstallCutoverEvent{Stage: "detect", State: "warning", Message: "7 unsupported artifacts cannot be migrated", Child: true})
	traceObserve := commandTraceObserver(cmd)
	traceObserve(tracepkg.Event{Component: "INSTALL", Name: "install.cutover.cleanup.started", Message: "Removing previous CodeMCP state", Phase: tracepkg.PhaseStart})
	traceObserve(tracepkg.Event{Component: "INSTALL", Name: "install.cutover.cleanup.completed", Message: "Previous CodeMCP state removed", Phase: tracepkg.PhaseEnd})
	session.CloseWith("Done")

	got := output.String()
	want := "!  Previous CodeMCP state requires a clean install\n│\n│  ! 7 unsupported artifacts cannot be migrated\n◇  Finalizing installation\n◆  Installation finalized"
	if !strings.Contains(got, want) {
		t.Fatalf("unsupported-state presentation mismatch: %q", got)
	}
}
