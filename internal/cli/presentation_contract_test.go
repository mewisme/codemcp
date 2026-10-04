package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/logger"
)

func TestCommandPresentationContractMatrix(t *testing.T) {
	tests := []struct {
		name      string
		caps      presentation.Capabilities
		json      bool
		want      []string
		forbidden []string
	}{
		{
			name: "human-unicode",
			caps: presentation.Capabilities{Interactive: true, Width: 100, Unicode: true},
			want: []string{"┌  Contract", "✓  Ready", "│  state\n│    ready", "└  Done"},
		},
		{
			name:      "human-ascii",
			caps:      presentation.Capabilities{Interactive: true, Width: 100, Unicode: false},
			want:      []string{"+  Contract", "[OK]  Ready", "|  state\n|    ready", "+  Done"},
			forbidden: []string{"┌", "│", "◆", "✓", "—"},
		},
		{
			name:      "plain-non-tty",
			caps:      presentation.Capabilities{Interactive: false, Width: 100, Unicode: true},
			want:      []string{"Contract", "✓ Ready", "state", "ready", "Done"},
			forbidden: []string{"┌", "│", "└", "◆", "\x1b["},
		},
		{
			name:      "json",
			caps:      presentation.Capabilities{Interactive: true, Width: 100, Unicode: true, Color: true},
			json:      true,
			want:      []string{"\"state\": \"ready\""},
			forbidden: []string{"Contract", "Ready", "┌", "│", "\x1b["},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			cmd := &cobra.Command{Use: "contract"}
			setCommandPresentationTitle(cmd, "Contract")
			var asJSON bool
			addJSONResultFlag(cmd, &asJSON)
			cmd.SetOut(presentation.WrapWriter(&output, test.caps))
			if test.json {
				if err := cmd.Flags().Set("json", "true"); err != nil {
					t.Fatal(err)
				}
				if err := writeResultJSON(cmd, map[string]string{"state": "ready"}); err != nil {
					t.Fatal(err)
				}
				var decoded map[string]string
				if err := json.Unmarshal(output.Bytes(), &decoded); err != nil || decoded["state"] != "ready" {
					t.Fatalf("JSON result=%q decoded=%#v err=%v", output.String(), decoded, err)
				}
			} else {
				presenter := commandPresenter(cmd)
				presenter.Status(presentation.StatusSuccess, "Ready")
				presenter.Fields(presentation.Field{Label: "state", Value: "ready"})
				commandProgressSession(cmd).SetCompletion("Done")
				closeCommandProgress(cmd, nil)
			}
			text := output.String()
			for _, want := range test.want {
				if !strings.Contains(text, want) {
					t.Fatalf("output missing %q: %q", want, text)
				}
			}
			for _, forbidden := range test.forbidden {
				if strings.Contains(text, forbidden) {
					t.Fatalf("output contains forbidden %q: %q", forbidden, text)
				}
			}
		})
	}
}

func TestJSONResultStdoutRemainsPureAcrossDiagnosticModes(t *testing.T) {
	for _, mode := range []string{"default", "verbose", "debug"} {
		t.Run(mode, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			cmd := &cobra.Command{Use: "contract"}
			addLoggingFlags(cmd)
			var asJSON bool
			addJSONResultFlag(cmd, &asJSON)
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			if err := cmd.Flags().Set("json", "true"); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "verbose":
				if err := cmd.PersistentFlags().Set("verbose", "true"); err != nil {
					t.Fatal(err)
				}
			case "debug":
				if err := cmd.PersistentFlags().Set("debug", "true"); err != nil {
					t.Fatal(err)
				}
			}
			commandLogger(cmd).Diagnostic(logger.Info, "TEST", "test.presentation", "diagnostic")
			if err := writeResultJSON(cmd, map[string]string{"secret": "<redacted>", "state": "ready"}); err != nil {
				t.Fatal(err)
			}
			closeCommandLogger(cmd)
			var decoded map[string]string
			if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
				t.Fatalf("stdout is not pure JSON: %q: %v", stdout.String(), err)
			}
			if decoded["secret"] != "<redacted>" || strings.Contains(stdout.String(), "diagnostic") {
				t.Fatalf("JSON stdout contract violated: %q", stdout.String())
			}
		})
	}
}

func TestCommandTerminalOverrideContract(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		wantColor bool
		unicode   bool
	}{
		{name: "no-color", env: map[string]string{"NO_COLOR": "1"}, wantColor: false, unicode: true},
		{name: "force-color", env: map[string]string{"FORCE_COLOR": "1"}, wantColor: true, unicode: true},
		{name: "ascii", env: map[string]string{"CM_ASCII": "1"}, wantColor: false, unicode: false},
		{name: "unicode", env: map[string]string{"CM_UNICODE": "1"}, wantColor: false, unicode: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, key := range []string{"NO_COLOR", "FORCE_COLOR", "CM_ASCII", "CM_UNICODE"} {
				t.Setenv(key, "")
			}
			for key, value := range test.env {
				t.Setenv(key, value)
			}
			var output bytes.Buffer
			cmd := &cobra.Command{Use: "cm"}
			addTerminalPresentationFlags(cmd)
			cmd.SetOut(&output)
			caps := detectCommandTerminalCapabilities(cmd, false, false)
			if caps.Color != test.wantColor || caps.Unicode != test.unicode {
				t.Fatalf("capabilities=%#v want color=%t unicode=%t", caps, test.wantColor, test.unicode)
			}
		})
	}
}
