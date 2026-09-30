// Command exiter is an e2e fixture that exits with -code after -after.
// With -count-file it appends one line per launch, so tests can count
// (re)starts.
package main

import (
	"flag"
	"fmt"
	"os"
	"syscall"
	"time"
)

func main() {
	code := flag.Int("code", 0, "exit code")
	after := flag.Duration("after", 0, "delay before exiting")
	countFile := flag.String("count-file", "", "append a line per launch")
	untilFile := flag.String("until-file", "", "exit once this file exists (instead of after -after)")
	flag.Parse()
	if *countFile != "" {
		f, err := os.OpenFile(*countFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			_ = f.Close()
		}
	}
	fmt.Printf("exiter start pid=%d pgid=%d code=%d after=%s\n", os.Getpid(), syscall.Getpgrp(), *code, *after)
	if *untilFile != "" {
		for {
			if _, err := os.Stat(*untilFile); err == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	} else {
		time.Sleep(*after)
	}
	fmt.Printf("exiter exiting code=%d\n", *code)
	os.Exit(*code)
}
