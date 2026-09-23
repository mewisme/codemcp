package tools

import (
	"encoding/json"
	"strings"
	"testing"

	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
)

func TestObservedRunCommandResultRedactsOutput(t *testing.T) {
	result := JSONResult(shellruntime.ExecResult{Command: "printf secret", CWD: "/workspace", Stdout: "secret-output", Stderr: "secret-error", ExitCode: 7})
	data, err := json.Marshal(observedResult("run_command", result))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{"secret-output", "secret-error", `"stdout"`, `"stderr"`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("run_command observation leaked %q: %s", forbidden, text)
		}
	}
	for _, expected := range []string{"printf secret", "/workspace", `"exit_code":7`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("run_command observation missing %q: %s", expected, text)
		}
	}
}
