package cftunnel

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSupportedPlatforms(t *testing.T) {
	for _, target := range [][2]string{{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "amd64"}, {"darwin", "arm64"}, {"windows", "amd64"}, {"windows", "arm64"}} {
		platform, ok := PlatformFor(target[0], target[1])
		if !ok || platform.Target == "" || platform.Archive == "" || platform.Entrypoint == "" {
			t.Fatalf("missing cf-tunnel platform for %s/%s: %#v", target[0], target[1], platform)
		}
	}
}

func TestManagedInstallUpdateResolveProbeAndRemove(t *testing.T) {
	archives := map[string][]byte{
		"v0.0.1": testTarGz(t, "cf-tunnel", []byte("#!/bin/sh\necho v0.0.1\n")),
		"v0.0.2": testTarGz(t, "cf-tunnel", []byte("#!/bin/sh\necho v0.0.2\n")),
	}
	version := "v0.0.1"
	client := releaseClient(t, func() string { return version }, archives)
	root := t.TempDir()
	manager := New(Options{ManagedRoot: root, HTTPClient: client})
	manager.goos, manager.goarch = "linux", "amd64"
	manager.lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	manager.run = func(_ context.Context, path string, args ...string) (string, error) {
		if filepath.Base(path) != "cf-tunnel" || len(args) != 1 || args[0] != "--version" {
			t.Fatalf("unexpected probe path=%q args=%#v", path, args)
		}
		if strings.Contains(path, "v0.0.2") {
			return "v0.0.2\n", nil
		}
		return "v0.0.1\n", nil
	}

	installed, err := manager.Install(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !installed.Installed || installed.Version != "v0.0.1" || installed.Status.Source != SourceManaged || !installed.Status.Verified {
		t.Fatalf("install result=%#v", installed)
	}
	probe, err := manager.Probe(t.Context())
	if err != nil || probe.Version != "v0.0.1" {
		t.Fatalf("probe=%#v err=%v", probe, err)
	}

	version = "v0.0.2"
	updated, err := manager.Update(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Installed || updated.Version != "v0.0.2" || updated.Status.Version != "v0.0.2" {
		t.Fatalf("update result=%#v", updated)
	}
	if _, err := os.Stat(filepath.Join(root, assetName, "v0.0.1")); !os.IsNotExist(err) {
		t.Fatalf("old managed version was not pruned: %v", err)
	}

	removed, err := manager.Remove()
	if err != nil {
		t.Fatal(err)
	}
	if !removed.Removed || removed.Status.Source != SourceUnavailable || removed.Status.ManagedInstalled {
		t.Fatalf("remove result=%#v", removed)
	}
}

func TestSystemExecutableWinsAndManagedRemoveNeverDeletesIt(t *testing.T) {
	root := t.TempDir()
	systemPath := filepath.Join(t.TempDir(), "cf-tunnel")
	if err := os.WriteFile(systemPath, []byte("system"), 0o755); err != nil {
		t.Fatal(err)
	}
	manager := New(Options{ManagedRoot: root})
	manager.goos, manager.goarch = "linux", "amd64"
	manager.lookPath = func(string) (string, error) { return systemPath, nil }
	resolution, err := manager.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Source != SourceSystem || resolution.Path != systemPath {
		t.Fatalf("resolution=%#v", resolution)
	}
	removed, err := manager.Remove()
	if err != nil {
		t.Fatal(err)
	}
	if removed.Removed {
		t.Fatal("remove reported a managed asset when none existed")
	}
	if _, err := os.Stat(systemPath); err != nil {
		t.Fatalf("system executable was removed: %v", err)
	}
}

func releaseClient(t *testing.T, version func() string, archives map[string][]byte) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		current := version()
		archive := archives[current]
		sum := sha256.Sum256(archive)
		filename := "cf-tunnel_" + strings.TrimPrefix(current, "v") + "_linux_amd64.tar.gz"
		releaseURL := "https://github.com/" + Repository + "/releases/download/" + current + "/"
		var body []byte
		switch request.URL.String() {
		case "https://api.github.com/repos/" + Repository + "/releases/latest":
			body = []byte(fmt.Sprintf(`{"tag_name":%q,"assets":[{"name":"checksums.txt","browser_download_url":%q},{"name":%q,"browser_download_url":%q}]}`,
				current, releaseURL+"checksums.txt", filename, releaseURL+filename))
		case releaseURL + "checksums.txt":
			body = []byte(hex.EncodeToString(sum[:]) + "  " + filename + "\n")
		case releaseURL + filename:
			body = archive
		default:
			return nil, fmt.Errorf("unexpected request: %s", request.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	})}
}

func testTarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }
