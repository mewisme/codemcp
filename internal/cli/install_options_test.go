package cli

import (
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/application"
)

func TestInstallCommandExposesManagedIntegrationOptOut(t *testing.T) {
	cmd := installCommand()
	flag := cmd.Flags().Lookup("no-install-integrations")
	if flag == nil || flag.DefValue != "false" {
		t.Fatalf("no-install-integrations flag=%#v", flag)
	}
	if !strings.Contains(flag.Usage, "detect and reuse existing integrations") || !strings.Contains(flag.Usage, "do not install missing managed integration assets") {
		t.Fatalf("flag usage=%q", flag.Usage)
	}
	if !strings.Contains(cmd.Long, application.InstallIntegrationsEnv) || !strings.Contains(cmd.Long, "takes precedence") {
		t.Fatalf("install long help=%q", cmd.Long)
	}
}
