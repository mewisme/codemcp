package install

import (
	"path/filepath"
	"testing"
)

func TestDetectDevelopmentBuild(t *testing.T) {
	layout, err := NewLayout(filepath.Join(t.TempDir(), "install"), filepath.Join(t.TempDir(), "bin"))
	if err != nil {
		t.Fatal(err)
	}
	for _, buildVersion := range []string{"dev", "0.0.1-dev"} {
		detection := detect(filepath.Join(t.TempDir(), layout.BinaryName), buildVersion, layout, "", "", "", "")
		if detection.Method != MethodDevelopment {
			t.Fatalf("version=%q method=%q", buildVersion, detection.Method)
		}
	}
}

func TestDetectDirectInstall(t *testing.T) {
	root := filepath.Join(t.TempDir(), "custom")
	layout, err := NewLayout(filepath.Join(t.TempDir(), "default"), filepath.Join(t.TempDir(), "bin"))
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "versions", "v1.2.3", layout.BinaryName)
	detection := detect(executable, "v1.2.3", layout, "", "", "", "")
	if detection.Method != MethodDirect || detection.Root != root {
		t.Fatalf("detection = %+v", detection)
	}
}

func TestDetectPackageManagersBeforeDirectShape(t *testing.T) {
	layout, err := NewLayout(filepath.Join(t.TempDir(), "default"), filepath.Join(t.TempDir(), "bin"))
	if err != nil {
		t.Fatal(err)
	}
	homebrew := filepath.Join(string(filepath.Separator), "opt", "homebrew", "Caskroom", "codemcp", "1.2.3", layout.BinaryName)
	if detection := detect(homebrew, "v1.2.3", layout, "", "", "", ""); detection.Method != MethodHomebrew {
		t.Fatalf("homebrew method = %q", detection.Method)
	}
	scoop := filepath.Join(string(filepath.Separator), "Users", "Mew", "scoop", "apps", "codemcp", "current", layout.BinaryName)
	if detection := detect(scoop, "v1.2.3", layout, "", "", "", ""); detection.Method != MethodScoop {
		t.Fatalf("scoop method = %q", detection.Method)
	}
}

func TestApplyCurrentPackageOwnershipOnlyPromotesCanonicalLinuxPackageMethods(t *testing.T) {
	standalone := Detection{Method: MethodStandalone, Executable: "/usr/bin/cm"}
	for _, method := range []Method{MethodDebian, MethodRPM} {
		got := applyCurrentPackageOwnership(standalone, standalone.Executable, func(path string) Method {
			if path != standalone.Executable {
				t.Fatalf("probe path = %q", path)
			}
			return method
		})
		if got.Method != method {
			t.Fatalf("method = %q, want %q", got.Method, method)
		}
	}
	for _, method := range []Method{MethodUnknown, MethodGo, MethodStandalone} {
		got := applyCurrentPackageOwnership(standalone, standalone.Executable, func(string) Method { return method })
		if got.Method != MethodStandalone {
			t.Fatalf("untrusted probe method %q promoted to %q", method, got.Method)
		}
	}
	for _, existing := range []Method{MethodHomebrew, MethodScoop, MethodDirect, MethodDevelopment, MethodGo} {
		got := applyCurrentPackageOwnership(Detection{Method: existing, Executable: "/path/cm"}, "/path/cm", func(string) Method {
			return MethodDebian
		})
		if got.Method != existing {
			t.Fatalf("existing precedence %q changed to %q", existing, got.Method)
		}
	}
}

func TestLegacyScoopPackagePathIsMigrationOnly(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "Users", "Mew", "scoop")
	legacy := filepath.Join(root, "apps", "chatgpt-mcp", "current", "chatgpt-mcp.exe")
	if isScoopPath(legacy, root) {
		t.Fatal("current Scoop detector accepted legacy package identity")
	}
	if !isLegacyScoopPackagePath(legacy, root) {
		t.Fatal("legacy Scoop package path was not recognized by migration detector")
	}
}

func TestDetectGoAndStandaloneInstall(t *testing.T) {
	home := t.TempDir()
	layout, err := NewLayout(filepath.Join(home, ".cm"), filepath.Join(home, ".local", "bin"))
	if err != nil {
		t.Fatal(err)
	}
	goExecutable := filepath.Join(home, "go", "bin", layout.BinaryName)
	if detection := detect(goExecutable, "v1.2.3", layout, home, "", "", ""); detection.Method != MethodGo {
		t.Fatalf("go method = %q", detection.Method)
	}
	standalone := filepath.Join(home, "Downloads", layout.BinaryName)
	if detection := detect(standalone, "v1.2.3", layout, home, "", "", ""); detection.Method != MethodStandalone {
		t.Fatalf("standalone method = %q", detection.Method)
	}
}

func TestDetectInstalledDevelopmentBuildAsDirect(t *testing.T) {
	root := filepath.Join(t.TempDir(), "install")
	layout, err := NewLayout(root, filepath.Join(t.TempDir(), "bin"))
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "versions", "dev", layout.BinaryName)
	detection := detect(executable, "dev", layout, "", "", "", "")
	if detection.Method != MethodDirect || detection.Root != root {
		t.Fatalf("detection = %+v", detection)
	}
}
