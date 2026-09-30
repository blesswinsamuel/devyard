// Command reap kills processes left behind by devyard e2e runs.
//
//	go run ./test/e2e/cmd/reap              # runs whose test binary has exited
//	go run ./test/e2e/cmd/reap -prefix ID   # tags starting with ID (e.g. one live run)
//	go run ./test/e2e/cmd/reap -n           # dry run: list, don't kill
//
// Only processes whose environment carries a DEVYARD_SANDBOX_ID tag written
// by the harness (plus their descendants and process groups, because macOS
// hides the environment of /bin/sh and friends) are touched.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/blesswinsamuel/devyard/test/e2e/harness"
)

func main() {
	prefix := flag.String("prefix", "", "reap tags with this prefix instead of stale runs")
	dry := flag.Bool("n", false, "dry run: list matching processes only")
	flag.Parse()

	match := func(p harness.ProcInfo) bool { return harness.StaleTag(p.SandboxID()) }
	if *prefix != "" {
		if !strings.HasPrefix(*prefix, "dye2e-") {
			fmt.Fprintln(os.Stderr, "reap: -prefix must start with dye2e-")
			os.Exit(2)
		}
		match = func(p harness.ProcInfo) bool { return strings.HasPrefix(p.SandboxID(), *prefix) }
	}
	if *dry {
		procs, err := harness.ListProcesses()
		if err != nil {
			fmt.Fprintln(os.Stderr, "reap:", err)
			os.Exit(1)
		}
		n := 0
		for _, p := range procs {
			if match(p) {
				fmt.Println(p)
				n++
			}
		}
		fmt.Fprintf(os.Stderr, "reap: %d tagged processes match (descendants not listed)\n", n)
		return
	}
	victims, err := harness.Reap(match)
	if err != nil {
		fmt.Fprintln(os.Stderr, "reap:", err)
		os.Exit(1)
	}
	for _, p := range victims {
		fmt.Println("killed", p)
	}
	fmt.Fprintf(os.Stderr, "reap: killed %d processes\n", len(victims))
}
