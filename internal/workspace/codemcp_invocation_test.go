package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
)

func TestClassifyCodeMCPInstalledInvocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	inherited := filepath.Join(t.TempDir(), "inherited")
	local := filepath.Join(t.TempDir(), "local")
	flag := filepath.Join(t.TempDir(), "flag")
	tests := []struct {
		name       string
		command    string
		wantSource CodeMCPConfigRootSource
		wantRoot   string
	}{
		{"direct", "cm status", CodeMCPConfigRootInheritedEnv, inherited},
		{"assignment", "CM_CONFIG_DIR=" + local + " cm status", CodeMCPConfigRootCommandEnv, local},
		{"env", "env CM_CONFIG_DIR=" + local + " cm status", CodeMCPConfigRootCommandEnv, local},
		{"command", "command cm status", CodeMCPConfigRootInheritedEnv, inherited},
		{"exec", "exec cm status", CodeMCPConfigRootInheritedEnv, inherited},
		{"windows executable", "cm.exe status", CodeMCPConfigRootInheritedEnv, inherited},
		{"flag", "cm --config-dir " + flag + " status", CodeMCPConfigRootFlag, flag},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyCodeMCPInvocation(home, tt.command, inherited)
			if !got.Recognized || got.Kind != CodeMCPInvocationInstalled || got.Program != "cm" || got.ConfigRootSource != tt.wantSource {
				t.Fatalf("classification=%#v", got)
			}
			if got.EffectiveConfigRoot != canonicalRoot(tt.wantRoot) {
				t.Fatalf("root=%q want=%q", got.EffectiveConfigRoot, canonicalRoot(tt.wantRoot))
			}
		})
	}
}

func TestClassifyCodeMCPEffectsAndCanonicalInvocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, tt := range []struct {
		command                                      string
		path                                         string
		readOnly, mutation, eligible, required, hard bool
	}{
		{"cm status", "status", true, false, false, false, false},
		{"cm cfg set http.mcp.port 41001", "config set", false, true, true, true, false},
		{"cm request approve req_test", "request approve", false, true, false, false, true},
	} {
		got := ClassifyCodeMCPInvocation(home, tt.command, "")
		if !got.Recognized || got.OperationPath != tt.path || got.ReadOnly != tt.readOnly || got.Mutation != tt.mutation || got.ApprovalEligible != tt.eligible || got.ApprovalRequired != tt.required || got.HardDenied != tt.hard {
			t.Fatalf("%q => %#v", tt.command, got)
		}
		invocation := got.ControlInvocation()
		if invocation == nil || invocation.Program != "cm" || invocation.Command != tt.command {
			t.Fatalf("control invocation for %q = %#v", tt.command, invocation)
		}
	}
}

func TestClassifyCodeMCPAliasesMatchCanonicalSecurityPolicy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	for _, test := range []struct {
		name      string
		canonical string
		alias     string
	}{
		{name: "config mutation", canonical: "cm config set http.mcp.port 41001", alias: "cm cfg set http.mcp.port 41001"},
		{name: "upstream destructive", canonical: "cm upstream server remove github", alias: "cm ups server rm github"},
		{name: "workspace destructive", canonical: "cm workspace container delete wsc_test", alias: "cm ws ctr rm wsc_test"},
		{name: "telegram mutation", canonical: "cm telegram token remove", alias: "cm tg token rm"},
	} {
		t.Run(test.name, func(t *testing.T) {
			canonical := ClassifyCodeMCPInvocation(home, test.canonical, "")
			alias := ClassifyCodeMCPInvocation(home, test.alias, "")
			if !canonical.Recognized || !alias.Recognized {
				t.Fatalf("classification missing: canonical=%#v alias=%#v", canonical, alias)
			}
			if alias.OperationPath != canonical.OperationPath ||
				alias.ReadOnly != canonical.ReadOnly ||
				alias.Mutation != canonical.Mutation ||
				alias.ApprovalEligible != canonical.ApprovalEligible ||
				alias.ApprovalRequired != canonical.ApprovalRequired ||
				alias.HardDenied != canonical.HardDenied ||
				alias.UsesDefaultRoot != canonical.UsesDefaultRoot {
				t.Fatalf("security policy drifted: canonical=%#v alias=%#v", canonical, alias)
			}
		})
	}
}

func TestClassifyCodeMCPSourceRunAliasMatchesCanonicalSecurityPolicy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := writeCodeMCPModuleFixture(t, codeMCPModulePath)

	canonical := ClassifyCodeMCPInvocation(root, "go run . config set http.mcp.port 41001", "")
	alias := ClassifyCodeMCPInvocation(root, "go run . cfg set http.mcp.port 41001", "")
	if !canonical.Recognized || !alias.Recognized || canonical.Kind != CodeMCPInvocationSourceRun || alias.Kind != CodeMCPInvocationSourceRun {
		t.Fatalf("source-run classification missing: canonical=%#v alias=%#v", canonical, alias)
	}
	if alias.OperationPath != canonical.OperationPath ||
		alias.ReadOnly != canonical.ReadOnly ||
		alias.Mutation != canonical.Mutation ||
		alias.ApprovalEligible != canonical.ApprovalEligible ||
		alias.ApprovalRequired != canonical.ApprovalRequired ||
		alias.HardDenied != canonical.HardDenied {
		t.Fatalf("source-run security policy drifted: canonical=%#v alias=%#v", canonical, alias)
	}
}

func TestClassifyCodeMCPApprovalRequirementFollowsConfigRootIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := writeCodeMCPModuleFixture(t, codeMCPModulePath)
	isolate := filepath.Join(t.TempDir(), "isolated")
	for _, command := range []string{
		"cm config set http.mcp.port 41001",
		"go run . config set http.mcp.port 41001",
	} {
		protected := ClassifyCodeMCPInvocation(root, command, "")
		if !protected.Recognized || !protected.ApprovalRequired || !protected.UsesDefaultRoot {
			t.Fatalf("protected %q => %#v", command, protected)
		}
		isolated := ClassifyCodeMCPInvocation(root, "CM_CONFIG_DIR="+isolate+" "+command, "")
		if !isolated.Recognized || isolated.ApprovalRequired || isolated.UsesDefaultRoot {
			t.Fatalf("isolated %q => %#v", command, isolated)
		}
		if protected.OperationPath != isolated.OperationPath || protected.ReadOnly != isolated.ReadOnly || protected.Mutation != isolated.Mutation || protected.ApprovalEligible != isolated.ApprovalEligible {
			t.Fatalf("config root changed canonical effect for %q: protected=%#v isolated=%#v", command, protected, isolated)
		}
	}
}

func TestClassifyCodeMCPSourceRunRecognizesSupportedForms(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := writeCodeMCPModuleFixture(t, codeMCPModulePath)
	inherited := filepath.Join(t.TempDir(), "inherited")
	local := filepath.Join(t.TempDir(), "local")
	flag := filepath.Join(t.TempDir(), "flag")
	tests := []struct {
		name       string
		command    string
		inherited  string
		wantSource CodeMCPConfigRootSource
		wantRoot   string
	}{
		{"bare", "go run . status", "", CodeMCPConfigRootDefault, configformat.DefaultRootPath()},
		{"assignment", "CM_CONFIG_DIR=" + local + " go run . status", inherited, CodeMCPConfigRootCommandEnv, local},
		{"env", "env CM_CONFIG_DIR=" + local + " go run . status", inherited, CodeMCPConfigRootCommandEnv, local},
		{"command", "command go run . status", inherited, CodeMCPConfigRootInheritedEnv, inherited},
		{"exec", "exec go run . status", inherited, CodeMCPConfigRootInheritedEnv, inherited},
		{"flag separated", "CM_CONFIG_DIR=" + local + " go run . --config-dir " + flag + " status", inherited, CodeMCPConfigRootFlag, flag},
		{"flag equals", "env CM_CONFIG_DIR=" + local + " go run . status --config-dir=" + flag, inherited, CodeMCPConfigRootFlag, flag},
		{"go equals flag", "go run -mod=mod . status", inherited, CodeMCPConfigRootInheritedEnv, inherited},
		{"go value flag", "go run -exec wrapper . status", inherited, CodeMCPConfigRootInheritedEnv, inherited},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyCodeMCPInvocation(root, tt.command, tt.inherited)
			if !got.Recognized || got.Kind != CodeMCPInvocationSourceRun || got.ModuleRoot != canonicalRoot(root) || got.SourceTarget != "." || got.ConfigRootSource != tt.wantSource {
				t.Fatalf("classification=%#v", got)
			}
			if got.EffectiveConfigRoot != canonicalRoot(tt.wantRoot) {
				t.Fatalf("root=%q want=%q", got.EffectiveConfigRoot, canonicalRoot(tt.wantRoot))
			}
		})
	}
}

func TestClassifyCodeMCPSourceRunRecognizesParentAndExplicitLocalModulePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := writeCodeMCPModuleFixture(t, codeMCPModulePath)
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		cwd, command string
	}{
		{nested, "go run .. status"},
		{filepath.Dir(root), "go run " + root + " status"},
	} {
		got := ClassifyCodeMCPInvocation(fixture.cwd, fixture.command, "")
		if !got.Recognized || got.Kind != CodeMCPInvocationSourceRun || got.ModuleRoot != canonicalRoot(root) || got.OperationPath != "status" {
			t.Fatalf("%q => %#v", fixture.command, got)
		}
	}
}

func TestClassifyCodeMCPSourceRunNestedShellAndConfigPrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := writeCodeMCPModuleFixture(t, codeMCPModulePath)
	isolate := filepath.Join(t.TempDir(), "isolated")
	tests := []struct {
		command     string
		inherited   string
		wantSource  CodeMCPConfigRootSource
		defaultRoot bool
		wantNested  bool
	}{
		{"bash -lc \"go run . status\"", "", CodeMCPConfigRootDefault, true, true},
		{"sh -c \"go run . status\"", isolate, CodeMCPConfigRootInheritedEnv, false, true},
		{"command bash -lc \"go run . status\"", isolate, CodeMCPConfigRootInheritedEnv, false, true},
		{"exec bash -lc \"go run . status\"", isolate, CodeMCPConfigRootInheritedEnv, false, true},
		{"CM_CONFIG_DIR=" + isolate + " bash -lc \"go run . status\"", "", CodeMCPConfigRootCommandEnv, false, true},
		{"env -u CM_CONFIG_DIR sh -c \"go run . status\"", isolate, CodeMCPConfigRootDefault, true, true},
		{"env --unset=CM_CONFIG_DIR sh -c \"go run . status\"", isolate, CodeMCPConfigRootDefault, true, true},
		{"env -i sh -c \"go run . status\"", isolate, CodeMCPConfigRootDefault, true, true},
		{"cmd /c \"go run . status\"", "", CodeMCPConfigRootDefault, true, true},
		{"pwsh -Command \"go run . status\"", isolate, CodeMCPConfigRootInheritedEnv, false, true},
		{"go.exe run . status", isolate, CodeMCPConfigRootInheritedEnv, false, false},
	}
	for _, tt := range tests {
		got := ClassifyCodeMCPInvocation(root, tt.command, tt.inherited)
		if !got.Recognized || got.Nested != tt.wantNested || got.ConfigRootSource != tt.wantSource || got.UsesDefaultRoot != tt.defaultRoot {
			t.Fatalf("%q => %#v", tt.command, got)
		}
	}
}

func TestCodeMCPConfigDirFlagAcceptsPlatformIndependentPathShapes(t *testing.T) {
	for _, args := range [][]string{
		{"--config-dir", "/tmp/cm-isolated"},
		{"--config-dir=C:/cm-isolated"},
		{"--config-dir=C:\\cm-isolated"},
	} {
		value, changed, empty := codeMCPConfigDirFlag(args)
		if !changed || empty || value == "" {
			t.Fatalf("config-dir args=%#v value=%q changed=%t empty=%t", args, value, changed, empty)
		}
	}
}

func TestClassifyCodeMCPSourceRunRejectsUnrelatedModulesNestedDotAndPayloadText(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := writeCodeMCPModuleFixture(t, codeMCPModulePath)
	unrelated := writeCodeMCPModuleFixture(t, "example.com/unrelated")
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	fixtures := []struct {
		cwd, command string
	}{
		{unrelated, "go run . status"},
		{nested, "go run . status"},
		{root, "echo \"go run . status\""},
		{root, "python -c 'print(\"go run . status\")'"},
		{root, "node -e 'console.log(\"go run . status\")'"},
		{root, "go run main.go"},
		{root, "printf 'cm config set http.mcp.port 41001'"},
		{root, "grep 'cm config set' README.md"},
	}
	for _, fixture := range fixtures {
		if got := ClassifyCodeMCPInvocation(fixture.cwd, fixture.command, ""); got.Recognized {
			t.Fatalf("%q unexpectedly classified: %#v", fixture.command, got)
		}
	}
}

func TestClassifyCodeMCPConfigRootPrecedenceAndDefaultAlias(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := writeCodeMCPModuleFixture(t, codeMCPModulePath)
	defaultRoot := configformat.DefaultRootPath()
	if err := os.MkdirAll(defaultRoot, 0700); err != nil {
		t.Fatal(err)
	}
	inherited := filepath.Join(t.TempDir(), "inherited")
	local := filepath.Join(t.TempDir(), "local")
	flag := filepath.Join(t.TempDir(), "flag")
	got := ClassifyCodeMCPInvocation(root, "CM_CONFIG_DIR="+local+" go run . --config-dir="+flag+" status", inherited)
	if !got.Recognized || got.ConfigRootSource != CodeMCPConfigRootFlag || got.EffectiveConfigRoot != canonicalRoot(flag) {
		t.Fatalf("flag precedence=%#v", got)
	}
	got = ClassifyCodeMCPInvocation(root, "go run . --config-dir= status", inherited)
	if !got.Recognized || got.ConfigRootSource != CodeMCPConfigRootEmptyFlag || !got.ExplicitEmptyFlag || !got.UsesDefaultRoot {
		t.Fatalf("empty flag=%#v", got)
	}
	if os.PathSeparator != '\\' {
		alias := filepath.Join(t.TempDir(), "default-alias")
		if err := os.Symlink(defaultRoot, alias); err == nil {
			got = ClassifyCodeMCPInvocation(root, "go run . --config-dir="+alias+" status", "")
			if !got.Recognized || !got.UsesDefaultRoot {
				t.Fatalf("default alias=%#v", got)
			}
		}
	}
}

func TestClassifyCodeMCPRelativeAndWindowsConfigRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	defaultRelative := ClassifyCodeMCPInvocation(home, "CM_CONFIG_DIR=.cm cm config set http.mcp.port 41001", "")
	if !defaultRelative.Recognized || !defaultRelative.UsesDefaultRoot {
		t.Fatalf("relative default=%#v", defaultRelative)
	}
	isolated := ClassifyCodeMCPInvocation(home, "CM_CONFIG_DIR=./tmp/cm cm config set http.mcp.port 41001", "")
	if !isolated.Recognized || isolated.UsesDefaultRoot || isolated.EffectiveConfigRoot != canonicalRoot(filepath.Join(home, "tmp/cm")) {
		t.Fatalf("relative isolated=%#v", isolated)
	}
	windows := ClassifyCodeMCPInvocation(home, "CM_CONFIG_DIR='C:\\\\cm-test' cm config set http.mcp.port 41001", "")
	if !windows.Recognized || windows.UsesDefaultRoot {
		t.Fatalf("windows root=%#v", windows)
	}
}

func writeCodeMCPModuleFixture(t *testing.T, module string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+module+"\n\ngo 1.27\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}
