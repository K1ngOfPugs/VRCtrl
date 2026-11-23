package netaddr

import (
	"net"
	"strings"
	"testing"
)

func TestURL(t *testing.T) {
	if got := URL("10.0.0.5", 8080); got != "http://10.0.0.5:8080" {
		t.Fatalf("URL = %q", got)
	}
}

func TestLANIPv4sNoLoopback(t *testing.T) {
	ips, err := LANIPv4s()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, ip := range ips {
		if strings.HasPrefix(ip, "127.") || strings.Contains(ip, ":") {
			t.Fatalf("bad ip %q in %v", ip, ips)
		}
		if seen[ip] {
			t.Fatalf("duplicate %q", ip)
		}
		seen[ip] = true
	}
}

func TestSkipInterface(t *testing.T) {
	for _, n := range []string{"docker0", "lxcbr0", "veth12ab", "virbr0", "br-3f2a", "vmnet8", "vboxnet0", "utun3", "CloudflareWARP", "wg0", "nordlynx", "ppp0"} {
		if !skipInterface(n) {
			t.Errorf("%s should be skipped", n)
		}
	}
	for _, n := range []string{"wlo1", "eth0", "en0", "Wi-Fi", "Ethernet"} {
		if skipInterface(n) {
			t.Errorf("%s should not be skipped", n)
		}
	}
}

func TestIsHostOnly(t *testing.T) {
	_, warp, _ := net.ParseCIDR("172.16.0.2/32")
	_, lan, _ := net.ParseCIDR("10.50.192.0/19")
	if !isHostOnly(warp) || isHostOnly(lan) {
		t.Fatal("isHostOnly wrong")
	}
}
