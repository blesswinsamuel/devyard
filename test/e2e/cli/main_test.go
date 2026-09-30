// Package cli_test drives the devyard CLI end to end: the commands
// themselves are under test here, so assertions use CLI output where that
// output is the contract and typed API state everywhere else.
package cli_test

import (
	"testing"

	"github.com/blesswinsamuel/devyard/test/e2e/harness"
)

func TestMain(m *testing.M) { harness.Main(m) }
