package upstream024

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/upstream"
)

func TestTransformReleased024FixturePreservesUpstreamIntent(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	fixture, err := os.ReadFile("testdata/upstream-0.2.24.json")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	source := filepath.Join(root, "released", "upstream.json")
	destination := filepath.Join(root, "staged", "upstreams.json")
	if err := os.MkdirAll(filepath.Dir(source), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, fixture, 0600); err != nil {
		t.Fatal(err)
	}

	released, err := decodeReleased(fixture)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Transform(Input{SourcePath: source, DestinationPath: destination})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Migrated || result.AlreadyApplied || !result.SourceRemoved || result.DestinationPath != destination {
		t.Fatalf("result=%#v", result)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("released source still exists: %v", err)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if os.PathSeparator != '\\' && info.Mode().Perm() != 0600 {
		t.Fatalf("destination mode=%#o", info.Mode().Perm())
	}

	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(`"servers"`)) || !bytes.Contains(data, []byte(`"upstreams"`)) {
		t.Fatalf("destination schema=%s", data)
	}
	var current currentStore
	if err := json.Unmarshal(data, &current); err != nil {
		t.Fatal(err)
	}
	if current.Version != storeVersion || !reflect.DeepEqual(current.Upstreams, released.Servers) {
		t.Fatalf("current=%#v released=%#v", current, released)
	}
	if current.Upstreams[0].Headers["Authorization"] != "<secret-file>" || current.Upstreams[1].Env["API_TOKEN"] != "<secret-file>" {
		t.Fatalf("presentation-safe markers not preserved: %#v", current.Upstreams)
	}

	before := append([]byte(nil), data...)
	again, err := Transform(Input{SourcePath: source, DestinationPath: destination})
	if err != nil {
		t.Fatal(err)
	}
	if again.Migrated || !again.AlreadyApplied || again.SourceRemoved {
		t.Fatalf("second result=%#v", again)
	}
	after, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("idempotent migration changed destination bytes")
	}
}

func TestTransformSupportsReleasedRawArrayStore(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	source := filepath.Join(root, "upstream.json")
	destination := filepath.Join(root, "upstreams.json")
	servers := []upstream.Server{{ID: "local", Name: "Local", Transport: "stdio", Enabled: true, Command: "node", Args: []string{"server.js"}}}
	data, err := json.Marshal(servers)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Transform(Input{SourcePath: source, DestinationPath: destination}); err != nil {
		t.Fatal(err)
	}
	migrated, exists, err := readCurrentIfExists(destination)
	if err != nil || !exists || !reflect.DeepEqual(migrated.Upstreams, servers) {
		t.Fatalf("migrated=%#v exists=%t err=%v", migrated, exists, err)
	}
}

func TestTransformRefusesDestinationConflictBeforeRemovingSource(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	source := filepath.Join(root, "upstream.json")
	destination := filepath.Join(root, "upstreams.json")
	sourceData := []byte(`{"version":1,"servers":[{"id":"source","name":"Source","transport":"stdio","enabled":true,"command":"node"}]}`)
	if err := os.WriteFile(source, sourceData, 0600); err != nil {
		t.Fatal(err)
	}
	conflict := []byte(`{"version":1,"upstreams":[{"id":"owned","name":"Owned","transport":"stdio","enabled":true,"command":"node"}]}`)
	if err := os.WriteFile(destination, conflict, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Transform(Input{SourcePath: source, DestinationPath: destination}); err == nil {
		t.Fatal("destination conflict accepted")
	}
	if got, err := os.ReadFile(source); err != nil || !bytes.Equal(got, sourceData) {
		t.Fatalf("source changed on conflict: %v %s", err, got)
	}
	if got, err := os.ReadFile(destination); err != nil || !bytes.Equal(got, conflict) {
		t.Fatalf("destination changed on conflict: %v %s", err, got)
	}
}

func TestTransformErrorsDoNotExposeSensitiveValues(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	source := filepath.Join(root, "upstream.json")
	destination := filepath.Join(root, "upstreams.json")
	secret := "TOP_SECRET_VALUE"
	data := []byte(`{"version":"bad","servers":[{"id":"alpha","headers":{"Authorization":"` + secret + `"}}]}`)
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Transform(Input{SourcePath: source, DestinationPath: destination})
	if err == nil {
		t.Fatal("malformed released store accepted")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("migration error leaked sensitive value: %q", err)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination written on failure: %v", statErr)
	}
	if got, readErr := os.ReadFile(source); readErr != nil || !bytes.Equal(got, data) {
		t.Fatalf("source changed on decode failure: %v", readErr)
	}
}

func TestTransformRejectsPlaintextSensitiveStateWithoutCopyingIt(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	source := filepath.Join(root, "upstream.json")
	destination := filepath.Join(root, "upstreams.json")
	secret := "TOP_SECRET_VALUE"
	data := []byte(`{"version":1,"servers":[{"id":"alpha","name":"Alpha","transport":"http","enabled":true,"url":"https://example.test/mcp","headers":{"Authorization":"` + secret + `"}}]}`)
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Transform(Input{SourcePath: source, DestinationPath: destination})
	if err == nil {
		t.Fatal("plaintext sensitive state was accepted")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("migration error leaked sensitive value: %q", err)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination written for plaintext sensitive state: %v", statErr)
	}
	got, readErr := os.ReadFile(source)
	if readErr != nil || !bytes.Equal(got, data) {
		t.Fatalf("source changed on plaintext rejection: %v", readErr)
	}
}

func TestTransformRejectsSymlinkSourceAndDestination(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink creation may require Windows Developer Mode or elevation")
	}
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	realSource := filepath.Join(root, "real-upstream.json")
	sourceLink := filepath.Join(root, "upstream.json")
	destination := filepath.Join(root, "upstreams.json")
	if err := os.WriteFile(realSource, []byte(`{"version":1,"servers":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realSource, sourceLink); err != nil {
		t.Fatal(err)
	}
	if _, err := Transform(Input{SourcePath: sourceLink, DestinationPath: destination}); err == nil {
		t.Fatal("symlink source accepted")
	}
	if err := os.Remove(sourceLink); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourceLink, []byte(`{"version":1,"servers":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	realDestination := filepath.Join(root, "real-upstreams.json")
	if err := os.WriteFile(realDestination, []byte(`{"version":1,"upstreams":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDestination, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := Transform(Input{SourcePath: sourceLink, DestinationPath: destination}); err == nil {
		t.Fatal("symlink destination accepted")
	}
}
