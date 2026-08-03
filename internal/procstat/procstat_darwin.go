//go:build darwin

package procstat

/*
#include <libproc.h>
#include <sys/proc_info.h>
#include <stdint.h>
*/
import "C"

import (
	"time"
	"unsafe"
)

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
		s.CPU += time.Duration(uint64(ti.pti_total_user) + uint64(ti.pti_total_system))
		s.RSS += uint64(ti.pti_resident_size)
	}
	return s, s.Procs > 0
}
