//go:build unix

package procstat_test

import (
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/procstat"
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
		if ok && s.Procs > 0 {
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
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill child: %v", err)
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
