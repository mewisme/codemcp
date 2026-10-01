//go:build !windows

package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinuxPackageOwnershipProvesExpectedPackage(t *testing.T) {
	tests := []struct {
		name       string
		available  map[string]string
		output     map[string]string
		wantMethod Method
		wantOwner  string
	}{
		{
			name:       "debian",
			available:  map[string]string{"dpkg-query": "/fake/dpkg-query"},
			output:     map[string]string{"/fake/dpkg-query": "codemcp: /usr/bin/cm\n"},
			wantMethod: MethodDebian,
			wantOwner:  PackageName,
		},
		{
			name:       "debian multiarch",
			available:  map[string]string{"dpkg-query": "/fake/dpkg-query"},
			output:     map[string]string{"/fake/dpkg-query": "codemcp:amd64: /usr/bin/cm\n"},
			wantMethod: MethodDebian,
			wantOwner:  PackageName,
		},
		{
			name:       "rpm",
			available:  map[string]string{"rpm": "/fake/rpm"},
			output:     map[string]string{"/fake/rpm": "codemcp\n"},
			wantMethod: MethodRPM,
			wantOwner:  PackageName,
		},
		{
			name:       "unrelated debian owner stays typed but not codemcp",
			available:  map[string]string{"dpkg-query": "/fake/dpkg-query"},
			output:     map[string]string{"/fake/dpkg-query": "other-package: /usr/bin/cm\n"},
			wantMethod: MethodDebian,
			wantOwner:  "other-package",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lookup := func(name string) (string, error) {
				if path := test.available[name]; path != "" {
					return path, nil
				}
				return "", errors.New("not found")
			}
			run := func(_ context.Context, command string, args ...string) (string, error) {
				if strings.Contains(command, "dpkg-query") {
					if got := strings.Join(args, " "); got != "--search /usr/bin/cm" {
						t.Fatalf("dpkg args = %q", got)
					}
				}
				if strings.Contains(command, "/rpm") {
					if got := strings.Join(args, " "); got != "-qf --qf %{NAME}\n /usr/bin/cm" {
						t.Fatalf("rpm args = %q", got)
					}
				}
				return test.output[command], nil
			}
			got := linuxPackageOwnershipWith("/usr/bin/cm", lookup, func(string) bool { return true }, run)
			if got.Method != test.wantMethod || got.Package != test.wantOwner {
				t.Fatalf("ownership = %#v", got)
			}
		})
	}
}

func TestLinuxPackageOwnershipDoesNotSpoofCodeMCP(t *testing.T) {
	lookup := func(name string) (string, error) {
		if name == "dpkg-query" {
			return "/fake/dpkg-query", nil
		}
		return "", errors.New("not found")
	}
	run := func(context.Context, string, ...string) (string, error) {
		return "unrelated: /usr/bin/cm\n", nil
	}
	ownership := linuxPackageOwnershipWith("/usr/bin/cm", lookup, func(string) bool { return true }, run)
	if ownership.Package == PackageName || ownership.Method != MethodDebian {
		t.Fatalf("ownership = %#v", ownership)
	}
}

func TestLinuxPackageOwnershipRejectsUntrustedProbeExecutable(t *testing.T) {
	ran := false
	ownership := linuxPackageOwnershipWith(
		"/usr/bin/cm",
		func(string) (string, error) { return "/tmp/fake-dpkg-query", nil },
		func(string) bool { return false },
		func(context.Context, string, ...string) (string, error) {
			ran = true
			return "codemcp: /usr/bin/cm\n", nil
		},
	)
	if ran || ownership.Package != "" || ownership.Method != "" {
		t.Fatalf("untrusted probe executed or produced ownership: ran=%v ownership=%#v", ran, ownership)
	}
}

func TestTrustedSystemExecutableRejectsUserWritablePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dpkg-query")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if TrustedSystemExecutable(path) {
		t.Fatalf("temporary executable was trusted: %s", path)
	}
}

func TestPackageOwnerParsersRejectEmptyOrMalformedOutput(t *testing.T) {
	for _, value := range []string{"", "not an owner line", " : /usr/bin/cm"} {
		if owner := parseDebianPackageOwner(value); owner != "" {
			t.Fatalf("debian owner for %q = %q", value, owner)
		}
	}
	if owner := parseRPMPackageOwner(" \n\t"); owner != "" {
		t.Fatalf("rpm owner = %q", owner)
	}
}
