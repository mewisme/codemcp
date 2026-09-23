package version

import (
	"runtime/debug"
	"testing"
)

func TestApplyBuildInfo(t *testing.T) {
	previousVersion, previousCommit, previousDate := Version, Commit, Date
	defer func() {
		Version, Commit, Date = previousVersion, previousCommit, previousDate
	}()

	Version, Commit, Date = "dev", "unknown", "unknown"
	applyBuildInfo(&debug.BuildInfo{
		Main: debug.Module{Version: "v0.1.0"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef"},
			{Key: "vcs.time", Value: "2026-08-29T10:00:00Z"},
		},
	})
	if Version != "v0.1.0" || Commit != "0123456789abcdef" || Date != "2026-08-29T10:00:00Z" {
		t.Fatalf("version metadata = %q %q %q", Version, Commit, Date)
	}
}

func TestApplyBuildInfoPreservesExplicitLdflags(t *testing.T) {
	previousVersion, previousCommit, previousDate := Version, Commit, Date
	defer func() {
		Version, Commit, Date = previousVersion, previousCommit, previousDate
	}()

	Version, Commit, Date = "v1.0.0", "explicit-commit", "explicit-date"
	applyBuildInfo(&debug.BuildInfo{
		Main: debug.Module{Version: "v0.1.0"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "auto-commit"},
			{Key: "vcs.time", Value: "auto-date"},
		},
	})
	if Version != "v1.0.0" || Commit != "explicit-commit" || Date != "explicit-date" {
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
