package tools

import (
	"encoding/json"
	"strings"
	"testing"

	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
)

func TestObservedRunCommandResultCarriesCanonicalOutputForDiagnosticProjection(t *testing.T) {
	result := JSONResult(shellruntime.ExecResult{Command: "printf output", CWD: "/workspace", Stdout: "command-output", Stderr: "command-error", ExitCode: 7})
	data, err := json.Marshal(observedResult("run_command", result))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{"printf output", "/workspace", "command-output", "command-error", `"stdout"`, `"stderr"`, `"exit_code":7`, `"structured_content"`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("run_command observation missing %q: %s", expected, text)
		}
	}
}
