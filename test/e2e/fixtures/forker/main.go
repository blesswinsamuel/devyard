// Command forker is an e2e fixture reproducing bug S10: it spawns a
// grandchild that escapes the service's process group and session (setsid)
// while keeping stdout/stderr open, so a supervisor that waits for EOF on
// the output pipe would wedge.
//
//	forker [-exit-after D] [-hold D] [-pidfile path]
//
// The grandchild sleeps for -hold (default 10m) and writes its pid to
// -pidfile so the test can kill it. The parent exits after -exit-after, or
// waits for a signal when it is 0.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	exitAfter := flag.Duration("exit-after", 0, "parent exits after this long (0 = run until signalled)")
	hold := flag.Duration("hold", 10*time.Minute, "how long the grandchild keeps stdout open")
	pidfile := flag.String("pidfile", "", "write the grandchild pid here")
	grandchild := flag.Bool("grandchild", false, "internal")
	flag.Parse()
	if *grandchild {
		fmt.Printf("forker grandchild pid=%d holding stdout\n", os.Getpid())
		time.Sleep(*hold)
		return
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cmd := exec.Command(self, "-grandchild", "-hold", hold.String())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *pidfile != "" {
		_ = os.WriteFile(*pidfile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)
	}
	fmt.Printf("forker ready pid=%d grandchild=%d\n", os.Getpid(), cmd.Process.Pid)
	_ = cmd.Process.Release()
	if *exitAfter > 0 {
		time.Sleep(*exitAfter)
		fmt.Println("forker parent exiting")
		return
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	s := <-sig
	fmt.Printf("forker parent got %v\n", s)
}
