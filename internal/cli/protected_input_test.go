package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"golang.org/x/term"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	typesafeintegration "go.mewis.me/codemcp/internal/integrations/typesafe"
	"go.mewis.me/codemcp/internal/secretstore"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func TestProtectedTerminalInputMasksSameLineHandlesBackspaceAndRestores(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var output bytes.Buffer
	presenter := presentation.New(&output, presentation.ModeHuman, presentation.Capabilities{Unicode: true, Interactive: true, CursorControl: true})
	restores := installProtectedTerminalStubs(t)

	go func() {
		_, _ = writer.Write([]byte{'s', 'e', 'c', 'r', 'x', 0x7f, 'e', 't', '\r'})
		_ = writer.Close()
	}()
	value, err := readProtectedTerminal(reader, presenter, "API key", 64)
	if err != nil {
		t.Fatal(err)
	}
	if value != "secret" {
		t.Fatalf("value=%q", value)
	}
	if *restores != 1 {
		t.Fatalf("terminal restore count=%d", *restores)
	}
	rendered := output.String()
	if !strings.Contains(rendered, "\r\x1b[2K") || !strings.Contains(rendered, "API key: ******") {
		t.Fatalf("protected rendering=%q", rendered)
	}
	for _, raw := range []string{"secret", "secr", "secrx"} {
		if strings.Contains(rendered, raw) {
			t.Fatalf("raw input %q leaked into rendering %q", raw, rendered)
		}
	}
}

func TestProtectedTerminalInputHandlesANSIForwardDelete(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	installProtectedTerminalStubs(t)
	go func() {
		_, _ = writer.Write([]byte{'s', 'e', 'c', 'r', 'e', 'x', 0x1b, '[', '3', '~', 't', '\r'})
		_ = writer.Close()
	}()
	value, err := readProtectedTerminal(reader, nil, "Secret", 64)
	if err != nil {
		t.Fatal(err)
	}
	if value != "secret" {
		t.Fatalf("value=%q", value)
	}
}

func TestProtectedTerminalInputCancelAndEOFRestoreWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input []byte
		want  error
	}{
		{name: "ctrl-c", input: append([]byte("partial"), byte(3)), want: errProtectedInputCancelled},
		{name: "ctrl-d", input: append([]byte("partial"), byte(4))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			restores := installProtectedTerminalStubs(t)
			go func() {
				_, _ = writer.Write(tc.input)
				_ = writer.Close()
			}()
			_, err = readProtectedTerminal(reader, nil, "Secret", 64)
			if err == nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want=%v", err, tc.want)
			}
			if *restores != 1 {
				t.Fatalf("terminal restore count=%d", *restores)
			}
		})
	}

	root := isolateProtectedInputCLI(t)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	oldTerminal := protectedInputIsTerminal
	protectedInputIsTerminal = func(int) bool { return true }
	t.Cleanup(func() { protectedInputIsTerminal = oldTerminal })
	installProtectedTerminalStubs(t)
	go func() {
		_, _ = writer.Write(append([]byte("must-not-store"), byte(3)))
		_ = writer.Close()
	}()
	_, _, err = executeProtectedInputCLI(root, reader, "integration", "typesafe", "key", "set")
	if !errors.Is(err, errProtectedInputCancelled) {
		t.Fatalf("cancelled setter err=%v", err)
	}
	if _, err := typesafeintegration.LoadAPIKey(root); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("cancelled input mutated TypeSafe credential: %v", err)
	}
}

func TestProtectedTerminalInputReadErrorRestoresTerminal(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	restores := 0
	oldMakeRaw, oldRestore := protectedInputMakeRaw, protectedInputRestore
	protectedInputMakeRaw = func(int) (*term.State, error) {
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		return nil, nil
	}
	protectedInputRestore = func(int, *term.State) error { restores++; return nil }
	t.Cleanup(func() {
		protectedInputMakeRaw = oldMakeRaw
		protectedInputRestore = oldRestore
	})
	if _, err := readProtectedTerminal(reader, nil, "Secret", 64); err == nil || !strings.Contains(err.Error(), "read protected terminal input") {
		t.Fatalf("read error=%v", err)
	}
	if restores != 1 {
		t.Fatalf("terminal restore count=%d", restores)
	}
}

func TestProtectedSecretSourcesConvergeAndNeverRenderRawValue(t *testing.T) {
	root := isolateProtectedInputCLI(t)
	const secret = "typesafe-secret-argument-stdin-env"
	const envName = "CODEMCP_TEST_PROTECTED_SECRET"
	t.Setenv(envName, secret)

	tests := []struct {
		name  string
		input io.Reader
		args  []string
	}{
		{name: "argument", args: []string{"integration", "typesafe", "key", "set", secret}},
		{name: "stdin", input: strings.NewReader(secret + "\n"), args: []string{"integration", "typesafe", "key", "set"}},
		{name: "environment", args: []string{"integration", "typesafe", "key", "set", "--from-env", envName}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := typesafeintegration.UpdateAPIKey(root, ""); err != nil {
				t.Fatal(err)
			}
			stdout, stderr, err := executeProtectedInputCLI(root, tc.input, tc.args...)
			if err != nil {
				t.Fatalf("execute: %v stderr=%q", err, stderr)
			}
			stored, err := typesafeintegration.LoadAPIKey(root)
			if err != nil || stored != secret {
				t.Fatalf("stored=%q err=%v", stored, err)
			}
			if strings.Contains(stdout+stderr, secret) {
				t.Fatalf("raw secret leaked to command output: %q", stdout+stderr)
			}
			if leakedFile := findStringInTree(t, root, secret); leakedFile != "" {
				t.Fatalf("raw secret leaked into config/log/trace file %s", leakedFile)
			}
		})
	}
}

func TestProtectedInputRejectsEmptyAndOversizedBeforeMutation(t *testing.T) {
	root := isolateProtectedInputCLI(t)
	for _, tc := range []struct {
		name  string
		input string
	}{
		{name: "empty", input: "\n"},
		{name: "oversized", input: strings.Repeat("x", maxProtectedInputBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := executeProtectedInputCLI(root, strings.NewReader(tc.input), "integration", "typesafe", "key", "set")
			if err == nil {
				t.Fatal("invalid protected input was accepted")
			}
			if _, err := typesafeintegration.LoadAPIKey(root); !errors.Is(err, secretstore.ErrNotFound) {
				t.Fatalf("invalid protected input mutated TypeSafe credential: %v", err)
			}
		})
	}
}

func TestWritableManagedSecretInventoryHasProtectedConfigSetPath(t *testing.T) {
	root := isolateProtectedInputCLI(t)
	service := application.NewSettingService()
	const secret = "managed-secret-inventory-value"
	writableSecrets := 0
	for _, spec := range config.Settings() {
		if !spec.Secret || !spec.Writable {
			continue
		}
		writableSecrets++
		key := spec.Key
		if spec.Selector != nil {
			key = strings.Replace(spec.Selector.Template, "<id>", "ollama", 1)
		}
		stdout, stderr, err := executeProtectedInputCLI(root, strings.NewReader(secret+"\n"), "config", "set", key)
		if err != nil {
			t.Fatalf("protected config set %s: %v stderr=%q", key, err, stderr)
		}
		if strings.Contains(stdout+stderr, secret) {
			t.Fatalf("raw %s secret leaked to output", key)
		}
		result, err := service.Present(context.Background(), key)
		if err != nil {
			t.Fatalf("present %s: %v", key, err)
		}
		if result.Configured == nil || !*result.Configured || result.Value != tracepkg.MaskSecret(secret, true) {
			t.Fatalf("present %s=%#v want canonical masked preview", key, result)
		}
		if _, err := service.Unset(context.Background(), key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
		cleared, err := service.Present(context.Background(), key)
		if err != nil || cleared.Configured == nil || *cleared.Configured || cleared.Value != "not configured" {
			t.Fatalf("cleared %s=%#v err=%v", key, cleared, err)
		}
	}
	if writableSecrets == 0 {
		t.Fatal("managed-secret inventory unexpectedly empty")
	}

	for _, key := range []string{"http.mcp.auth.token", "http.admin.auth.token"} {
		spec, ok := config.SettingByKey(key)
		if !ok || !spec.Secret || spec.Writable || !spec.Rotatable || spec.ValueRole != config.SettingValueGenerated {
			t.Fatalf("generated credential metadata drift for %s: %#v", key, spec)
		}
		if _, _, err := executeProtectedInputCLI(root, strings.NewReader("must-not-be-read\n"), "config", "set", key); err == nil {
			t.Fatalf("generated credential %s accepted user-supplied input", key)
		}
	}
	for _, path := range []string{"auth mcp create", "auth admin create"} {
		command := commandByRelativePath(newRootCommand(), path)
		if command == nil {
			t.Fatalf("generated credential command %q missing", path)
		}
		if err := command.Args(command, []string{"user-supplied-secret"}); err == nil {
			t.Fatalf("generated credential command %q accepted user-supplied input", path)
		}
		if command.Flags().Lookup("from-env") != nil {
			t.Fatalf("generated credential command %q unexpectedly exposes --from-env", path)
		}
	}
}

func TestDedicatedManagedSecretSettersExposeProtectedNoValueAndEnvironmentPaths(t *testing.T) {
	root := newRootCommand()
	tests := []struct {
		path string
		args []string
	}{
		{path: "telegram token set"},
		{path: "integration typesafe key set"},
		{path: "tunnel key set"},
		{path: "tunnel admin key set"},
		{path: "llm ollama key set"},
		{path: "llm ollama key set"},
		{path: "llm provider key set", args: []string{"ollama"}},
	}
	for _, tc := range tests {
		command := commandByRelativePath(root, tc.path)
		if command == nil {
			t.Fatalf("command %q missing", tc.path)
		}
		if err := command.Args(command, tc.args); err != nil {
			t.Fatalf("command %q rejects protected no-value form: %v", tc.path, err)
		}
		if command.Flags().Lookup("from-env") == nil {
			t.Fatalf("command %q missing --from-env", tc.path)
		}
		if !strings.Contains(strings.ToLower(command.Long), "process listings") {
			t.Fatalf("command %q help does not warn about explicit secret arguments", tc.path)
		}
	}
}

func installProtectedTerminalStubs(t *testing.T) *int {
	t.Helper()
	oldMakeRaw, oldRestore := protectedInputMakeRaw, protectedInputRestore
	restores := 0
	protectedInputMakeRaw = func(int) (*term.State, error) { return nil, nil }
	protectedInputRestore = func(int, *term.State) error { restores++; return nil }
	t.Cleanup(func() {
		protectedInputMakeRaw = oldMakeRaw
		protectedInputRestore = oldRestore
	})
	return &restores
}

func isolateProtectedInputCLI(t *testing.T) string {
	t.Helper()
	root := isolateUniversalConfigCLI(t)
	t.Setenv(configformat.EnvConfigDir, root)
	restore := secretstore.UseMemoryForTesting()
	t.Cleanup(restore)
	return root
}

func executeProtectedInputCLI(root string, input io.Reader, args ...string) (string, string, error) {
	var stdout, stderr bytes.Buffer
	cmd := newRootCommand()
	cmd.SetContext(context.Background())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if input != nil {
		cmd.SetIn(input)
	}
	cmd.SetArgs(append([]string{"--config-dir", root}, args...))
	err := executeCommand(cmd)
	return stdout.String(), stderr.String(), err
}
