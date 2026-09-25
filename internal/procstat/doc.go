// Package procstat samples aggregate CPU and memory usage of a process group.
//
// A devyard service runs as a process-group leader (its shell), so a
// single service's real resource footprint spans every process in that group.
// The package hides the per-platform source of truth behind one function:
// procfs on Linux, the libproc APIs on macOS. The `top` command uses it to
// render per-service CPU% and memory.
//
// Build tags confine the implementation to the platforms the project targets
// (macOS + Linux); there is deliberately no fallback for other operating
// systems.
package procstat

import "time"

// Sample is a point-in-time snapshot of a process group's aggregate resource
// usage: the cumulative CPU time (user + system) of every live process in the
// group and their total resident set size.
type Sample struct {
	// CPU is the cumulative CPU time consumed by every live process in the
	// group since each process started.
	CPU time.Duration
	// RSS is the aggregate resident set size in bytes.
	RSS uint64
	// Procs is the number of live processes observed in the group.
	Procs int
}

// SampleGroup returns an aggregate snapshot of every process whose process
// group id equals pgid. It reports false when the group does not exist (e.g.
// the service has not been launched or its group has exited).
func SampleGroup(pgid int) (Sample, bool) {
	return sampleGroup(pgid)
}

// ProcessInfo describes the liveness of a single process.
type ProcessInfo struct {
	// PGID is the process group id the process currently belongs to.
	PGID int
	// Zombie is true when the process has exited but has not been reaped.
	Zombie bool
}

// InspectProcess inspects a single process by pid, returning its current
// process group id and zombie state. It reports false when pid does not exist
// or cannot be inspected. Used by the supervisor to verify adoption
// candidates: a live process group is only re-attachable when the recorded
// leader still exists and still leads the recorded group.
func InspectProcess(pid int) (ProcessInfo, bool) {
	return processInfo(pid)
}
