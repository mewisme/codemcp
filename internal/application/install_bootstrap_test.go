package application

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
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

func TestResolveInstallCurrentOptionsEnvironmentPrecedence(t *testing.T) {
	tests := []struct {
		name     string
		options  InstallCurrentOptions
		raw      string
		present  bool
		wantSkip bool
		wantErr  bool
	}{
		{name: "default", present: false, wantSkip: false},
		{name: "env true", raw: " YES ", present: true, wantSkip: false},
		{name: "env false", raw: " OFF ", present: true, wantSkip: true},
		{name: "explicit skip wins true env", options: InstallCurrentOptions{SkipMissingIntegrations: true}, raw: "on", present: true, wantSkip: true},
		{name: "explicit skip wins invalid env", options: InstallCurrentOptions{SkipMissingIntegrations: true}, raw: "invalid", present: true, wantSkip: true},
		{name: "invalid env", raw: "invalid", present: true, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveInstallCurrentOptionsWithLookup(test.options, func(key string) (string, bool) {
				if key != InstallIntegrationsEnv || !test.present {
					return "", false
				}
				return test.raw, true
			})
			if (err != nil) != test.wantErr {
				t.Fatalf("err=%v wantErr=%t", err, test.wantErr)
			}
			if test.wantErr {
				if !strings.Contains(err.Error(), InstallIntegrationsEnv) {
					t.Fatalf("err=%v", err)
				}
				return
			}
			if got.SkipMissingIntegrations != test.wantSkip {
				t.Fatalf("options=%#v", got)
			}
		})
	}
}

func TestInstallCurrentContextRejectsInvalidIntegrationEnvBeforeInstall(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	t.Setenv(InstallIntegrationsEnv, "maybe")
	_, err := InstallCurrentContext(t.Context(), InstallCurrentOptions{})
	if err == nil || !strings.Contains(err.Error(), InstallIntegrationsEnv) {
		t.Fatalf("err=%v", err)
	}
}
