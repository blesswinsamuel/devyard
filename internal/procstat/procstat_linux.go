//go:build linux

package procstat

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// clockTicksPerSec is the fixed user/kernel clock tick rate on Linux
// (100 Hz), which is what utime/stime in /proc/<pid>/stat are measured in.
const clockTicksPerSec = 100

// sampleGroup scans /proc and sums the CPU time and RSS of every process whose
// process group matches pgid.
func sampleGroup(pgid int) (Sample, bool) {
	if pgid <= 0 {
		return Sample{}, false
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return Sample{}, false
	}

	var s Sample
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		if ps, ok := sampleProc(pid, pgid); ok {
			s.CPU += ps.CPU
			s.RSS += ps.RSS
			s.Procs++
		}
	}
	return s, s.Procs > 0
}

// sampleProc reads /proc/<pid>/stat and, if the process belongs to pgid,
// returns its cumulative CPU time and RSS in pages converted to bytes.
func sampleProc(pid, pgid int) (Sample, bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return Sample{}, false
	}
	// Field 2 (comm) may contain spaces and parentheses, so skip past the
	// last ')' and treat the remainder as whitespace-separated fields 3+.
	close := bytes.LastIndexByte(data, ')')
	if close < 0 || close+2 > len(data) {
		return Sample{}, false
	}
	fields := strings.Fields(string(data[close+2:]))
	// After stripping "pid (comm) ", index i is stat field i+3:
	//   fields[2]  -> field 5  pgrp
	//   fields[11] -> field 14 utime (clock ticks)
	//   fields[12] -> field 15 stime (clock ticks)
	//   fields[21] -> field 24 rss (pages)
	if len(fields) < 22 {
		return Sample{}, false
	}
	pgrp, err := strconv.Atoi(fields[2])
	if err != nil || pgrp != pgid {
		return Sample{}, false
	}
	utime, _ := strconv.ParseInt(fields[11], 10, 64)
	stime, _ := strconv.ParseInt(fields[12], 10, 64)
	pages, _ := strconv.ParseInt(fields[21], 10, 64)
	if utime < 0 || stime < 0 {
		utime, stime = 0, 0
	}
	return Sample{
		CPU: time.Duration(utime+stime) * time.Second / clockTicksPerSec,
		RSS: uint64(pages) * uint64(os.Getpagesize()),
	}, true
}
