// Command ticker is an e2e fixture that prints numbered lines.
//
//	ticker [-interval 100ms] [-count N] [-stderr] [-line-size N] [-prefix P] [-exit-code N] [-busy]
//
// Each line is "<prefix> <seq>", padded with 'x' to -line-size bytes when
// set (to exercise lines larger than 64 KiB). -stderr also writes
// "<prefix> err <seq>" to stderr. With -count it exits after N lines with
// -exit-code. -busy spins a CPU core (for stats tests).
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	interval := flag.Duration("interval", 100*time.Millisecond, "delay between lines")
	count := flag.Int("count", 0, "exit after this many lines (0 = forever)")
	toStderr := flag.Bool("stderr", false, "also write to stderr")
	lineSize := flag.Int("line-size", 0, "pad each stdout line to this many bytes")
	prefix := flag.String("prefix", "", "line prefix (default $NAME or \"tick\")")
	exitCode := flag.Int("exit-code", 0, "exit code after -count lines")
	busy := flag.Bool("busy", false, "spin a CPU core")
	flag.Parse()
	if *prefix == "" {
		*prefix = os.Getenv("NAME")
	}
	if *prefix == "" {
		*prefix = "tick"
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	out := bufio.NewWriterSize(os.Stdout, 1<<20)
	go func() {
		s := <-sig
		_ = out.Flush()
		fmt.Printf("%s got %v\n", *prefix, s)
		os.Exit(0)
	}()
	if *busy {
		go func() {
			x := 0
			for {
				x++
			}
		}()
	}
	fmt.Printf("%s start pid=%d pgid=%d\n", *prefix, os.Getpid(), syscall.Getpgrp())
	for i := 1; *count == 0 || i <= *count; i++ {
		line := fmt.Sprintf("%s %d", *prefix, i)
		if *lineSize > len(line) {
			line += " " + strings.Repeat("x", *lineSize-len(line)-1)
		}
		_, _ = out.WriteString(line + "\n")
		_ = out.Flush()
		if *toStderr {
			fmt.Fprintf(os.Stderr, "%s err %d\n", *prefix, i)
		}
		if *interval > 0 {
			time.Sleep(*interval)
		}
	}
	fmt.Printf("%s done\n", *prefix)
	os.Exit(*exitCode)
}
