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
	detection := detect(filepath.Join(t.TempDir(), layout.BinaryName), "dev", layout, "", "", "", "")
	if detection.Method != MethodDevelopment {
		t.Fatalf("method = %q", detection.Method)
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
	homebrew := filepath.Join(string(filepath.Separator), "opt", "homebrew", "Caskroom", "chatgpt-mcp", "1.2.3", layout.BinaryName)
	if detection := detect(homebrew, "v1.2.3", layout, "", "", "", ""); detection.Method != MethodHomebrew {
		t.Fatalf("homebrew method = %q", detection.Method)
	}
	scoop := filepath.Join(string(filepath.Separator), "Users", "Mew", "scoop", "apps", "chatgpt-mcp", "current", layout.BinaryName)
	if detection := detect(scoop, "v1.2.3", layout, "", "", "", ""); detection.Method != MethodScoop {
		t.Fatalf("scoop method = %q", detection.Method)
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
