package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/doctor"
)

func TestDoctorOutputModesAreDeterministic(t *testing.T) {
	original := newDoctorService
	newDoctorService = func() (*application.DoctorService, error) {
		return deterministicDoctorService(t), nil
	}
	t.Cleanup(func() { newDoctorService = original })

	rootPath := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, rootPath)
	t.Cleanup(func() { _ = configformat.SetRootPath("") })

	plainOne := executeDoctorFixture(t, rootPath, false, false)
	plainTwo := executeDoctorFixture(t, rootPath, false, false)
	if plainOne != plainTwo {
		t.Fatalf("plain doctor output is not deterministic\nfirst=%q\nsecond=%q", plainOne, plainTwo)
	}
	for _, want := range []string{"Config", "config.overview", "state", "healthy", "Runtime", "runtime.control", "action", "Start the managed runtime", "Summary"} {
		if !strings.Contains(plainOne, want) {
			t.Fatalf("plain output missing %q: %q", want, plainOne)
		}
	}
	if strings.Contains(plainOne, "┌") || strings.Contains(plainOne, "│") {
		t.Fatalf("plain output contains interactive rails: %q", plainOne)
	}

	human := executeDoctorFixture(t, rootPath, true, false)
	for _, want := range []string{"┌  CodeMCP doctor", "│  ▸ Config", "│  ▸ config.overview", "│  ▸ Runtime", "│  ▸ runtime.control", "└  Done"} {
		if !strings.Contains(human, want) {
			t.Fatalf("human output missing %q: %q", want, human)
		}
	}

	jsonText := executeDoctorFixture(t, rootPath, false, true)
	var report doctor.Report
	if err := json.Unmarshal([]byte(jsonText), &report); err != nil {
		t.Fatalf("decode JSON output: %v\n%s", err, jsonText)
	}
	if len(report.Components) != 2 || report.Components[0].ID != doctor.ComponentConfigOverview || report.Components[1].ID != doctor.ComponentRuntimeControl {
		t.Fatalf("json report=%#v", report)
	}
	if strings.Contains(jsonText, "CodeMCP doctor") || strings.Contains(jsonText, "◆") {
		t.Fatalf("JSON output contains presentation text: %q", jsonText)
	}
}

func TestDoctorUsesDefaultCobraHelp(t *testing.T) {
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"doctor", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{"Diagnose CodeMCP system health", "Usage:", "cm doctor [flags]", "Flags:", "--json"} {
		if !strings.Contains(text, want) {
			t.Fatalf("help missing %q: %q", want, text)
		}
	}
	for _, forbidden := range []string{"┌", "└", "◆"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("default Cobra help contains workflow glyph %q: %q", forbidden, text)
		}
	}
}

func TestDoctorRunsBeforeAndAfterInitializationWithoutMutation(t *testing.T) {
	original := newDoctorService
	newDoctorService = func() (*application.DoctorService, error) {
		return application.NewDefaultDoctorService()
	}
	t.Cleanup(func() { newDoctorService = original })

	rootPath := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, rootPath)
	t.Cleanup(func() { _ = configformat.SetRootPath("") })

	before := snapshotDoctorTree(t, rootPath)
	preInit := executeDoctorFixture(t, rootPath, false, true)
	var preReport doctor.Report
	if err := json.Unmarshal([]byte(preInit), &preReport); err != nil {
		t.Fatal(err)
	}
	if len(preReport.Components) == 0 {
		t.Fatal("pre-initialization doctor returned no diagnostics")
	}
	if after := snapshotDoctorTree(t, rootPath); !reflect.DeepEqual(before, after) {
		t.Fatalf("pre-initialization doctor mutated state\nbefore=%#v\nafter=%#v", before, after)
	}

	initRoot := newRootCommand()
	initRoot.SetOut(io.Discard)
	initRoot.SetErr(io.Discard)
	initRoot.SetArgs([]string{"--config-dir", rootPath, "init"})
	if err := executeCommand(initRoot); err != nil {
		t.Fatal(err)
	}
	initialized := snapshotDoctorTree(t, rootPath)
	if len(initialized) == 0 {
		t.Fatal("initialization created no state")
	}

	postInit := executeDoctorFixture(t, rootPath, false, true)
	var postReport doctor.Report
	if err := json.Unmarshal([]byte(postInit), &postReport); err != nil {
		t.Fatal(err)
	}
	if len(postReport.Components) == 0 {
		t.Fatal("post-initialization doctor returned no diagnostics")
	}
	if after := snapshotDoctorTree(t, rootPath); !reflect.DeepEqual(initialized, after) {
		t.Fatalf("post-initialization doctor mutated state\nbefore=%#v\nafter=%#v", initialized, after)
	}
}

func deterministicDoctorService(t *testing.T) *application.DoctorService {
	t.Helper()
	configDef, ok := doctor.DefinitionFor(doctor.ComponentConfigOverview)
	if !ok {
		t.Fatal("config definition missing")
	}
	runtimeDef, ok := doctor.DefinitionFor(doctor.ComponentRuntimeControl)
	if !ok {
		t.Fatal("runtime definition missing")
	}
	service, err := application.NewDoctorServiceWithProviders(
		doctor.ProviderFunc{
			Definition: configDef.ProviderSpec(),
			Run: func(context.Context) (doctor.Component, error) {
				return doctor.Component{
					State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "configuration is readable",
					Flags: []doctor.Flag{{ID: "initialized", Value: true}},
				}, nil
			},
		},
		doctor.ProviderFunc{
			Definition: runtimeDef.ProviderSpec(),
			Run: func(context.Context) (doctor.Component, error) {
				return doctor.Component{
					State: doctor.StateDegraded, Severity: doctor.SeverityWarning, Summary: "managed runtime is stopped",
					Remediations: []doctor.Remediation{{ID: "runtime_start", Summary: "Start the managed runtime", Operation: "runtime.start"}},
				}, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func executeDoctorFixture(t *testing.T, rootPath string, interactive, jsonOutput bool) string {
	t.Helper()
	var output bytes.Buffer
	var writer io.Writer = &output
	if interactive {
		writer = presentation.WrapWriter(&output, presentation.Capabilities{
			StdoutTTY: true, StderrTTY: true, Width: 100, Unicode: true, RawUnicode: true, Interactive: true,
		})
	}
	root := newRootCommand()
	root.SetOut(writer)
	root.SetErr(writer)
	args := []string{"--config-dir", rootPath, "doctor"}
	if jsonOutput {
		args = append(args, "--json")
	}
	root.SetArgs(args)
	if err := executeCommand(root); err != nil {
		t.Fatal(err)
	}
	return output.String()
}

func snapshotDoctorTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	result := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = bytes.Clone(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
