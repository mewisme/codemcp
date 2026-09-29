package released024

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"go.mewis.me/codemcp/internal/install"
	"go.mewis.me/codemcp/internal/secretstore"
)

func TestLegacySecretAccessorBindsPhysicalBackupToVerifiedLogicalSource(t *testing.T) {
	currentRoot := filepath.Join(t.TempDir(), "current")
	t.Setenv("CM_CONFIG_DIR", currentRoot)
	home := t.TempDir()
	source := filepath.Join(home, "released")
	writeFixture(t, filepath.Join(source, legacyRootMarkerName), legacyRootMarkerValue+"\n")
	writeJSONFixture(t, filepath.Join(source, "config.json"), map[string]any{
		"server":   map[string]any{"enabled": true},
		"auth":     map[string]any{},
		"features": map[string]any{},
		"tunnel":   map[string]any{"api_key": secretstore.Marker},
	})
	runtimeAccount := secretstore.AccountName(secretstore.DomainTunnel, "runtime-key")
	unknownAccount := secretstore.Name("unknown-domain", "credential")
	writeFixture(t, legacySecretFixturePath(source, source, runtimeAccount), "runtime-secret-from-backup")
	writeFixture(t, legacySecretFixturePath(source, source, unknownAccount), "unknown-secret-must-stay-inactive")

	manifest, err := Detect(t.Context(), Options{
		SourceRoot: source, HomeDir: home, LookupEnv: func(string) string { return "" },
		FindInstallations: func(install.Layout, string) ([]install.LegacyInstallation, error) { return nil, nil },
		FindAliases:       func() ([]install.LegacyAlias, error) { return nil, nil },
		InspectServices:   func(_ context.Context, _ SourceDescriptor) ([]ServiceState, error) { return nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(home, "retained-backup")
	copyReleasedTreeFixture(t, source, backup)
	accessor, err := OpenLegacySecretAccessor(manifest, backup)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(home, "stage")
	if err := os.MkdirAll(destination, 0700); err != nil {
		t.Fatal(err)
	}
	result, err := accessor.Migrate(destination)
	if err != nil {
		t.Fatal(err)
	}
	if result.Migrated != 1 {
		t.Fatalf("migrated=%d inventory=%#v", result.Migrated, result.Inventory)
	}
	got, err := secretstore.New(destination).Get(runtimeAccount)
	if err != nil || got != "runtime-secret-from-backup" {
		t.Fatalf("runtime secret=%q err=%v", got, err)
	}
	if _, err := secretstore.New(destination).Get(unknownAccount); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("unknown legacy account became active: %v", err)
	}

	writeFixture(t, filepath.Join(backup, "tampered.txt"), "changed")
	if _, err := OpenLegacySecretAccessor(manifest, backup); err == nil {
		t.Fatal("tampered retained backup was accepted")
	}
}

func legacySecretFixturePath(physicalRoot, logicalRoot, account string) string {
	rootDigest := sha256.Sum256([]byte(filepath.Clean(logicalRoot)))
	service := "chatgpt-mcp/" + hex.EncodeToString(rootDigest[:8])
	secretDigest := sha256.Sum256([]byte(service + "\x00" + account))
	return filepath.Join(physicalRoot, "state", "secrets", hex.EncodeToString(secretDigest[:])+".secret")
}

func copyReleasedTreeFixture(t *testing.T, source, destination string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return errors.New("fixture source contains non-regular entry")
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		return errors.Join(copyErr, closeErr)
	})
	if err != nil {
		t.Fatal(err)
	}
}
