package platform

import (
	"net"
	"testing"
)

func TestProviderEndpointIPAllowed(t *testing.T) {
	tests := []struct {
		name         string
		address      string
		allowPrivate bool
		want         bool
	}{
		{name: "public IPv4", address: "8.8.8.8", want: true},
		{name: "public IPv6", address: "2606:4700:4700::1111", want: true},
		{name: "private", address: "10.0.0.1"},
		{name: "loopback", address: "127.0.0.1"},
		{name: "shared address lower bound", address: "100.64.0.0"},
		{name: "shared address upper bound", address: "100.127.255.255"},
		{name: "before shared address", address: "100.63.255.255", want: true},
		{name: "after shared address", address: "100.128.0.0", want: true},
		{name: "explicit private opt in", address: "100.64.0.1", allowPrivate: true, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ProviderEndpointIPAllowed(net.ParseIP(test.address), test.allowPrivate); got != test.want {
				t.Fatalf("ProviderEndpointIPAllowed(%q, %v) = %v, want %v", test.address, test.allowPrivate, got, test.want)
			}
		})
	}
}
