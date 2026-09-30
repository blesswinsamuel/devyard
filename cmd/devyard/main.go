// Command devyard orchestrates local processes with a compose-style config.
package main

import (
	"os"

	"github.com/blesswinsamuel/devyard/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
