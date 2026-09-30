//go:build darwin

package procstat

/*
#include <libproc.h>
#include <sys/proc.h>
#include <sys/proc_info.h>
#include <stdint.h>
#include <mach/mach_time.h>

static void devyard_timebase(uint32_t *numer, uint32_t *denom) {
	mach_timebase_info_data_t tb;
	mach_timebase_info(&tb);
	*numer = tb.numer;
	*denom = tb.denom;
}
*/
import "C"

import (
	"sync"
	"time"
	"unsafe"
)

// pti_total_user/system are in Mach absolute time units, which equal
// nanoseconds on Intel but not on Apple Silicon (numer/denom = 125/3).
var timebase = sync.OnceValues(func() (uint64, uint64) {
	var numer, denom C.uint32_t
	C.devyard_timebase(&numer, &denom)
	if denom == 0 {
		return 1, 1
	}
	return uint64(numer), uint64(denom)
})

func machToDuration(ticks uint64) time.Duration {
	numer, denom := timebase()
	return time.Duration(ticks / denom * numer)
}

// processInfo inspects a single process via libproc, returning its process
// group id and zombie state.
func processInfo(pid int) (ProcessInfo, bool) {
	if pid <= 0 {
		return ProcessInfo{}, false
	}
	var bsd C.struct_proc_bsdinfo
	if C.proc_pidinfo(C.int(pid), C.PROC_PIDTBSDINFO, 0, unsafe.Pointer(&bsd), C.int(C.sizeof_struct_proc_bsdinfo)) <= 0 {
		return ProcessInfo{}, false
	}
	return ProcessInfo{
		PGID:   int(bsd.pbi_pgid),
		Zombie: bsd.pbi_status == C.SZOMB,
	}, true
}

// sampleGroup enumerates every process via libproc, sums the CPU time (from
// the task's total user/system nanoseconds) and resident size (bytes) of every
// process whose process group matches pgid.
func sampleGroup(pgid int) (Sample, bool) {
	if pgid <= 0 {
		return Sample{}, false
	}

	// First call with a nil buffer returns the number of pids; grow a buffer
	// of that size and re-query for the actual list.
	size := C.proc_listallpids(nil, 0)
	if size <= 0 {
		return Sample{}, false
	}
	buf := make([]C.pid_t, size)
	n := C.proc_listallpids(unsafe.Pointer(&buf[0]), C.int(int(size)*int(unsafe.Sizeof(buf[0]))))
	if n <= 0 {
		return Sample{}, false
	}

	var s Sample
	for i := C.int(0); i < n; i++ {
		pid := int(buf[i])
		var bsd C.struct_proc_bsdinfo
		if C.proc_pidinfo(C.int(pid), C.PROC_PIDTBSDINFO, 0, unsafe.Pointer(&bsd), C.int(C.sizeof_struct_proc_bsdinfo)) <= 0 {
			continue
		}
		if int(bsd.pbi_pgid) != pgid {
			continue
		}
		var ti C.struct_proc_taskinfo
		if C.proc_pidinfo(C.int(pid), C.PROC_PIDTASKINFO, 0, unsafe.Pointer(&ti), C.int(C.sizeof_struct_proc_taskinfo)) <= 0 {
			continue
		}
		s.Procs++
		s.CPU += machToDuration(uint64(ti.pti_total_user) + uint64(ti.pti_total_system))
		s.RSS += uint64(ti.pti_resident_size)
	}
	return s, s.Procs > 0
}
