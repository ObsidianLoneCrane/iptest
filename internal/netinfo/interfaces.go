// Package netinfo enumerates local interface addresses without sending traffic.
package netinfo

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
)

// Address associates a usable local IP with its interface name.
type Address struct {
	Interface string
	IP        netip.Addr
}

// List returns addresses on interfaces marked up, including virtual interfaces.
// Loopback, link-local, multicast, and unspecified addresses are excluded.
// A partial result is returned if reading one of the interfaces fails.
func List() ([]Address, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("enumerate network interfaces: %w", err)
	}
	return collect(interfaces, func(iface net.Interface) ([]net.Addr, error) {
		return iface.Addrs()
	})
}

func collect(interfaces []net.Interface, read func(net.Interface) ([]net.Addr, error)) ([]Address, error) {
	var result []Address
	var failures []error
	seen := make(map[Address]bool)
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := read(iface)
		if err != nil {
			failures = append(failures, fmt.Errorf("read addresses for %s: %w", iface.Name, err))
			continue
		}
		for _, raw := range addresses {
			ip, ok := interfaceIP(raw)
			if !ok || !ip.IsGlobalUnicast() {
				continue
			}
			address := Address{Interface: iface.Name, IP: ip}
			if !seen[address] {
				seen[address] = true
				result = append(result, address)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Interface != result[j].Interface {
			return result[i].Interface < result[j].Interface
		}
		return result[i].IP.Compare(result[j].IP) < 0
	})
	return result, errors.Join(failures...)
}

func interfaceIP(raw net.Addr) (netip.Addr, bool) {
	var bytes net.IP
	switch value := raw.(type) {
	case *net.IPNet:
		if value != nil {
			bytes = value.IP
		}
	case *net.IPAddr:
		if value != nil {
			bytes = value.IP
		}
	default:
		return netip.Addr{}, false
	}
	ip, ok := netip.AddrFromSlice(bytes)
	return ip.Unmap(), ok
}
