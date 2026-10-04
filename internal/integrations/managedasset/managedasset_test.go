package managedasset

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func TestInstallVerifiesArchiveAndActivatedBinary(t *testing.T) {
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	payload := []byte("#!/bin/sh\necho managed\n")
	if err := tarWriter.WriteHeader(&tar.Header{Name: "tool", Mode: 0o755, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archive.Bytes())

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive.Bytes())
	}))
	defer server.Close()

	spec := Spec{
		Name: "tool", Version: "v1", Platform: "test-platform",
		URL: server.URL + "/tool.tar.gz", SHA256: hex.EncodeToString(sum[:]),
		Archive: "tar.gz", Entrypoint: "tool",
	}
	manager := Manager{Root: t.TempDir(), HTTPClient: server.Client()}
	var traceEvents []tracepkg.Event
	ctx := tracepkg.WithObserver(t.Context(), func(event tracepkg.Event) {
		traceEvents = append(traceEvents, event)
	})
	path, err := manager.Install(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("installed payload=%q err=%v", data, err)
	}
	if _, err := manager.Validate(spec); err != nil {
		t.Fatalf("activated asset failed validation: %v", err)
	}
	wantTrace := []string{
		"managed_asset.download.started",
		"managed_asset.download.completed",
		"managed_asset.verify.archive.started",
		"managed_asset.verify.archive.completed",
		"managed_asset.extract.started",
		"managed_asset.extract.completed",
		"managed_asset.verify.payload.started",
		"managed_asset.verify.payload.completed",
		"managed_asset.activate.started",
		"managed_asset.activate.completed",
		"managed_asset.cleanup.started",
		"managed_asset.cleanup.completed",
	}
	if got := managedAssetTraceNames(traceEvents); strings.Join(got, ",") != strings.Join(wantTrace, ",") {
		t.Fatalf("managed asset trace=%v want=%v", got, wantTrace)
	}
	again, err := manager.Install(t.Context(), spec)
	if err != nil || again != path {
		t.Fatalf("idempotent install path=%q err=%v want=%q", again, err, path)
	}
	manifestPath := filepath.Join(filepath.Dir(path), "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
}

func TestActivateDirectoryTracesRollbackAndRestoresPreviousAsset(t *testing.T) {
	stage := t.TempDir()
	payload := filepath.Join(stage, "payload")
	if err := os.MkdirAll(payload, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "tool"), []byte("new"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "tool"), []byte("old"), 0o700); err != nil {
		t.Fatal(err)
	}
	var traceEvents []tracepkg.Event
	ctx := tracepkg.WithObserver(context.Background(), func(event tracepkg.Event) {
		traceEvents = append(traceEvents, event)
	})
	manager := Manager{}
	_, keepStage, err := manager.activateDirectory(ctx, stage, payload, target, func() (string, error) {
		return "", errors.New("invalid activated asset")
	})
	if err == nil || keepStage {
		t.Fatalf("activate rollback err=%v keepStage=%t", err, keepStage)
	}
	data, readErr := os.ReadFile(filepath.Join(target, "tool"))
	if readErr != nil || string(data) != "old" {
		t.Fatalf("previous asset not restored: data=%q err=%v", data, readErr)
	}
	names := managedAssetTraceNames(traceEvents)
	if strings.Join(names, ",") != "managed_asset.activate.rollback.started,managed_asset.activate.rollback.completed" {
		t.Fatalf("rollback trace=%v", names)
	}
}

func TestLatestGitHubReleaseTracesResolutionFailure(t *testing.T) {
	var traceEvents []tracepkg.Event
	ctx := tracepkg.WithObserver(t.Context(), func(event tracepkg.Event) {
		traceEvents = append(traceEvents, event)
	})
	if _, err := LatestGitHubRelease(ctx, nil, "invalid", "checksums.txt"); err == nil {
		t.Fatal("invalid repository unexpectedly resolved")
	}
	names := managedAssetTraceNames(traceEvents)
	if strings.Join(names, ",") != "managed_asset.release.resolve.started,managed_asset.release.resolve.failed" {
		t.Fatalf("release resolution trace=%v", names)
	}
}

func managedAssetTraceNames(events []tracepkg.Event) []string {
	names := make([]string, 0, len(events))
	for _, event := range events {
		if strings.HasPrefix(event.Name, "managed_asset.") {
			names = append(names, event.Name)
		}
	}
	return names
}

func TestPruneStaleManagedStages(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	old := now.Add(-2 * staleManagedStageAge)
	for _, name := range []string{".linux-amd64-staging-old", ".linux-amd64-tree-staging-old", ".linux-amd64-staging-fresh", "payload"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(name, "old") {
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	pruneStaleManagedStages(root, now)
	for _, name := range []string{".linux-amd64-staging-old", ".linux-amd64-tree-staging-old"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("stale stage %q still exists: %v", name, err)
		}
	}
	for _, name := range []string{".linux-amd64-staging-fresh", "payload"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("kept entry %q removed: %v", name, err)
		}
	}
}

func TestSpecValidationRejectsUnsafeIdentityAndTransport(t *testing.T) {
	base := Spec{
		Name: "tool", Version: "v1", Platform: "linux-amd64",
		URL:     "https://example.com/tool.tar.gz",
		SHA256:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Archive: "tar.gz", Entrypoint: "tool",
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
	unsafe := base
	unsafe.Name = "../tool"
	if err := unsafe.Validate(); err == nil {
		t.Fatal("unsafe managed-asset name was accepted")
	}
	insecure := base
	insecure.URL = "http://example.com/tool.tar.gz"
	if err := insecure.Validate(); err == nil {
		t.Fatal("insecure managed-asset URL was accepted")
	}
}
