package network

import (
	"fmt"
	"net"
	"sort"

	"go.mewis.me/codemcp/internal/config"
)

const (
	LoopbackHost = "127.0.0.1"
	WildcardHost = "0.0.0.0"
)

type Address struct {
	Host      string `json:"address"`
	Interface string `json:"interface,omitempty"`
	Scope     string `json:"scope"`
}

type Interface struct {
	Name      string    `json:"name"`
	Addresses []Address `json:"addresses"`
}

type Candidate struct {
	IP        net.IP
	Interface string
	Flags     net.Flags
}

func Discover() ([]Interface, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	candidates := make([]Candidate, 0)
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			var ip net.IP
			switch value := address.(type) {
			case *net.IPNet:
				ip = value.IP
			case *net.IPAddr:
				ip = value.IP
			}
			if ip != nil {
				candidates = append(candidates, Candidate{IP: ip, Interface: iface.Name, Flags: iface.Flags})
			}
		}
	}
	return NormalizeInterfaces(candidates), nil
}

func NormalizeInterfaces(candidates []Candidate) []Interface {
	byName := map[string][]Address{}
	seenHost := map[string]map[string]struct{}{}
	for _, candidate := range candidates {
		if candidate.Interface == "" || candidate.Flags&net.FlagUp == 0 || candidate.Flags&net.FlagLoopback != 0 {
			continue
		}
		ip := candidate.IP
		if ipv4 := ip.To4(); ipv4 != nil {
			ip = ipv4
		} else {
			ip = ip.To16()
		}
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || !ip.IsGlobalUnicast() {
			continue
		}
		host := ip.String()
		if seenHost[candidate.Interface] == nil {
			seenHost[candidate.Interface] = map[string]struct{}{}
		}
		if _, exists := seenHost[candidate.Interface][host]; exists {
			continue
		}
		seenHost[candidate.Interface][host] = struct{}{}
		scope := "network"
		if ip.IsPrivate() {
			scope = "lan"
		}
		byName[candidate.Interface] = append(byName[candidate.Interface], Address{Host: host, Interface: candidate.Interface, Scope: scope})
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]Interface, 0, len(names))
	for _, name := range names {
		addresses := byName[name]
		sort.Slice(addresses, func(i, j int) bool { return addresses[i].Host < addresses[j].Host })
		result = append(result, Interface{Name: name, Addresses: addresses})
	}
	return result
}

func Resolve(exposure config.ExposureConfig, interfaces []Interface) ([]string, []Address, error) {
	exposure = config.NormalizeExposure(exposure)
	loopback := Address{Host: LoopbackHost, Scope: "local"}
	switch exposure.Mode {
	case config.ExposureNone:
		return []string{LoopbackHost}, []Address{loopback}, nil
	case config.ExposureAll:
		hosts := []string{LoopbackHost}
		addresses := []Address{loopback}
		seen := map[string]struct{}{LoopbackHost: {}}
		for _, iface := range interfaces {
			for _, address := range iface.Addresses {
				if _, ok := seen[address.Host]; ok {
					continue
				}
				seen[address.Host] = struct{}{}
				hosts = append(hosts, address.Host)
				addresses = append(addresses, address)
			}
		}
		return hosts, addresses, nil
	case config.ExposureWildcard:
		addresses := []Address{loopback}
		seen := map[string]struct{}{LoopbackHost: {}}
		for _, iface := range interfaces {
			for _, address := range iface.Addresses {
				if net.ParseIP(address.Host).To4() == nil {
					continue
				}
				if _, ok := seen[address.Host]; ok {
					continue
				}
				seen[address.Host] = struct{}{}
				addresses = append(addresses, address)
			}
		}
		return []string{WildcardHost}, addresses, nil
	case config.ExposureInterfaces:
		byName := make(map[string]Interface, len(interfaces))
		for _, iface := range interfaces {
			byName[iface.Name] = iface
		}
		hosts := []string{LoopbackHost}
		addresses := []Address{loopback}
		seen := map[string]struct{}{LoopbackHost: {}}
		for _, name := range exposure.Interfaces {
			iface, ok := byName[name]
			if !ok || len(iface.Addresses) == 0 {
				return nil, nil, fmt.Errorf("configured network interface %q is unavailable, down, loopback-only, or has no eligible IP address", name)
			}
			for _, address := range iface.Addresses {
				if _, ok := seen[address.Host]; ok {
					continue
				}
				seen[address.Host] = struct{}{}
				hosts = append(hosts, address.Host)
				addresses = append(addresses, address)
			}
		}
		return hosts, addresses, nil
	default:
		return nil, nil, fmt.Errorf("unsupported network exposure mode %q", exposure.Mode)
	}
}

func ResolveCurrent(exposure config.ExposureConfig) ([]string, []Address, error) {
	interfaces, err := Discover()
	if err != nil {
		return nil, nil, err
	}
	return Resolve(exposure, interfaces)
}
