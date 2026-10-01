// Command devyard orchestrates local processes described by devyard.yml.
package main

import (
	"os"

	"github.com/blesswinsamuel/devyard/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
