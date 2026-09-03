package platform

import "net"

// ProviderEndpointIPAllowed rejects local and shared address space unless the
// operator explicitly opts into private provider endpoints.
func ProviderEndpointIPAllowed(ip net.IP, allowPrivate bool) bool {
	if allowPrivate {
		return true
	}
	if ip == nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	ipv4 := ip.To4()
	return ipv4 == nil || ipv4[0] != 100 || ipv4[1]&0xc0 != 0x40
}
