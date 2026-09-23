package configformat

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	EnvConfigDir = "CM_CONFIG_DIR"
	rootMarker   = ".cm-root"
)

var rootOverride struct {
	sync.RWMutex
	path string
}

type Source struct {
	Path   string
	Format Format
	Ext    string
	Exists bool
}

func RootPath() string {
	rootOverride.RLock()
	override := rootOverride.path
	rootOverride.RUnlock()
	if override != "" {
		return override
	}
	if configured := strings.TrimSpace(os.Getenv(EnvConfigDir)); configured != "" {
		if absolute, err := filepath.Abs(configured); err == nil {
			return filepath.Clean(absolute)
		}
		return filepath.Clean(configured)
	}
	return DefaultRootPath()
}

func DefaultRootPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "cm"
	}
	return filepath.Join(home, ".cm")
}

func SetRootPath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		rootOverride.Lock()
		rootOverride.path = ""
		rootOverride.Unlock()
		return nil
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve config directory: %w", err)
	}
	clean := filepath.Clean(absolute)
	volume := filepath.VolumeName(clean)
	if clean == string(filepath.Separator) || clean == volume+string(filepath.Separator) {
		return fmt.Errorf("config directory cannot be a filesystem root: %s", clean)
	}
	if home, err := os.UserHomeDir(); err == nil && samePath(clean, home) {
		return fmt.Errorf("config directory cannot be the user home directory: %s", clean)
	}
	if cwd, err := os.Getwd(); err == nil && containsPath(clean, cwd) {
		return fmt.Errorf("config directory cannot contain the current working directory: %s", clean)
	}
	rootOverride.Lock()
	rootOverride.path = clean
	rootOverride.Unlock()
	return nil
}

func containsPath(root, target string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil {
		return false
	}
	return relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func samePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	return leftErr == nil && rightErr == nil && filepath.Clean(leftAbs) == filepath.Clean(rightAbs)
}

func MarkRoot(root string) error {
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, rootMarker), []byte("cm\n"), 0600)
}

func IsManagedRoot(root string) bool {
	data, err := os.ReadFile(filepath.Join(root, rootMarker))
	return err == nil && strings.TrimSpace(string(data)) == "cm"
}

func RemoveRootMarker(root string) error {
	err := os.Remove(filepath.Join(root, rootMarker))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func Discover(root string) (Source, error) {
	if strings.TrimSpace(root) == "" {
		root = RootPath()
	}
	candidates := []string{"config.json", "config.yaml", "config.yml", "config.toml"}
	found := make([]string, 0, len(candidates))
	for _, name := range candidates {
		path := filepath.Join(root, name)
		if _, err := os.Stat(path); err == nil {
			found = append(found, path)
		} else if !os.IsNotExist(err) {
			return Source{}, err
		}
	}
	if len(found) > 1 {
		sort.Strings(found)
		return Source{}, fmt.Errorf("multiple main config files found: %s", strings.Join(found, ", "))
	}
	if len(found) == 0 {
		return Source{Path: filepath.Join(root, "config.json"), Format: JSON, Ext: ".json", Exists: false}, nil
	}
	format, err := Detect(found[0])
	if err != nil {
		return Source{}, err
	}
	return Source{Path: found[0], Format: format, Ext: filepath.Ext(found[0]), Exists: true}, nil
}

func PathFor(root, name string, format Format) string {
	return filepath.Join(root, name+Extension(format))
}

func StructuredPath(root, name string) string {
	return filepath.Join(root, name+ExtensionForRoot(root))
}

func ExtensionForRoot(root string) string {
	source, err := Discover(root)
	if err != nil || source.Ext == "" {
		return ".json"
	}
	return source.Ext
}

func StructuredPathFrom(path, name string) string {
	root := filepath.Dir(path)
	ext := filepath.Ext(path)
	if _, err := Detect(path); err != nil {
		ext = ".json"
	}
	return filepath.Join(root, name+ext)
}
