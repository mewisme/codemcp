package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Detection struct {
	Method     Method
	Executable string
	Root       string
	Metadata   *Metadata
}

func (d Detection) ManagedLayout() (Layout, error) {
	if d.Method != MethodDirect || d.Metadata == nil {
		return Layout{}, errors.New("managed direct installation not found")
	}
	if d.Metadata.Method != MethodDirect {
		return Layout{}, fmt.Errorf("install metadata method is %q, want direct", d.Metadata.Method)
	}
	binDir := strings.TrimSpace(d.Metadata.BinDir)
	if binDir == "" {
		defaults, err := DefaultLayout()
		if err != nil {
			return Layout{}, err
		}
		binDir = defaults.BinDir
	}
	layout, err := NewLayout(d.Metadata.InstallDir, binDir)
	if err != nil {
		return Layout{}, err
	}
	if d.Root != "" && !samePath(d.Root, layout.Root) {
		return Layout{}, fmt.Errorf("install metadata root %s does not match detected root %s", layout.Root, d.Root)
	}
	return layout, nil
}

func DetectCurrent(buildVersion string) (Detection, error) {
	executable, err := os.Executable()
	if err != nil {
		return Detection{}, err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
		executable = resolved
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return Detection{}, err
	}
	layout, err := DefaultLayout()
	if err != nil {
		return Detection{}, err
	}
	home, _ := os.UserHomeDir()
	detection := detect(executable, buildVersion, layout, home, os.Getenv("GOBIN"), os.Getenv("GOPATH"), os.Getenv("SCOOP"))
	if detection.Method != MethodDirect || detection.Root == "" {
		return detection, nil
	}
	metadata, err := ReadMetadata(filepath.Join(detection.Root, "install.json"))
	if err == nil {
		detection.Metadata = &metadata
		return detection, nil
	}
	if errors.Is(err, ErrMetadataNotFound) {
		return detection, nil
	}
	return Detection{}, err
}

func detect(executable, buildVersion string, layout Layout, home, goBin, goPath, scoopRoot string) Detection {
	executable = filepath.Clean(strings.TrimSpace(executable))
	if executable == "" || executable == "." {
		return Detection{Method: MethodUnknown}
	}
	if isHomebrewPath(executable) {
		return Detection{Method: MethodHomebrew, Executable: executable}
	}
	if isScoopPath(executable, scoopRoot) {
		return Detection{Method: MethodScoop, Executable: executable}
	}
	if withinPath(layout.Root, executable) {
		return Detection{Method: MethodDirect, Executable: executable, Root: layout.Root}
	}
	if root := directRootFromExecutable(executable); root != "" {
		return Detection{Method: MethodDirect, Executable: executable, Root: root}
	}
	if buildVersion == "dev" || strings.TrimSpace(buildVersion) == "" {
		return Detection{Method: MethodDevelopment, Executable: executable}
	}
	if isGoInstallPath(executable, home, goBin, goPath) {
		return Detection{Method: MethodGo, Executable: executable}
	}
	return Detection{Method: MethodStandalone, Executable: executable}
}

func directRootFromExecutable(executable string) string {
	parent := filepath.Dir(executable)
	if strings.EqualFold(filepath.Base(parent), "current") {
		return filepath.Dir(parent)
	}
	versions := filepath.Dir(parent)
	if strings.EqualFold(filepath.Base(versions), "versions") {
		return filepath.Dir(versions)
	}
	return ""
}

func isHomebrewPath(path string) bool {
	normalized := normalizedPath(path)
	return strings.Contains(normalized, "/cellar/") || strings.Contains(normalized, "/caskroom/")
}

func isScoopPath(path, scoopRoot string) bool {
	normalized := normalizedPath(path)
	if strings.Contains(normalized, "/scoop/apps/codemcp/") {
		return true
	}
	scoopRoot = strings.TrimSpace(scoopRoot)
	return scoopRoot != "" && withinPath(scoopRoot, path) && strings.Contains(normalized, "/apps/codemcp/")
}

func isGoInstallPath(executable, home, goBin, goPath string) bool {
	dirs := make([]string, 0, 4)
	if strings.TrimSpace(goBin) != "" {
		dirs = append(dirs, goBin)
	}
	for _, root := range filepath.SplitList(goPath) {
		if strings.TrimSpace(root) != "" {
			dirs = append(dirs, filepath.Join(root, "bin"))
		}
	}
	if strings.TrimSpace(home) != "" {
		dirs = append(dirs, filepath.Join(home, "go", "bin"))
	}
	for _, dir := range dirs {
		if samePath(filepath.Dir(executable), dir) {
			return true
		}
	}
	return false
}

func withinPath(root, target string) bool {
	root = strings.TrimSpace(root)
	target = strings.TrimSpace(target)
	if root == "" || target == "" {
		return false
	}
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	return err == nil && (relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func samePath(left, right string) bool {
	leftPath, leftErr := comparablePath(left)
	rightPath, rightErr := comparablePath(right)
	return leftErr == nil && rightErr == nil && strings.EqualFold(leftPath, rightPath)
}

func comparablePath(path string) (string, error) {
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
		absolute = resolved
	}
	return filepath.Clean(absolute), nil
}

func normalizedPath(path string) string {
	return strings.ToLower(strings.ReplaceAll(filepath.ToSlash(path), "\\", "/"))
}
