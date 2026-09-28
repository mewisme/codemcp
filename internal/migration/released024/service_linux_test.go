//go:build linux

package released024

import "testing"

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
