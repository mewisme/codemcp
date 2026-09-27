package typesafe

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/secretstore"
)

func TestAPIKeyUsesCanonicalSecretStore(t *testing.T) {
	root := t.TempDir()
	restore := secretstore.UseMemoryForTesting()
	defer restore()

	status, err := Credential(root)
	if err != nil || status.Configured {
		t.Fatalf("initial status=%#v err=%v", status, err)
	}
	const secret = "ts-secret-value"
	if err := UpdateAPIKey(root, "  "+secret+"  "); err != nil {
		t.Fatal(err)
	}
	status, err = Credential(root)
	if err != nil || !status.Configured {
		t.Fatalf("configured status=%#v err=%v", status, err)
	}
	value, err := LoadAPIKey(root)
	if err != nil || value != secret {
		t.Fatalf("value=%q err=%v", value, err)
	}
	if err := UpdateAPIKey(root, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAPIKey(root); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("cleared key err=%v", err)
	}
}

func TestAPIKeyNeverAppearsInConfigFiles(t *testing.T) {
	root := t.TempDir()
	const secret = "ts-file-secret-sentinel"
	if err := UpdateAPIKey(root, secret); err != nil {
		t.Fatal(err)
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if filepath.Base(path) == "config.json" && strings.Contains(string(data), secret) {
			t.Fatalf("TypeSafe key leaked into config file %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
