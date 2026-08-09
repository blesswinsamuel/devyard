//go:build darwin

package ports

/*
#include <libproc.h>
#include <sys/proc_info.h>
#include <stdint.h>
*/
import "C"
import "unsafe"

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

	size := C.proc_listallpids(nil, 0)
	if size <= 0 {
		return nil, nil
	}
	buf := make([]C.pid_t, size)
	n := C.proc_listallpids(unsafe.Pointer(&buf[0]), C.int(int(size)*int(unsafe.Sizeof(buf[0]))))
	if n <= 0 {
		return nil, nil
	}

	var matchingPIDs []int
	pidToPGID := make(map[int]int)
	for i := C.int(0); i < n; i++ {
		pid := int(buf[i])
		if pid <= 0 {
			continue
		}
		var bsd C.struct_proc_bsdinfo
		if C.proc_pidinfo(C.int(pid), C.PROC_PIDTBSDINFO, 0, unsafe.Pointer(&bsd), C.int(C.sizeof_struct_proc_bsdinfo)) <= 0 {
			continue
		}
		pgid := int(bsd.pbi_pgid)
		if pgidSet[pgid] {
			matchingPIDs = append(matchingPIDs, pid)
			pidToPGID[pid] = pgid
		}
	}
	return matchingPIDs, pidToPGID
}
