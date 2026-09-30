package harness

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"
)

// TrackPid registers a pid (and its process group) with the leak checker.
func (sb *Sandbox) TrackPid(pid int, desc string) {
	if pid <= 1 {
		return
	}
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if _, ok := sb.tracked[pid]; !ok {
		sb.tracked[pid] = desc
	}
}

// recordState tracks every service/task pid in s.
func (sb *Sandbox) recordState(s State) {
	for _, sv := range s.Services {
		if pid := int(sv.GetPid()); pid > 1 {
			sb.TrackPid(pid, fmt.Sprintf("service %s/%s run %d", sv.GetProject(), sv.GetName(), sv.GetRun()))
		}
	}
	for _, tk := range s.Tasks {
		if pid := int(tk.GetPid()); pid > 1 {
			sb.TrackPid(pid, fmt.Sprintf("task %s/%s run %d", tk.GetProject(), tk.GetName(), tk.GetRun()))
		}
	}
}

// Tracked returns the pids the leak checker knows about.
func (sb *Sandbox) Tracked() map[int]string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	out := make(map[int]string, len(sb.tracked))
	for k, v := range sb.tracked {
		out[k] = v
	}
	return out
}

// TaggedProcesses lists live processes carrying this sandbox's tag.
func (sb *Sandbox) TaggedProcesses() []ProcInfo {
	procs, err := ProcessesWithTag(sb.ID)
	if err != nil {
		sb.t.Logf("list processes: %v", err)
	}
	return procs
}

// LiveGroups returns the distinct process groups of live, tagged processes
// whose environment has marker=value and that aren't devyard itself (the
// runner) — i.e. the live instances of one service/task. Services under
// test set a unique marker in their `env:`.
func (sb *Sandbox) LiveGroups(marker, value string) []int {
	bin, _ := DevyardPath()
	groups := map[int]bool{}
	for _, p := range sb.TaggedProcesses() {
		if v, ok := p.EnvValue(marker); !ok || v != value {
			continue
		}
		if bin != "" && (p.Exe == bin || strings.HasSuffix(p.Exe, "/devyard")) {
			continue
		}
		if !PidAlive(p.Pid) {
			continue
		}
		groups[p.Pgid] = true
	}
	out := make([]int, 0, len(groups))
	for g := range groups {
		out = append(out, g)
	}
	sort.Ints(out)
	return out
}

// teardown stops the daemon, checks for leaked processes, sweeps tagged
// leftovers and removes the sandbox dirs.
func (sb *Sandbox) teardown() {
	t := sb.t
	sb.mu.Lock()
	sb.closing = true
	rec := sb.recorder
	sb.mu.Unlock()

	if sb.env != nil {
		if _, err := DevyardPath(); err == nil {
			if t.Failed() {
				t.Logf("sandbox %s diagnostics:\n%s", sb.ID, sb.Diagnostics())
			}
			sb.stopDaemonForTeardown()
		}
	}
	sb.cancel()
	if rec != nil {
		<-rec.done
	}
	sb.mu.Lock()
	if sb.closeCli != nil {
		sb.closeCli()
	}
	sb.mu.Unlock()

	if sb.env != nil {
		sb.checkLeaks()
	}

	sb.mu.Lock()
	keep := sb.noCleanup
	sb.mu.Unlock()
	if os.Getenv("DEVYARD_E2E_KEEP") != "" || keep {
		t.Logf("sandbox kept: root=%s runtime=%s", sb.Root, sb.Runtime)
		return
	}
	for _, dir := range []string{sb.Root, sb.Runtime} {
		if dir == "" {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			// Files may be read-only (e.g. go module caches); make writable and retry.
			_ = chmodTree(dir)
			if err := os.RemoveAll(dir); err != nil {
				t.Logf("remove %s: %v", dir, err)
			}
		}
	}
}

func (sb *Sandbox) stopDaemonForTeardown() {
	t := sb.t
	info := sb.DaemonInfo()
	if info == nil && len(sb.TaggedProcesses()) > 0 {
		// The test killed the daemon and left runners behind: start a
		// daemon so it adopts and stops them the normal way.
		r := sb.CLIWith(RunOpts{Timeout: 30 * time.Second}, "daemon", "start")
		if r.Code != 0 {
			t.Logf("teardown: daemon start (to adopt leftovers) failed:\n%s", r)
		}
		info = sb.DaemonInfo()
	}
	if info == nil {
		return
	}
	sb.recordFinalState()
	pid := int(info.GetPid())
	r := sb.CLIWith(RunOpts{Timeout: 45 * time.Second}, "daemon", "stop")
	if r.Code != 0 {
		t.Errorf("teardown: `devyard daemon stop` failed:\n%s", r)
	}
	deadline := time.Now().Add(Scale(15 * time.Second))
	for PidAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if PidAlive(pid) {
		t.Errorf("teardown: daemon pid %d still alive after daemon stop; SIGKILL", pid)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func (sb *Sandbox) recordFinalState() {
	ctxState := sb.fetchStateQuiet()
	if ctxState != nil {
		sb.recordState(*ctxState)
	}
}

func (sb *Sandbox) checkLeaks() {
	t := sb.t
	tracked := sb.Tracked()
	deadline := time.Now().Add(Scale(5 * time.Second))
	var survivors []int
	for pid := range tracked {
		for (GroupAlive(pid) || PidAlive(pid)) && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if GroupAlive(pid) || PidAlive(pid) {
			survivors = append(survivors, pid)
		}
	}
	sort.Ints(survivors)
	if len(survivors) > 0 {
		var b strings.Builder
		for _, pid := range survivors {
			fmt.Fprintf(&b, "  pgid %d (%s)\n", pid, tracked[pid])
		}
		for _, pid := range survivors {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		t.Errorf("LEAK: %d process group(s) reported by the API survived daemon stop (killed now):\n%s", len(survivors), b.String())
	}
	victims, err := Reap(func(p ProcInfo) bool { return p.SandboxID() == sb.ID })
	if err != nil {
		t.Logf("sweep: %v", err)
	}
	if len(victims) > 0 {
		var b strings.Builder
		for _, p := range victims {
			fmt.Fprintf(&b, "  %s\n", p)
		}
		t.Errorf("LEAK: %d sandbox-tagged process(es) still running after teardown (killed now):\n%s", len(victims), b.String())
	}
}

func chmodTree(dir string) error {
	return walkDirs(dir, func(path string) { _ = os.Chmod(path, 0o755) })
}

func walkDirs(dir string, fn func(string)) error {
	fn(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			_ = walkDirs(dir+string(os.PathSeparator)+e.Name(), fn)
		}
	}
	return nil
}
