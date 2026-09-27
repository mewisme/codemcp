package admin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminInterfaceDoesNotOwnBusinessMutations(t *testing.T) {
	t.Helper()
	forbidden := []string{
		"config.Save(",
		".Config.Update(",
		"SaveTunnelMetadata(",
		"RemoveTunnelMetadata(",
		"SyncTunnelMetadata(",
		".Reconfigure(",
		".ReconfigureSeeded(",
		".SyncManagementConfig(",
		"api.Tools.Processes.ClearFinished(",
		"ReloadConfig",
		"saveConfig",
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		source := string(data)
		for _, needle := range forbidden {
			if strings.Contains(source, needle) {
				t.Fatalf("%s contains interface-owned mutation primitive %q; delegate through application/domain ownership instead", entry.Name(), needle)
			}
		}
	}
}
