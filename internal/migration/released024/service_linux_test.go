//go:build linux

package released024

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspectSystemdOwnershipRequiresReleasedRootExecutableAndConfigArg(t *testing.T) {
	descriptor := SourceDescriptor{
		Root:       "/home/user/.config/chatgpt-mcp",
		BinaryName: "chatgpt-mcp",
	}
	valid := "[Service]\nExecStart=/home/user/.chatgpt-mcp/current/chatgpt-mcp --config-dir /home/user/.config/chatgpt-mcp _service run --service-id chatgpt-mcp-user-test\n"
	ownership, reason := inspectSystemdOwnership(valid, descriptor)
	if ownership != OwnershipVerified || reason == "" {
		t.Fatalf("valid definition ownership=%q reason=%q", ownership, reason)
	}
	for _, definition := range []string{
		"[Service]\nExecStart=/usr/local/bin/chatgpt-mcp --config-dir /other/root _service run\n",
		"[Service]\nExecStart=/home/user/.chatgpt-mcp/current/not-chatgpt-mcp --config-dir /home/user/.config/chatgpt-mcp _service run\n",
		"[Service]\nExecStart=/home/user/.chatgpt-mcp/current/chatgpt-mcp _service run\n",
	} {
		ownership, _ := inspectSystemdOwnership(definition, descriptor)
		if ownership != OwnershipAmbiguous {
			t.Fatalf("conflicting definition was claimed: ownership=%q definition=%q", ownership, definition)
		}
	}
}

func TestVerifyHistoricalServiceDefinitionRejectsDefinitionReplacement(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".config", "chatgpt-mcp")
	binary := filepath.Join(t.TempDir(), "chatgpt-mcp")
	definition := filepath.Join(t.TempDir(), "chatgpt-mcp.service")
	descriptor := SourceDescriptor{Root: root, BinaryName: "chatgpt-mcp"}
	state := ServiceState{ID: "chatgpt-mcp-user-test", ConfigRoot: root, Binary: binary, DefinitionPath: definition}
	valid := "[Service]\nExecStart=" + binary + " --config-dir " + root + " _service run --service-id " + state.ID + "\n"
	if err := os.WriteFile(definition, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyHistoricalServiceDefinition(descriptor, state); err != nil {
		t.Fatalf("valid definition rejected: %v", err)
	}
	replaced := "[Service]\nExecStart=" + binary + " --config-dir " + filepath.Join(t.TempDir(), "other") + " _service run --service-id " + state.ID + "\n"
	if err := os.WriteFile(definition, []byte(replaced), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyHistoricalServiceDefinition(descriptor, state); err == nil {
		t.Fatal("changed historical service definition was accepted")
	}
}
