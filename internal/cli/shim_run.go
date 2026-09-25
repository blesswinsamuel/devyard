//go:build unix

package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/blesswinsamuel/devyard/internal/shim"
)

// isShimChild reports whether the binary was invoked with the hidden --shim flag.
func isShimChild(args []string) bool {
	for i, a := range args {
		if a == shim.Flag && i+1 < len(args) {
			return true
		}
	}
	return false
}

// runShimChild runs the shim child process reading its config from the file path passed as argument.
func runShimChild(args []string) error {
	var cfgPath string
	for i, a := range args {
		if a == shim.Flag && i+1 < len(args) {
			cfgPath = args[i+1]
			break
		}
	}
	if cfgPath == "" {
		return fmt.Errorf("shim: missing config path argument")
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return fmt.Errorf("shim: read config %s: %w", cfgPath, err)
	}

	var cfg shim.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("shim: unmarshal config: %w", err)
	}

	return shim.Run(&cfg)
}
