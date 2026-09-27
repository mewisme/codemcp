package capability

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestBrowserRequiredOperationsHaveFrontendAdapters(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve capability test path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
	body, err := os.ReadFile(filepath.Join(root, "frontend", "src", "lib", "operations.ts"))
	if err != nil {
		t.Fatal(err)
	}
	matches := regexp.MustCompile(`\{\s*method:\s*"([^"]+)",\s*pattern:\s*"([^"]+)",\s*operation:\s*"([^"]+)"\s*,?\s*\}`).FindAllStringSubmatch(string(body), -1)
	actual := map[ID]bool{}
	for _, match := range matches {
		method, path, id := match[1], match[2], ID(match[3])
		actual[id] = true
		canonical, found := ForAdmin(method, path)
		if !found || canonical != id {
			t.Errorf("frontend adapter %s %s -> %s, canonical=%s,%t", method, path, id, canonical, found)
		}
	}
	if len(matches) == 0 {
		t.Fatal("frontend operation adapter inventory is empty")
	}
	var missing, stale []string
	required := map[ID]bool{}
	for _, id := range RequiredOperations(SurfaceBrowser) {
		required[id] = true
		if !actual[id] {
			missing = append(missing, string(id))
		}
	}
	for id := range actual {
		if !required[id] {
			stale = append(stale, string(id))
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) != 0 {
		t.Fatalf("Browser required operations missing frontend adapters: %s", strings.Join(missing, ", "))
	}
	if len(stale) != 0 {
		t.Fatalf("frontend operation adapter has stale/non-required operations: %s", strings.Join(stale, ", "))
	}
}
