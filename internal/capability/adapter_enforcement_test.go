package capability

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

func TestBrowserFrontendAdapterEvidenceMatchesRegistry(t *testing.T) {
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
	for id := range browserFrontendOperationIDs {
		if !actual[id] {
			t.Errorf("Browser frontend evidence registry contains missing operation %s", id)
		}
	}
	for id := range actual {
		if !browserFrontendOperationIDs[id] {
			t.Errorf("Browser frontend operation %s is missing from adapter evidence registry", id)
		}
		spec, ok := Lookup(id)
		if !ok {
			t.Errorf("Browser frontend operation %s is not canonical", id)
			continue
		}
		contract, ok := spec.Surface(SurfaceBrowser)
		if !ok || contract.State != SurfaceRequired {
			t.Errorf("live Browser adapter %s points to non-required product operation: %#v", id, contract)
		}
	}
}
