package ports

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Binding describes one open listening socket bound by a process.
type Binding struct {
	PID      int    `json:"pid"`
	PGID     int    `json:"pgid"`
	IP       string `json:"ip"`       // e.g. "127.0.0.1", "0.0.0.0", "::1", "*"
	Port     int    `json:"port"`     // e.g. 3000
	Protocol string `json:"protocol"` // "tcp" or "udp"
}

// InspectPGIDs returns all open listening sockets for processes matching the given process group IDs.
func InspectPGIDs(pgids []int) ([]Binding, error) {
	if len(pgids) == 0 {
		return nil, nil
	}
	return inspectNativeOrLsof(pgids)
}

// inspectLsof invokes `lsof` as a fallback or cross-platform inspector for process groups / PIDs.
func inspectLsof(pids []int) ([]Binding, error) {
	if len(pids) == 0 {
		return nil, nil
	}

	pidStrs := make([]string, len(pids))
	for i, pid := range pids {
		pidStrs[i] = strconv.Itoa(pid)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// lsof -nP -iTCP -sTCP:LISTEN -iUDP -a -p <pids>
	cmd := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-iUDP", "-a", "-p", strings.Join(pidStrs, ","))
	var out bytes.Buffer
	cmd.Stdout = &out

	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return nil, fmt.Errorf("lsof: %w", err)
		}
		// lsof exits 1 when ANY -i selection had no match — including the UDP
		// half of this query — while stdout still carries TCP LISTEN rows.
		// Exit status therefore doesn't mean "no matches": always parse the
		// output; empty output means no listeners.
	}

	return parseLsofOutput(out.Bytes())
}

// parseLsofOutput parses the tabular stdout of `lsof -nP -iTCP -sTCP:LISTEN -iUDP ...`.
func parseLsofOutput(data []byte) ([]Binding, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	var results []Binding
	seen := make(map[string]bool)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "COMMAND") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}

		pid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}

		proto := strings.ToLower(fields[7])
		if proto != "tcp" && proto != "udp" {
			for _, f := range fields {
				lf := strings.ToLower(f)
				if lf == "tcp" || lf == "udp" {
					proto = lf
					break
				}
			}
		}

		nameField := fields[len(fields)-1]
		if strings.HasSuffix(nameField, "(LISTEN)") {
			if len(fields) >= 2 {
				nameField = fields[len(fields)-2]
			}
		}

		nameField = strings.TrimSuffix(nameField, "(LISTEN)")
		nameField = strings.TrimSpace(nameField)

		host, portStr, err := splitHostPort(nameField)
		if err != nil {
			continue
		}

		port, err := strconv.Atoi(portStr)
		if err != nil || port <= 0 {
			continue
		}

		if host == "*" || host == "" {
			host = "0.0.0.0"
		}

		key := fmt.Sprintf("%d:%s:%d:%s", pid, host, port, proto)
		if seen[key] {
			continue
		}
		seen[key] = true

		results = append(results, Binding{
			PID:      pid,
			IP:       host,
			Port:     port,
			Protocol: proto,
		})
	}

	return results, scanner.Err()
}

func splitHostPort(addr string) (string, string, error) {
	if host, port, err := net.SplitHostPort(addr); err == nil {
		return host, port, nil
	}

	idx := strings.LastIndex(addr, ":")
	if idx < 0 {
		return "", "", fmt.Errorf("invalid address format: %s", addr)
	}
	return addr[:idx], addr[idx+1:], nil
}
