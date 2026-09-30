//go:build darwin

package harness

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// listProcesses uses ps(1): `ps -E` appends each process's initial
// environment to its command line. The environment of platform binaries
// (/bin/sh, /bin/sleep, ...) is hidden by the kernel; those processes are
// listed with EnvReadable=false and found through the process tree instead.
func listProcesses() ([]ProcInfo, error) {
	// ps -o comm prints the executable path; ps -E -o command prints
	// "argv... ENV=...". Two snapshots, joined by pid.
	commOut, err := exec.Command("/bin/ps", "-ax", "-ww", "-o", "pid=,ppid=,pgid=,stat=,comm=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps comm: %w", err)
	}
	envOut, err := exec.Command("/bin/ps", "-ax", "-E", "-ww", "-o", "pid=,command=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps -E: %w", err)
	}
	withEnv := map[int]string{}
	sc := bufio.NewScanner(bytes.NewReader(envOut))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		line := strings.TrimLeft(sc.Text(), " ")
		i := strings.IndexByte(line, ' ')
		if i < 0 {
			continue
		}
		pid, err := strconv.Atoi(line[:i])
		if err != nil {
			continue
		}
		withEnv[pid] = line[i+1:]
	}
	var out []ProcInfo
	sc = bufio.NewScanner(bytes.NewReader(commOut))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		pgid, err3 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		if strings.HasPrefix(f[3], "Z") {
			continue
		}
		p := ProcInfo{Pid: pid, PPid: ppid, Pgid: pgid, Exe: strings.Join(f[4:], " ")}
		if raw, ok := withEnv[pid]; ok {
			p.Command = raw
			// ps prints the environment after the arguments; it only does
			// so when the environment is readable.
			if strings.Contains(raw, " "+"PATH=") || strings.Contains(raw, " "+SandboxIDVar+"=") || strings.Contains(raw, " HOME=") {
				p.EnvReadable = true
				p.rawEnv = " " + raw
			}
		}
		out = append(out, p)
	}
	return out, nil
}
