//go:build unix

package procstat_test

import (
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/blesswinsamuel/devyard/internal/procstat"
)

// startGrouped spawns `sh -c 'sleep 30'` in its own process group (matching how
// the supervisor launches services) and returns its process group id.
func startGrouped(t *testing.T) (int, *exec.Cmd) {
	t.Helper()
	cmd := exec.Command("sh", "-c", "sleep 30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	return cmd.Process.Pid, cmd
}

func TestSampleGroupLiveProcess(t *testing.T) {
	pgid, cmd := startGrouped(t)
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	// The child may take a moment to appear in the process table.
	var s procstat.Sample
	var ok bool
	deadline := time.Now().Add(2 * time.Second)
	for {
		s, ok = procstat.SampleGroup(pgid)
		// Right after fork (before exec) the child can briefly report no
		// resident pages.
		if ok && s.Procs > 0 && s.RSS > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("group %d never appeared with processes", pgid)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if s.Procs < 1 {
		t.Fatalf("procs = %d, want >= 1", s.Procs)
	}
	if s.RSS == 0 {
		t.Errorf("rss = 0, want non-zero resident set")
	}
	// A sleeping process burns almost no CPU; allow generous slack.
	if cpu := s.CPU; cpu > time.Second {
		t.Errorf("cumulative CPU = %v, implausibly large for a sleep", cpu)
	}
}

func TestSampleGroupUnknownGroup(t *testing.T) {
	pgid, cmd := startGrouped(t)
	// Kill the whole group: a shell leader may have forked its command.
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill group: %v", err)
	}
	if _, err := cmd.Process.Wait(); err != nil {
		t.Fatalf("wait child: %v", err)
	}

	var ok bool
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, ok = procstat.SampleGroup(pgid)
		if !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("group %d still reported alive after child exited", pgid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSampleGroupZeroOrNegative(t *testing.T) {
	if _, ok := procstat.SampleGroup(0); ok {
		t.Errorf("SampleGroup(0) reported ok")
	}
	if _, ok := procstat.SampleGroup(-1); ok {
		t.Errorf("SampleGroup(-1) reported ok")
	}
}

func TestInspectProcessLive(t *testing.T) {
	pid, cmd := startGrouped(t)
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	// The child may take a moment to appear in the process table.
	deadline := time.Now().Add(2 * time.Second)
	for {
		info, ok := procstat.InspectProcess(pid)
		if ok {
			if info.Zombie {
				t.Fatalf("leader %d reported zombie while alive", pid)
			}
			if info.PGID != pid {
				t.Fatalf("pgid = %d, want %d (Setpgid makes pgid == pid)", info.PGID, pid)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %d never became inspectable", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestInspectProcessDead(t *testing.T) {
	pid, cmd := startGrouped(t)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill child: %v", err)
	}
	if _, err := cmd.Process.Wait(); err != nil {
		t.Fatalf("wait child: %v", err)
	}
	if _, ok := procstat.InspectProcess(pid); ok {
		t.Errorf("InspectProcess(reaped pid %d) reported ok", pid)
	}
}

func TestInspectProcessZeroOrNegative(t *testing.T) {
	if _, ok := procstat.InspectProcess(0); ok {
		t.Errorf("InspectProcess(0) reported ok")
	}
	if _, ok := procstat.InspectProcess(-1); ok {
		t.Errorf("InspectProcess(-1) reported ok")
	}
}

func TestSampleGroupCPUIsWallClockScale(t *testing.T) {
	// Burn ~300ms of CPU in this process and check the sampled CPU time
	// grows by a comparable amount (catches unit bugs like Mach ticks being
	// treated as nanoseconds).
	pgid := syscall.Getpgrp()
	before, ok := procstat.SampleGroup(pgid)
	if !ok {
		t.Skip("cannot sample own group")
	}
	start := time.Now()
	x := 0
	for time.Since(start) < 300*time.Millisecond {
		x++
	}
	_ = x
	after, _ := procstat.SampleGroup(pgid)
	delta := after.CPU - before.CPU
	if delta < 100*time.Millisecond || delta > 5*time.Second {
		t.Fatalf("cpu delta %v for ~300ms busy loop", delta)
	}
}
