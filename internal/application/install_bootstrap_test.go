package application

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"go.mewis.me/codemcp/internal/config"
)

type failRoundTripper struct {
	calls int
}

func (transport *failRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls++
	return nil, errors.New("network must not be used")
}

func TestIntegrationEnsureAvailableUsesResolvedExecutableWithoutManagedDownload(t *testing.T) {
	root := t.TempDir()
	rtkPath := filepath.Join(root, "rtk")
	codeGraphPath := filepath.Join(root, "codegraph")
	for _, path := range []string{rtkPath, codeGraphPath} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Integrations.RTK.Path = rtkPath
	cfg.Integrations.CodeGraph.Path = codeGraphPath
	transport := &failRoundTripper{}
	client := &http.Client{Transport: transport}

	rtkResult, err := (&RTKService{
		LoadConfig:  func() (config.Config, error) { return cfg, nil },
		ManagedRoot: filepath.Join(root, "managed-rtk"),
		HTTPClient:  client,
	}).EnsureAvailable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rtkResult.State != "available" || rtkResult.Source != "configured" {
		t.Fatalf("rtk result=%#v", rtkResult)
	}

	codeGraphResult, err := (&CodeGraphService{
		LoadConfig:  func() (config.Config, error) { return cfg, nil },
		ManagedRoot: filepath.Join(root, "managed-codegraph"),
		HTTPClient:  client,
	}).EnsureAvailable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if codeGraphResult.State != "available" || codeGraphResult.Source != "configured" {
		t.Fatalf("codegraph result=%#v", codeGraphResult)
	}
	if transport.calls != 0 {
		t.Fatalf("managed download attempted %d time(s)", transport.calls)
	}
}
