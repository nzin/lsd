// Package netutil holds small networking helpers shared by agent and server.
package netutil

import (
	"net"
	"net/netip"
)

// LocalIPs lists the unicast addresses of this network namespace that other
// nodes could see us under (no loopback / link-local / multicast).
func LocalIPs() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip, ok := netip.AddrFromSlice(ipn.IP)
		if !ok {
			continue
		}
		ip = ip.Unmap()
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
			continue
		}
		out = append(out, ip.String())
	}
	return out
}
