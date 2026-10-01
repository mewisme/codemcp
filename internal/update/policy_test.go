package update

import (
	"errors"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/install"
)

func TestPolicyForInstallation(t *testing.T) {
	directMetadata := &install.Metadata{Schema: install.MetadataSchema, Method: install.MethodDirect, Version: "v1.0.0", InstallDir: "/managed"}
	tests := []struct {
		name      string
		detection install.Detection
		action    PolicyAction
		command   string
	}{
		{"direct", install.Detection{Method: install.MethodDirect, Metadata: directMetadata}, PolicySelfUpdate, ""},
		{"legacy-direct", install.Detection{Method: install.MethodDirect}, PolicyInstallFirst, "cm install"},
		{"homebrew", install.Detection{Method: install.MethodHomebrew}, PolicyDelegate, "cm upgrade"},
		{"scoop", install.Detection{Method: install.MethodScoop}, PolicyDelegate, "cm upgrade"},
		{"debian", install.Detection{Method: install.MethodDebian}, PolicyDelegate, "cm upgrade"},
		{"rpm", install.Detection{Method: install.MethodRPM}, PolicyDelegate, "cm upgrade"},
		{"go", install.Detection{Method: install.MethodGo}, PolicyUnsupported, ""},
		{"development", install.Detection{Method: install.MethodDevelopment}, PolicyUnsupported, ""},
		{"standalone", install.Detection{Method: install.MethodStandalone}, PolicyInstallFirst, "cm install"},
		{"unknown", install.Detection{Method: install.MethodUnknown}, PolicyUnsupported, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := PolicyForInstallation(test.detection)
			if policy.Method != test.detection.Method || policy.Action != test.action || policy.Command != test.command {
				t.Fatalf("policy = %+v", policy)
			}
			if test.action == PolicySelfUpdate || test.action == PolicyDelegate {
				if err := policy.Error(); err != nil {
					t.Fatalf("policy error = %v", err)
				}
			} else if err := policy.Error(); !errors.Is(err, ErrSelfUpdateUnavailable) {
				t.Fatalf("policy error = %v", err)
			}
		})
	}
}

func TestPackageManagerPlanFor(t *testing.T) {
	tests := []struct {
		method  install.Method
		name    string
		refresh PackageManagerCommand
		apply   PackageManagerCommand
	}{
		{install.MethodHomebrew, "Homebrew", PackageManagerCommand{Name: "brew", Args: []string{"update"}}, PackageManagerCommand{Name: "brew", Args: []string{"upgrade", "--cask", "codemcp"}}},
		{install.MethodScoop, "Scoop", PackageManagerCommand{Name: "scoop", Args: []string{"update"}}, PackageManagerCommand{Name: "scoop", Args: []string{"update", "mew/codemcp"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, ok := PackageManagerPlanFor(test.method)
			if !ok || plan.Method != test.method || plan.Name != test.name || plan.Refresh.Name != test.refresh.Name || plan.Apply.Name != test.apply.Name {
				t.Fatalf("plan = %+v, ok = %t", plan, ok)
			}
			if strings.Join(plan.Refresh.Args, " ") != strings.Join(test.refresh.Args, " ") || strings.Join(plan.Apply.Args, " ") != strings.Join(test.apply.Args, " ") {
				t.Fatalf("plan = %+v", plan)
			}
		})
	}
	if _, ok := PackageManagerPlanFor(install.MethodDirect); ok {
		t.Fatal("direct installation returned a package manager plan")
	}
}

func TestPolicyRejectsMismatchedDirectMetadata(t *testing.T) {
	policy := PolicyForInstallation(install.Detection{Method: install.MethodDirect, Metadata: &install.Metadata{Method: install.MethodScoop}})
	if policy.Action != PolicyUnsupported || !errors.Is(policy.Error(), ErrSelfUpdateUnavailable) {
		t.Fatalf("policy = %+v, error = %v", policy, policy.Error())
	}
}

func TestPackageManagerOwnershipNeverSelectsDirectSelfUpdate(t *testing.T) {
	for _, method := range []install.Method{install.MethodHomebrew, install.MethodScoop, install.MethodDebian, install.MethodRPM} {
		policy := PolicyForInstallation(install.Detection{Method: method})
		if policy.Action != PolicyDelegate || policy.Action == PolicySelfUpdate {
			t.Fatalf("%s policy = %#v", method, policy)
		}
	}
}
