package netinfo

import (
	"errors"
	"net"
	"net/netip"
	"reflect"
	"testing"
)

func TestCollectFiltersAndSortsAddresses(t *testing.T) {
	interfaces := []net.Interface{
		{Name: "wifi", Flags: net.FlagUp},
		{Name: "down"},
		{Name: "loopback", Flags: net.FlagUp | net.FlagLoopback},
		{Name: "ethernet", Flags: net.FlagUp},
	}
	reads := 0
	got, err := collect(interfaces, func(iface net.Interface) ([]net.Addr, error) {
		reads++
		if iface.Name == "ethernet" {
			return []net.Addr{&net.IPNet{IP: net.ParseIP("10.0.0.3")}}, nil
		}
		var addresses []net.Addr
		for _, ip := range []string{"fd00::2", "192.168.1.20", "192.168.1.20", "::ffff:192.168.1.20", "127.0.0.1", "::1", "169.254.1.2", "fe80::2", "224.0.0.1", "0.0.0.0", "::"} {
			addresses = append(addresses, &net.IPNet{IP: net.ParseIP(ip)})
		}
		addresses = append(addresses, &net.IPAddr{IP: net.ParseIP("192.168.1.21")}, (*net.IPNet)(nil), (*net.IPAddr)(nil))
		return addresses, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Address{
		{Interface: "ethernet", IP: netip.MustParseAddr("10.0.0.3")},
		{Interface: "wifi", IP: netip.MustParseAddr("192.168.1.20")},
		{Interface: "wifi", IP: netip.MustParseAddr("192.168.1.21")},
		{Interface: "wifi", IP: netip.MustParseAddr("fd00::2")},
	}
	if reads != 2 || !reflect.DeepEqual(got, want) {
		t.Fatalf("reads = %d, addresses = %#v; want %#v", reads, got, want)
	}
}

func TestCollectPreservesPartialResults(t *testing.T) {
	failure := errors.New("interface unavailable")
	got, err := collect([]net.Interface{
		{Name: "broken", Flags: net.FlagUp},
		{Name: "usable", Flags: net.FlagUp},
	}, func(iface net.Interface) ([]net.Addr, error) {
		if iface.Name == "broken" {
			return nil, failure
		}
		return []net.Addr{&net.IPNet{IP: net.ParseIP("10.0.0.2")}}, nil
	})
	if !errors.Is(err, failure) || len(got) != 1 || got[0].Interface != "usable" {
		t.Fatalf("got %#v, %v; want the usable address and partial error", got, err)
	}
}
