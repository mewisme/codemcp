package cli

import (
	"bytes"
	"fmt"
	"reflect"
	"syscall"
	"testing"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/config"
)

func TestExposeFlagSupportsBareAllAndInterfaceLists(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want config.ExposureConfig
	}{
		{name: "bare", args: []string{"--expose"}, want: config.ExposureConfig{Mode: config.ExposureAll, Interfaces: []string{}}},
		{name: "all", args: []string{"--expose=all"}, want: config.ExposureConfig{Mode: config.ExposureAll, Interfaces: []string{}}},
		{name: "wildcard", args: []string{"--expose=0.0.0.0"}, want: config.ExposureConfig{Mode: config.ExposureWildcard, Interfaces: []string{}}},
		{name: "none", args: []string{"--expose=none"}, want: config.ExposureConfig{Mode: config.ExposureNone, Interfaces: []string{}}},
		{name: "true compatibility", args: []string{"--expose=true"}, want: config.ExposureConfig{Mode: config.ExposureWildcard, Interfaces: []string{}}},
		{name: "false compatibility", args: []string{"--expose=false"}, want: config.ExposureConfig{Mode: config.ExposureNone, Interfaces: []string{}}},
		{name: "interfaces", args: []string{"--expose=tailscale0,eth0,eth0"}, want: config.ExposureConfig{Mode: config.ExposureInterfaces, Interfaces: []string{"eth0", "tailscale0"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Default()
			cmd := &cobra.Command{Use: "test"}
			addExposeFlag(cmd)
			cmd.SetArgs(test.args)
			cmd.SetOut(&bytes.Buffer{})
			cmd.RunE = func(cmd *cobra.Command, args []string) error { return applyExposeOverride(cmd, &cfg) }
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if !config.ExposureEqual(cfg.HTTP.Exposure, test.want) {
				t.Fatalf("exposure = %#v, want %#v", cfg.HTTP.Exposure, test.want)
			}
		})
	}
}

func TestEndpointURL(t *testing.T) {
	if got := endpointURL("127.0.0.1", 37421, "/mcp"); got != "http://127.0.0.1:37421/mcp" {
		t.Fatalf("url = %q", got)
	}
	if got := endpointURL("2001:db8::20", 37421, "/mcp"); got != "http://[2001:db8::20]:37421/mcp" {
		t.Fatalf("ipv6 url = %q", got)
	}
}

func TestListenPlanShape(t *testing.T) {
	plan := listenerPlan{Hosts: []string{"127.0.0.1"}}
	if !reflect.DeepEqual(plan.Hosts, []string{"127.0.0.1"}) {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestIsAddressInUseErrorRecognizesWindowsWSAError(t *testing.T) {
	err := fmt.Errorf("listen on 127.0.0.1:37422: %w", syscall.Errno(10048))
	if !isAddressInUseError(err) {
		t.Fatalf("Windows WSAEADDRINUSE was not recognized: %v", err)
	}
	if isAddressInUseError(syscall.Errno(10061)) {
		t.Fatal("unrelated Windows socket error was treated as address-in-use")
	}
}
