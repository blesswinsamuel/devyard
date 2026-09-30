//go:build linux

package ports

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

func inspectNativeOrLsof(pgids []int) ([]Binding, error) {
	pids, pidToPGID := findPIDsForPGIDs(pgids)
	if len(pids) == 0 {
		return nil, nil
	}
	bindings, err := inspectProc(pids)
	if err != nil {
		if bindings, err = inspectLsof(pids); err != nil {
			return nil, err
		}
	}
	for i := range bindings {
		bindings[i].PGID = pidToPGID[bindings[i].PID]
	}
	return bindings, nil
}

// inspectProc finds listening sockets without lsof: it maps each process's
// socket fds to inodes and matches them against /proc/net/{tcp,tcp6,udp,udp6}.
func inspectProc(pids []int) ([]Binding, error) {
	inodeToPID := map[string]int{}
	for _, pid := range pids {
		dir := fmt.Sprintf("/proc/%d/fd", pid)
		fds, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(dir + "/" + fd.Name())
			if err != nil || !strings.HasPrefix(target, "socket:[") {
				continue
			}
			inodeToPID[strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")] = pid
		}
	}
	var out []Binding
	seen := map[string]bool{}
	for _, table := range []struct {
		file, proto string
		v6          bool
		state       string // 0A = LISTEN (tcp), 07 = unconnected (udp)
	}{
		{"/proc/net/tcp", "tcp", false, "0A"},
		{"/proc/net/tcp6", "tcp", true, "0A"},
		{"/proc/net/udp", "udp", false, "07"},
		{"/proc/net/udp6", "udp", true, "07"},
	} {
		data, err := os.ReadFile(table.file)
		if err != nil {
			if table.file == "/proc/net/tcp" {
				return nil, err
			}
			continue
		}
		for _, line := range strings.Split(string(data), "\n")[1:] {
			f := strings.Fields(line)
			if len(f) < 10 || f[3] != table.state {
				continue
			}
			pid, ok := inodeToPID[f[9]]
			if !ok {
				continue
			}
			ip, port, ok := parseProcAddr(f[1], table.v6)
			if !ok {
				continue
			}
			key := fmt.Sprintf("%d/%s/%s/%d", pid, table.proto, ip, port)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, Binding{PID: pid, IP: ip, Port: port, Protocol: table.proto})
		}
	}
	return out, nil
}

// parseProcAddr decodes "0100007F:1F90" (little-endian words) into an IP
// string and port.
func parseProcAddr(s string, v6 bool) (string, int, bool) {
	hexIP, hexPort, ok := strings.Cut(s, ":")
	if !ok {
		return "", 0, false
	}
	port, err := strconv.ParseUint(hexPort, 16, 16)
	if err != nil {
		return "", 0, false
	}
	raw, err := hex.DecodeString(hexIP)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return "", 0, false
	}
	// Each 32-bit word is stored in host (little-endian) order.
	for i := 0; i+4 <= len(raw); i += 4 {
		raw[i], raw[i+1], raw[i+2], raw[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
	}
	ip := net.IP(raw)
	if !v6 {
		ip = ip.To4()
	}
	return ip.String(), int(port), true
}

func findPIDsForPGIDs(pgids []int) ([]int, map[int]int) {
	pgidSet := make(map[int]bool, len(pgids))
	for _, p := range pgids {
		if p > 0 {
			pgidSet[p] = true
		}
	}
	if len(pgidSet) == 0 {
		return nil, nil
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, nil
	}

	var matchingPIDs []int
	pidToPGID := make(map[int]int)
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		if pgrp, ok := getPIDPGID(pid, pgidSet); ok {
			matchingPIDs = append(matchingPIDs, pid)
			pidToPGID[pid] = pgrp
		}
	}
	return matchingPIDs, pidToPGID
}

func getPIDPGID(pid int, pgidSet map[int]bool) (int, bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, false
	}
	close := bytes.LastIndexByte(data, ')')
	if close < 0 || close+2 > len(data) {
		return 0, false
	}
	fields := strings.Fields(string(data[close+2:]))
	if len(fields) < 3 {
		return 0, false
	}
	pgrp, err := strconv.Atoi(fields[2])
	if err != nil || !pgidSet[pgrp] {
		return 0, false
	}
	return pgrp, true
}
