package egress

import (
	"net/netip"
	"testing"
)

func TestPublicAddressSpecialUse(t *testing.T) {
	for _, address := range []string{
		"0.1.2.3", "100.64.0.1", "100.127.255.255", "192.0.0.8", "192.0.0.11",
		"192.0.2.1", "192.88.99.2", "198.18.0.1", "198.19.255.255",
		"198.51.100.1", "203.0.113.10", "240.0.0.1", "255.255.255.254",
		"64:ff9b:1::1", "100::1", "100:0:0:1::1", "2001:1::4", "2001:2::1",
		"2001:db8::10", "3fff::1", "5f00::1",
	} {
		t.Run(address, func(t *testing.T) {
			if publicAddress(netip.MustParseAddr(address)) {
				t.Fatal("non-public special-purpose address was accepted")
			}
		})
	}
	for _, address := range []string{
		"8.8.8.8", "1.1.1.1", "100.63.255.255", "100.128.0.1", "192.0.0.9", "192.0.0.10",
		"198.17.255.255", "198.20.0.1", "2606:4700:4700::1111", "2001:4860:4860::8888",
		"64:ff9b::808:808", "2001::1234", "2001:1::1", "2001:1::2", "2001:1::3",
		"2001:3::1", "2001:4:112::1", "2001:20::1", "2001:30::1", "2002::1",
	} {
		t.Run(address, func(t *testing.T) {
			if !publicAddress(netip.MustParseAddr(address)) {
				t.Fatal("public or explicitly allowed transition address was rejected")
			}
		})
	}
}
