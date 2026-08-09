//go:build linux

package ports

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func inspectNativeOrLsof(pgids []int) ([]Binding, error) {
	pids, pidToPGID := findPIDsForPGIDs(pgids)
	if len(pids) == 0 {
		return nil, nil
	}
	bindings, err := inspectLsof(pids)
	if err != nil {
		return nil, err
	}
	for i := range bindings {
		bindings[i].PGID = pidToPGID[bindings[i].PID]
	}
	return bindings, nil
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
