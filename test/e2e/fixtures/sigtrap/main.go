// Command sigtrap is an e2e fixture that ignores SIGTERM/SIGINT/SIGHUP, to
// test stop escalation to SIGKILL. With -term-delay it exits that long after
// the first SIGTERM instead (a slow-stopping service).
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	termDelay := flag.Duration("term-delay", 0, "exit this long after SIGTERM (0 = ignore SIGTERM)")
	flag.Parse()
	sig := make(chan os.Signal, 8)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	fmt.Printf("sigtrap ready pid=%d\n", os.Getpid())
	for s := range sig {
		fmt.Printf("sigtrap got %v\n", s)
		if s == syscall.SIGTERM && *termDelay > 0 {
			go func() {
				time.Sleep(*termDelay)
				fmt.Println("sigtrap exiting after term-delay")
				os.Exit(0)
			}()
		}
	}
}
