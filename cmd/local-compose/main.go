package main

import (
	"os"

	"github.com/blesswinsamuel/local-compose/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
