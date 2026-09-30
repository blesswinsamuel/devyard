//go:build linux

package harness

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func listProcesses() ([]ProcInfo, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var out []ProcInfo
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		dir := filepath.Join("/proc", e.Name())
		stat, err := os.ReadFile(filepath.Join(dir, "stat"))
		if err != nil {
			continue
		}
		// pid (comm) state ppid pgrp ...; comm may contain spaces/parens.
		i := bytes.LastIndexByte(stat, ')')
		if i < 0 {
			continue
		}
		fields := strings.Fields(string(stat[i+1:]))
		if len(fields) < 3 {
			continue
		}
		if fields[0] == "Z" {
			continue
		}
		ppid, _ := strconv.Atoi(fields[1])
		pgid, _ := strconv.Atoi(fields[2])
		p := ProcInfo{Pid: pid, PPid: ppid, Pgid: pgid}
		if cmdline, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
			args := strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
			p.Command = strings.Join(args, " ")
			if len(args) > 0 {
				p.Exe = args[0]
			}
		}
		if exe, err := os.Readlink(filepath.Join(dir, "exe")); err == nil {
			p.Exe = exe
		}
		if env, err := os.ReadFile(filepath.Join(dir, "environ")); err == nil {
			p.EnvReadable = true
			p.env = strings.Split(strings.TrimRight(string(env), "\x00"), "\x00")
			if p.env == nil {
				p.env = []string{}
			}
		}
		out = append(out, p)
	}
	return out, nil
}
