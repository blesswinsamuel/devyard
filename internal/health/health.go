// Package health runs per-service healthchecks and tracks their state.
//
// A Checker owns the state machine for one service:
//
//	starting -> healthy | unhealthy
//
// It runs the configured probe (CMD or CMD-SHELL) on an interval, up to
// Retries consecutive failures before flipping to unhealthy. A subsequent
// success recovers the state to healthy and resets the failure counter. The
// probe runs in its own process group so a CMD-SHELL pipeline can be killed
// wholesale after Timeout.
//
// Checkers are frontend-agnostic: the supervisor owns them and exposes their
// State via the control protocol's ServiceState.Health field, so the CLI and
// web UI both read the same value.
package health

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// State is the health state of one service.
type State string

const (
	// StateStarting means the checker has not yet observed a successful probe.
	StateStarting State = "starting"
	// StateHealthy means the most recent probe succeeded.
	StateHealthy State = "healthy"
	// StateUnhealthy means Retries consecutive probes have failed.
	StateUnhealthy State = "unhealthy"
)

// Config is the resolved healthcheck configuration for one service. Defaults
// are applied by New if zero.
type Config struct {
	// Test is the healthcheck test spec. Test[0] must be "CMD" or "CMD-SHELL".
	//   - CMD:        Test[1:] is exec'd directly (no shell).
	//   - CMD-SHELL:  Test[1] is run via Shell -c.
	Test []string
	// Interval is the time between probes.
	Interval time.Duration
	// Retries is the number of consecutive failures required to mark the
	// service unhealthy.
	Retries int
	// Timeout is how long a single probe may run before it is killed.
	Timeout time.Duration
	// Shell is the shell used for CMD-SHELL probes (default "sh").
	Shell string
	// WorkingDir is the probe process's working directory (optional).
	WorkingDir string
	// Env is the probe process's environment (optional; nil inherits parent).
	Env []string
}

// Checker runs one service's healthcheck on an interval until Stop.
type Checker struct {
	name string
	cfg  Config
	log  func(string)

	mu            sync.Mutex
	state         State
	consecutive   int
	onStateChange func(State)

	started  atomic.Bool
	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// New validates cfg and returns a Checker in StateStarting. It does not start
// probing; call EnsureStarted.
func New(name string, cfg Config, log func(string)) (*Checker, error) {
	if len(cfg.Test) == 0 {
		return nil, errors.New("health: test is required")
	}
	switch cfg.Test[0] {
	case "CMD", "CMD-SHELL":
	default:
		return nil, fmt.Errorf("health: test must start with CMD or CMD-SHELL, got %q", cfg.Test[0])
	}
	if len(cfg.Test) < 2 {
		return nil, fmt.Errorf("health: %s requires a command argument", cfg.Test[0])
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 2 * time.Second
	}
	if cfg.Retries <= 0 {
		cfg.Retries = 3
	}
	if cfg.Shell == "" {
		cfg.Shell = "sh"
	}
	if log == nil {
		log = func(string) {}
	}
	return &Checker{
		name:  name,
		cfg:   cfg,
		log:   log,
		state: StateStarting,
		stop:  make(chan struct{}),
	}, nil
}

// Name returns the service name the checker is probing.
func (c *Checker) Name() string { return c.name }

// State returns the current health state. Safe for concurrent use.
func (c *Checker) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// SetOnStateChange registers a callback invoked whenever the health state
// transitions. Safe for concurrent use.
func (c *Checker) SetOnStateChange(fn func(State)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onStateChange = fn
}

// EnsureStarted begins probing on an interval. The first call launches the
// probe goroutine; subsequent calls are no-ops. It is safe to call after Stop
// returns (the call is a no-op once Stop has run).
func (c *Checker) EnsureStarted(ctx context.Context) {
	if !c.started.CompareAndSwap(false, true) {
		return
	}
	c.wg.Add(1)
	go c.loop(ctx)
}

// Stop signals the probe goroutine to exit and blocks until it has. Safe to
// call multiple times.
func (c *Checker) Stop() {
	c.stopOnce.Do(func() { close(c.stop) })
	c.wg.Wait()
}

// loop runs probes on the interval until ctx is cancelled, Stop is called, or
// the stop channel is closed. The first probe runs immediately so a healthy
// service reports healthy without waiting a full interval.
func (c *Checker) loop(ctx context.Context) {
	defer c.wg.Done()
	c.probeOnce(ctx)

	t := time.NewTicker(c.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stop:
			return
		case <-t.C:
			c.probeOnce(ctx)
		}
	}
}

// probeOnce runs a single probe and updates state. A success resets the
// consecutive-failure counter and marks the service healthy. A failure
// increments the counter and, once it reaches Retries, marks the service
// unhealthy. State transitions are logged so the supervisor's per-service log
// file records why health flipped, and onStateChange is invoked on transitions.
func (c *Checker) probeOnce(ctx context.Context) {
	err := c.runProbe(ctx)
	c.mu.Lock()
	prevState := c.state
	var logMsg string
	if err == nil {
		recovered := c.state == StateUnhealthy
		c.consecutive = 0
		c.state = StateHealthy
		if recovered {
			logMsg = "local-compose: healthcheck recovered: healthy"
		}
	} else {
		c.consecutive++
		failed := c.consecutive >= c.cfg.Retries
		wasUnhealthy := c.state == StateUnhealthy
		if failed {
			c.state = StateUnhealthy
			if !wasUnhealthy {
				logMsg = fmt.Sprintf("local-compose: healthcheck unhealthy after %d consecutive failures: %v", c.consecutive, err)
			}
		}
	}
	newState := c.state
	fn := c.onStateChange
	c.mu.Unlock()

	if logMsg != "" {
		c.log(logMsg)
	}
	if newState != prevState && fn != nil {
		fn(newState)
	}
}

// runProbe spawns the probe command in its own process group and waits for it.
// If the per-probe timeout elapses, the whole group is killed so a stuck
// CMD-SHELL pipeline cannot leak children.
func (c *Checker) runProbe(ctx context.Context) error {
	probeCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	var cmd *exec.Cmd
	if c.cfg.Test[0] == "CMD-SHELL" {
		cmd = exec.CommandContext(probeCtx, c.cfg.Shell, "-c", c.cfg.Test[1])
	} else {
		cmd = exec.CommandContext(probeCtx, c.cfg.Test[1], c.cfg.Test[2:]...)
	}
	if c.cfg.WorkingDir != "" {
		cmd.Dir = c.cfg.WorkingDir
	}
	if c.cfg.Env != nil {
		cmd.Env = c.cfg.Env
	}
	if err := applyProcessGroup(cmd); err != nil {
		return fmt.Errorf("set process group: %w", err)
	}
	// Take over the context's cancellation so we kill the whole group, not
	// just the lead process (a CMD-SHELL pipeline would otherwise leak).
	cmd.Cancel = func() error {
		_ = killProcessGroup(cmd)
		return os.ErrProcessDone
	}
	cmd.WaitDelay = c.cfg.Timeout
	return cmd.Run()
}
