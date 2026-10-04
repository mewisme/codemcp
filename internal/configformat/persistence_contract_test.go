package configformat

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRuntimePersistenceHasNoLegacyFormatReadersOrCGMBundles(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	internalRoot := filepath.Clean(filepath.Join(filepath.Dir(current), ".."))
	legacyBundleExt := "." + "cgm"
	allowedNonPersistenceImports := map[string]map[string]bool{
		filepath.FromSlash("skills/validation.go"): {
			`"gopkg.in/yaml.v3"`: true, // SKILL.md frontmatter, not configuration persistence.
		},
		filepath.FromSlash("releaseverify/verify.go"): {
			`"gopkg.in/yaml.v3"`: true, // Repository release configuration verification, not runtime persistence.
		},
	}
	err := filepath.WalkDir(internalRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != internalRoot && filepath.Base(path) == "migration" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		relative, _ := filepath.Rel(internalRoot, path)
		for _, forbidden := range []string{
			`"github.com/pelletier/go-toml/v2"`,
			`"gopkg.in/yaml.v3"`,
			`"go.mewis.me/codemcp/internal/migration/configformat"`,
			`"` + legacyBundleExt + `"`,
		} {
			if strings.Contains(text, forbidden) {
				if allowedNonPersistenceImports[relative][forbidden] {
					continue
				}
				t.Fatalf("current runtime source %s contains legacy persistence dependency %s", relative, forbidden)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
