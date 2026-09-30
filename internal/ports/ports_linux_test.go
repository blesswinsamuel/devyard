//go:build linux

package ports

import "testing"

func TestParseProcAddr(t *testing.T) {
	cases := []struct {
		in   string
		v6   bool
		ip   string
		port int
	}{
		{"0100007F:1F90", false, "127.0.0.1", 8080},
		{"00000000:0050", false, "0.0.0.0", 80},
		{"00000000000000000000000001000000:0BB8", true, "::1", 3000},
	}
	for _, c := range cases {
		ip, port, ok := parseProcAddr(c.in, c.v6)
		if !ok || ip != c.ip || port != c.port {
			t.Errorf("parseProcAddr(%q) = %q %d %v, want %q %d", c.in, ip, port, ok, c.ip, c.port)
		}
	}
}
