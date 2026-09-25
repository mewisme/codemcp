package version

import (
	"runtime/debug"
	"testing"
)

func TestApplyBuildInfoPreservesDevelopmentVersionWithoutReleaseTag(t *testing.T) {
	previousTag, previousVersion, previousCommit, previousDate := ReleaseTag, Version, Commit, Date
	defer func() {
		ReleaseTag, Version, Commit, Date = previousTag, previousVersion, previousCommit, previousDate
	}()

	ReleaseTag, Version, Commit, Date = "", DevelopmentVersion, "unknown", "unknown"
	applyBuildInfo(&debug.BuildInfo{
		Main: debug.Module{Version: "v0.1.0"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef"},
			{Key: "vcs.time", Value: "2026-08-29T10:00:00Z"},
		},
	})
	if Version != DevelopmentVersion || Commit != "0123456789abcdef" || Date != "2026-08-29T10:00:00Z" {
		t.Fatalf("version metadata = %q %q %q", Version, Commit, Date)
	}
}

func TestApplyBuildInfoTaggedReleaseWinsAndNormalizes(t *testing.T) {
	previousTag, previousVersion, previousCommit, previousDate := ReleaseTag, Version, Commit, Date
	defer func() {
		ReleaseTag, Version, Commit, Date = previousTag, previousVersion, previousCommit, previousDate
	}()

	ReleaseTag, Version, Commit, Date = "v2.3.4-rc.1", DevelopmentVersion, "release-commit", "release-date"
	applyBuildInfo(&debug.BuildInfo{Main: debug.Module{Version: "v9.9.9"}})
	if Version != "2.3.4-rc.1" || Commit != "release-commit" || Date != "release-date" {
		t.Fatalf("tagged metadata = %q %q %q", Version, Commit, Date)
	}
}

func TestApplyBuildInfoSourceBuildUsesExactDevelopmentVersion(t *testing.T) {
	previousTag, previousVersion, previousCommit, previousDate := ReleaseTag, Version, Commit, Date
	defer func() {
		ReleaseTag, Version, Commit, Date = previousTag, previousVersion, previousCommit, previousDate
	}()

	ReleaseTag, Version, Commit, Date = "", DevelopmentVersion, "unknown", "unknown"
	applyBuildInfo(&debug.BuildInfo{Main: debug.Module{Version: "(devel)"}})
	if Version != "0.0.1-dev" {
		t.Fatalf("development version = %q", Version)
	}
}

func TestApplyBuildInfoPreservesExplicitLdflags(t *testing.T) {
	previousTag, previousVersion, previousCommit, previousDate := ReleaseTag, Version, Commit, Date
	defer func() {
		ReleaseTag, Version, Commit, Date = previousTag, previousVersion, previousCommit, previousDate
	}()

	ReleaseTag, Version, Commit, Date = "", "1.0.0", "explicit-commit", "explicit-date"
	applyBuildInfo(&debug.BuildInfo{
		Main: debug.Module{Version: "v0.1.0"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "auto-commit"},
			{Key: "vcs.time", Value: "auto-date"},
		},
	})
	if Version != "1.0.0" || Commit != "explicit-commit" || Date != "explicit-date" {
		t.Fatalf("explicit metadata was overwritten: %q %q %q", Version, Commit, Date)
	}
}

func TestStringUsesCodeMCPProductIdentity(t *testing.T) {
	previousVersion, previousCommit, previousDate := Version, Commit, Date
	defer func() {
		Version, Commit, Date = previousVersion, previousCommit, previousDate
	}()

	Version, Commit, Date = "v1.2.3", "abc123", "2026-09-24T00:00:00Z"
	if got, want := String(), "CodeMCP version v1.2.3 (abc123) 2026-09-24T00:00:00Z"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestUserAgentUsesCanonicalVersion(t *testing.T) {
	previous := Version
	defer func() { Version = previous }()
	Version = "7.8.9"
	if got := UserAgent(); got != "codemcp/7.8.9" {
		t.Fatalf("UserAgent() = %q", got)
	}
}

func TestIsDevelopmentRecognizesCanonicalAndLegacyDevelopmentVersions(t *testing.T) {
	for _, value := range []string{"", DevelopmentVersion, "dev", "(devel)", "dev-local"} {
		if !IsDevelopment(value) {
			t.Fatalf("IsDevelopment(%q)=false", value)
		}
	}
	for _, value := range []string{"0.0.1", "1.2.3", "1.2.3-dev"} {
		if IsDevelopment(value) {
			t.Fatalf("IsDevelopment(%q)=true", value)
		}
	}
}
