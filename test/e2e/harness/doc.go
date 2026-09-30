// Package harness is the hermetic end-to-end test harness for devyard.
//
// Every test gets a Sandbox: private HOME/TMPDIR/XDG_* directories, a short
// XDG_RUNTIME_DIR under /tmp (unix socket path limits), a global config that
// binds the web UI and proxy to ephemeral ports, and a child environment
// built from an allowlist rather than os.Environ. Every process started
// through the sandbox carries DEVYARD_SANDBOX_ID, which the leak checker and
// the sweeper use to find (and only ever kill) processes this run created.
//
// Suites call Main from TestMain; it builds the devyard binary and the
// fixture programs once per test binary.
package harness
