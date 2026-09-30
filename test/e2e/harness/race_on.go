//go:build race

package harness

// RaceEnabled reports whether the test binary was built with -race. The
// devyard binary under test is then built with -race too, and deadlines are
// scaled up.
const RaceEnabled = true
