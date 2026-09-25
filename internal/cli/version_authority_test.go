package cli

import (
	"testing"

	"go.mewis.me/codemcp/internal/version"
)

func TestRootVersionUsesCanonicalVersionAuthority(t *testing.T) {
	previousVersion, previousCommit, previousDate := version.Version, version.Commit, version.Date
	defer func() { version.Version, version.Commit, version.Date = previousVersion, previousCommit, previousDate }()
	version.Version, version.Commit, version.Date = "5.6.7", "abc123", "2026-09-25T00:00:00Z"
	command := newRootCommand()
	if command.Version != version.Short() {
		t.Fatalf("root version=%q canonical=%q", command.Version, version.Short())
	}
}
