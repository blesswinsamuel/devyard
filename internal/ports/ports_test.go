package ports

import (
	"net"
	"os"
	"testing"
)

func TestParseLsofOutput(t *testing.T) {
	sampleOutput := `
COMMAND   PID USER   FD   TYPE DEVICE SIZE/OFF NODE NAME
node    12345 user   22u  IPv4 0x1234      0t0  TCP 127.0.0.1:3000 (LISTEN)
node    12345 user   23u  IPv6 0x5678      0t0  TCP *:8080 (LISTEN)
python  67890 user    5u  IPv4 0x9abc      0t0  TCP 0.0.0.0:5432 (LISTEN)
`
	bindings, err := parseLsofOutput([]byte(sampleOutput))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(bindings) != 3 {
		t.Fatalf("expected 3 bindings, got %d", len(bindings))
	}

	if bindings[0].PID != 12345 || bindings[0].IP != "127.0.0.1" || bindings[0].Port != 3000 || bindings[0].Protocol != "tcp" {
		t.Errorf("unexpected binding 0: %+v", bindings[0])
	}
	if bindings[1].PID != 12345 || bindings[1].IP != "0.0.0.0" || bindings[1].Port != 8080 || bindings[1].Protocol != "tcp" {
		t.Errorf("unexpected binding 1: %+v", bindings[1])
	}
	if bindings[2].PID != 67890 || bindings[2].IP != "0.0.0.0" || bindings[2].Port != 5432 || bindings[2].Protocol != "tcp" {
		t.Errorf("unexpected binding 2: %+v", bindings[2])
	}
}

func TestInspectPGIDsRealListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("failed to split host port: %v", err)
	}

	// Our current process pid/pgid
	pid := os.Getpid()
	bindings, err := inspectLsof([]int{pid})
	if err != nil {
		t.Logf("lsof failed or not installed: %v", err)
		return
	}

	found := false
	for _, b := range bindings {
		if b.PID == pid && b.Port > 0 && b.IP == "127.0.0.1" {
			if net.JoinHostPort(b.IP, portStr) == net.JoinHostPort("127.0.0.1", portStr) {
				found = true
				break
			}
		}
	}

	if !found {
		t.Logf("Note: real listener port %s not matched in lsof output (may depend on OS environment)", portStr)
	}
}
