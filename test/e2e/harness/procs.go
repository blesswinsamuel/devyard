package harness

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// SandboxIDVar tags every process started through a sandbox.
const SandboxIDVar = "DEVYARD_SANDBOX_ID"

// sandboxIDPrefix starts every tag: dye2e-<owner pid>-<run nonce>-<seq>.
const sandboxIDPrefix = "dye2e-"

// ProcInfo describes one process on the machine as seen by the sweeper.
type ProcInfo struct {
	Pid     int
	PPid    int
	Pgid    int
	Command string // full command line (best effort)
	Exe     string // executable path or argv[0]
	// EnvReadable is false when the OS hides the environment (e.g. macOS
	// platform binaries such as /bin/sh).
	EnvReadable bool

	env    []string // Linux: exact entries
	rawEnv string   // macOS: "cmd args KEY=V KEY=V" as printed by ps -E
}

// EnvValue returns the value of key in the process's initial environment.
func (p ProcInfo) EnvValue(key string) (string, bool) {
	if p.env != nil {
		prefix := key + "="
		for i := len(p.env) - 1; i >= 0; i-- {
			if strings.HasPrefix(p.env[i], prefix) {
				return p.env[i][len(prefix):], true
			}
		}
		return "", false
	}
	if p.rawEnv == "" {
		return "", false
	}
	needle := " " + key + "="
	i := strings.LastIndex(p.rawEnv, needle)
	if i < 0 {
		return "", false
	}
	v := p.rawEnv[i+len(needle):]
	if j := strings.IndexByte(v, ' '); j >= 0 {
		v = v[:j]
	}
	return v, true
}

// SandboxID returns the process's DEVYARD_SANDBOX_ID tag, if any.
func (p ProcInfo) SandboxID() string {
	v, _ := p.EnvValue(SandboxIDVar)
	return v
}

func (p ProcInfo) String() string {
	cmd := p.Command
	if len(cmd) > 160 {
		cmd = cmd[:160] + "..."
	}
	return fmt.Sprintf("pid=%d ppid=%d pgid=%d tag=%q cmd=%q", p.Pid, p.PPid, p.Pgid, p.SandboxID(), cmd)
}

// ListProcesses returns every process visible to the current user.
func ListProcesses() ([]ProcInfo, error) {
	return listProcesses()
}

// ProcessesWithTag returns processes whose tag equals id exactly.
func ProcessesWithTag(id string) ([]ProcInfo, error) {
	return filterProcs(func(p ProcInfo) bool { return p.SandboxID() == id })
}

func filterProcs(match func(ProcInfo) bool) ([]ProcInfo, error) {
	all, err := listProcesses()
	if err != nil {
		return nil, err
	}
	var out []ProcInfo
	for _, p := range all {
		if match(p) {
			out = append(out, p)
		}
	}
	return out, nil
}

// PidAlive reports whether pid exists (EPERM counts as alive).
func PidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// GroupAlive reports whether any process in process group pgid exists.
func GroupAlive(pgid int) bool {
	if pgid <= 1 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// protectedPids returns this process and all of its ancestors: never kill
// them, whatever their tags or groups say.
func protectedPids(all []ProcInfo) map[int]bool {
	byPid := make(map[int]ProcInfo, len(all))
	for _, p := range all {
		byPid[p.Pid] = p
	}
	prot := map[int]bool{0: true, 1: true}
	pid := os.Getpid()
	for i := 0; i < 64 && pid > 1; i++ {
		prot[pid] = true
		p, ok := byPid[pid]
		if !ok {
			break
		}
		pid = p.PPid
	}
	prot[os.Getppid()] = true
	return prot
}

// expandTagged returns the processes to reap for a tag predicate: every
// tagged process, all of their descendants, and every member of a process
// group led by one of those. The expansion exists because macOS hides the
// environment of platform binaries (/bin/sh, /bin/sleep), so a shell under a
// tagged runner is only reachable through the process tree. Protected pids
// (this process and its ancestors) and their groups are never included.
func expandTagged(all []ProcInfo, tagged func(ProcInfo) bool) []ProcInfo {
	prot := protectedPids(all)
	protGroups := map[int]bool{}
	for _, p := range all {
		if prot[p.Pid] {
			protGroups[p.Pgid] = true
		}
	}
	children := map[int][]ProcInfo{}
	for _, p := range all {
		children[p.PPid] = append(children[p.PPid], p)
	}
	set := map[int]ProcInfo{}
	var walk func(p ProcInfo)
	walk = func(p ProcInfo) {
		if prot[p.Pid] {
			return
		}
		if _, seen := set[p.Pid]; seen {
			return
		}
		set[p.Pid] = p
		for _, c := range children[p.Pid] {
			walk(c)
		}
	}
	for _, p := range all {
		if tagged(p) {
			walk(p)
		}
	}
	// Groups led by a member of the set, until nothing new is added.
	for {
		before := len(set)
		for _, p := range all {
			if _, in := set[p.Pgid]; in && !protGroups[p.Pgid] {
				walk(p)
			}
		}
		if len(set) == before {
			break
		}
	}
	out := make([]ProcInfo, 0, len(set))
	for _, p := range set {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pid < out[j].Pid })
	return out
}

// Reap SIGKILLs every process matched by tagged (see expandTagged) and
// returns what it killed.
func Reap(tagged func(ProcInfo) bool) ([]ProcInfo, error) {
	all, err := listProcesses()
	if err != nil {
		return nil, err
	}
	victims := expandTagged(all, tagged)
	for _, p := range victims {
		_ = syscall.Kill(p.Pid, syscall.SIGKILL)
	}
	if len(victims) > 0 {
		deadline := time.Now().Add(2 * time.Second)
		for _, p := range victims {
			for PidAlive(p.Pid) && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
		}
	}
	return victims, nil
}

// TagOwner parses the owning test-binary pid out of a sandbox tag.
func TagOwner(tag string) (int, bool) {
	if !strings.HasPrefix(tag, sandboxIDPrefix) {
		return 0, false
	}
	rest := tag[len(sandboxIDPrefix):]
	i := strings.IndexByte(rest, '-')
	if i <= 0 {
		return 0, false
	}
	pid, err := strconv.Atoi(rest[:i])
	if err != nil {
		return 0, false
	}
	return pid, true
}

// StaleTag reports whether tag belongs to a test binary that no longer runs.
func StaleTag(tag string) bool {
	owner, ok := TagOwner(tag)
	return ok && !PidAlive(owner)
}

// ReapStale kills processes left behind by e2e runs whose test binary has
// exited (crash, Ctrl-C, timeout). Live runs are never touched.
func ReapStale() ([]ProcInfo, error) {
	return Reap(func(p ProcInfo) bool { return StaleTag(p.SandboxID()) })
}

// ReapPrefix kills processes whose tag starts with prefix.
func ReapPrefix(prefix string) ([]ProcInfo, error) {
	if !strings.HasPrefix(prefix, sandboxIDPrefix) {
		return nil, fmt.Errorf("harness: refusing to reap prefix %q (must start with %q)", prefix, sandboxIDPrefix)
	}
	return Reap(func(p ProcInfo) bool { return strings.HasPrefix(p.SandboxID(), prefix) })
}
