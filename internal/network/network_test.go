package network

import (
	"net"
	"reflect"
	"testing"

	"go.mewis.me/codemcp/internal/config"
)

func TestNormalizeInterfacesFiltersAndDeduplicates(t *testing.T) {
	candidates := []Candidate{
		{IP: net.ParseIP("127.0.0.1"), Interface: "lo", Flags: net.FlagUp | net.FlagLoopback},
		{IP: net.ParseIP("192.168.1.20"), Interface: "eth0", Flags: net.FlagUp},
		{IP: net.ParseIP("10.0.0.4"), Interface: "tailscale0", Flags: net.FlagUp},
		{IP: net.ParseIP("192.168.1.20"), Interface: "duplicate", Flags: net.FlagUp},
		{IP: net.ParseIP("172.20.0.2"), Interface: "down", Flags: 0},
		{IP: net.ParseIP("2001:db8::1"), Interface: "ipv6", Flags: net.FlagUp},
	}
	got := NormalizeInterfaces(candidates)
	want := []Interface{
		{Name: "duplicate", Addresses: []Address{{Host: "192.168.1.20", Interface: "duplicate", Scope: "lan"}}},
		{Name: "eth0", Addresses: []Address{{Host: "192.168.1.20", Interface: "eth0", Scope: "lan"}}},
		{Name: "ipv6", Addresses: []Address{{Host: "2001:db8::1", Interface: "ipv6", Scope: "network"}}},
		{Name: "tailscale0", Addresses: []Address{{Host: "10.0.0.4", Interface: "tailscale0", Scope: "lan"}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("interfaces = %#v, want %#v", got, want)
	}
}

func TestResolveExposureModes(t *testing.T) {
	interfaces := []Interface{
		{Name: "eth0", Addresses: []Address{{Host: "192.168.1.20", Interface: "eth0", Scope: "lan"}, {Host: "2001:db8::20", Interface: "eth0", Scope: "network"}}},
		{Name: "tailscale0", Addresses: []Address{{Host: "100.64.0.10", Interface: "tailscale0", Scope: "network"}}},
	}

	hosts, addresses, err := Resolve(config.ExposureConfig{Mode: config.ExposureNone}, interfaces)
	if err != nil || !reflect.DeepEqual(hosts, []string{LoopbackHost}) || len(addresses) != 1 || addresses[0].Host != LoopbackHost {
		t.Fatalf("none = hosts %#v addresses %#v err %v", hosts, addresses, err)
	}

	hosts, addresses, err = Resolve(config.ExposureConfig{Mode: config.ExposureAll}, interfaces)
	if err != nil || !reflect.DeepEqual(hosts, []string{LoopbackHost, "192.168.1.20", "2001:db8::20", "100.64.0.10"}) || len(addresses) != 4 {
		t.Fatalf("all = hosts %#v addresses %#v err %v", hosts, addresses, err)
	}

	hosts, addresses, err = Resolve(config.ExposureConfig{Mode: config.ExposureWildcard}, interfaces)
	if err != nil || !reflect.DeepEqual(hosts, []string{WildcardHost}) || len(addresses) != 3 {
		t.Fatalf("wildcard = hosts %#v addresses %#v err %v", hosts, addresses, err)
	}

	hosts, addresses, err = Resolve(config.ExposureConfig{Mode: config.ExposureInterfaces, Interfaces: []string{"tailscale0", "eth0"}}, interfaces)
	if err != nil || !reflect.DeepEqual(hosts, []string{LoopbackHost, "192.168.1.20", "2001:db8::20", "100.64.0.10"}) || len(addresses) != 4 {
		t.Fatalf("interfaces = hosts %#v addresses %#v err %v", hosts, addresses, err)
	}
}

func TestResolveIPv6OnlySelectedInterface(t *testing.T) {
	interfaces := []Interface{{Name: "eth0", Addresses: []Address{{Host: "2001:db8::20", Interface: "eth0", Scope: "network"}}}}
	hosts, addresses, err := Resolve(config.ExposureConfig{Mode: config.ExposureInterfaces, Interfaces: []string{"eth0"}}, interfaces)
	if err != nil || !reflect.DeepEqual(hosts, []string{LoopbackHost, "2001:db8::20"}) || len(addresses) != 2 {
		t.Fatalf("ipv6 interface = hosts %#v addresses %#v err %v", hosts, addresses, err)
	}
}

func TestResolveSelectedInterfaceFailsClosed(t *testing.T) {
	interfaces := []Interface{{Name: "eth0", Addresses: []Address{{Host: "192.168.1.20", Interface: "eth0", Scope: "lan"}}}}
	if _, _, err := Resolve(config.ExposureConfig{Mode: config.ExposureInterfaces, Interfaces: []string{"missing"}}, interfaces); err == nil {
		t.Fatal("missing interface was accepted")
	}
}
