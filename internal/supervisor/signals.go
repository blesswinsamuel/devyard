package supervisor

import (
	"fmt"
	"strconv"
	"strings"
	"syscall"
)

// signalNames maps accepted signal names (without the "SIG" prefix) to their
// syscall values. Both "SIGTERM" and "TERM" are accepted.
var signalNames = map[string]syscall.Signal{
	"ABRT":  syscall.SIGABRT,
	"ALRM":  syscall.SIGALRM,
	"BUS":   syscall.SIGBUS,
	"CHLD":  syscall.SIGCHLD,
	"CONT":  syscall.SIGCONT,
	"FPE":   syscall.SIGFPE,
	"HUP":   syscall.SIGHUP,
	"ILL":   syscall.SIGILL,
	"INT":   syscall.SIGINT,
	"KILL":  syscall.SIGKILL,
	"PIPE":  syscall.SIGPIPE,
	"QUIT":  syscall.SIGQUIT,
	"SEGV":  syscall.SIGSEGV,
	"STOP":  syscall.SIGSTOP,
	"TERM":  syscall.SIGTERM,
	"TRAP":  syscall.SIGTRAP,
	"TSTP":  syscall.SIGTSTP,
	"TTIN":  syscall.SIGTTIN,
	"TTOU":  syscall.SIGTTOU,
	"URG":   syscall.SIGURG,
	"USR1":  syscall.SIGUSR1,
	"USR2":  syscall.SIGUSR2,
	"WINCH": syscall.SIGWINCH,
}

// parseSignal converts a signal name ("SIGTERM", "TERM") or numeric value
// ("15") into a syscall.Signal. The empty string means SIGKILL, the kill
// default.
func parseSignal(name string) (syscall.Signal, error) {
	if name == "" {
		return syscall.SIGKILL, nil
	}
	upper := strings.ToUpper(name)
	upper = strings.TrimPrefix(upper, "SIG")
	if sig, ok := signalNames[upper]; ok {
		return sig, nil
	}
	if n, err := strconv.Atoi(upper); err == nil && n >= 1 && n <= 31 {
		return syscall.Signal(n), nil
	}
	return 0, fmt.Errorf("supervisor: unknown signal %q (expected e.g. SIGTERM, SIGKILL, or a number)", name)
}
