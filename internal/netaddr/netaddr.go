// Package netaddr finds the laptop's LAN addresses for the phone URL.
package netaddr

import (
	"fmt"
	"net"
	"strings"
)

var virtualPrefixes = []string{"docker", "lxcbr", "veth", "virbr", "br-", "vmnet", "vboxnet", "utun", "tun", "tap", "zt", "tailscale"}

func skipInterface(name string) bool {
	n := strings.ToLower(name)
	for _, p := range virtualPrefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

// defaultRouteIP returns the local address used to reach the outside world.
// UDP "dial" sends no packets.
func defaultRouteIP() string {
	c, err := net.Dial("udp4", "192.0.2.1:9")
	if err != nil {
		return ""
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}

// LANIPv4s lists usable IPv4 addresses, default-route address first.
func LANIPv4s() ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	def := defaultRouteIP()
	var ips []string
	seen := map[string]bool{}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || skipInterface(ifc.Name) {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipn.IP.To4()
			if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
				continue
			}
			s := ip4.String()
			if seen[s] {
				continue
			}
			seen[s] = true
			if s == def {
				ips = append([]string{s}, ips...)
			} else {
				ips = append(ips, s)
			}
		}
	}
	return ips, nil
}

func URL(ip string, port int) string { return fmt.Sprintf("http://%s:%d", ip, port) }
