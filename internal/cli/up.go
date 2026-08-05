package cli

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
)

var upDetach bool
var upBuild bool

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Start services defined in local-compose.yml",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig(flagConfigPath, flagProject)
		if err != nil {
			return err
		}

		if upBuild {
			if err := runAllBuilds(cfg); err != nil {
				return err
			}
		}

		socket, err := ensureDaemon()
		if err != nil {
			return err
		}

		client, err := control.Dial(socket)
		if err != nil {
			return err
		}
		if err := client.StartProject(cfg.ConfigPath, false, cfg.EnvFile); err != nil {
			_ = client.Close()
			return err
		}
		_ = client.Close()

		fmt.Fprintf(os.Stderr, "local-compose: project %q started\n", cfg.Project)

		if upDetach {
			return nil
		}

		return followForeground(socket, cfg.Project, cfg.Order)
	},
}

// followForeground streams logs from all services in the project to stdout with
// colored prefixes and blocks until all services have exited or been stopped.
// Ctrl+C sends stop_project to the daemon and waits for it to complete.
func followForeground(socket, project string, order []string) error {
	stop := make(chan struct{})
	stopOnce := sync.Once{}

	// Track follow clients so we can close them when services are done.
	var followMu sync.Mutex
	var followClients []*control.Client

	closeAllFollows := func() {
		followMu.Lock()
		for _, c := range followClients {
			_ = c.Close()
		}
		followClients = nil
		followMu.Unlock()
	}

	// Signal handler: Ctrl+C → stop_project → exit.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		select {
		case <-sigCh:
			stopOnce.Do(func() { close(stop) })
			closeAllFollows()
			c, err := control.Dial(socket)
			if err != nil {
				return
			}
			defer func() { _ = c.Close() }()
			_ = c.StopProject(project)
		case <-stop:
		}
	}()

	// Follow logs for each service in its own goroutine.
	var wg sync.WaitGroup
	for _, svc := range order {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			c, err := control.Dial(socket)
			if err != nil {
				return
			}
			followMu.Lock()
			followClients = append(followClients, c)
			followMu.Unlock()
			_ = c.Logs(project, name, true, false, protocol.DefaultLogTail, newLogPrinter(name))
		}(svc)
	}

	// Poll list until all services are exited or stopped.
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			closeAllFollows()
			wg.Wait()
			return nil
		case <-ticker.C:
			c, err := control.Dial(socket)
			if err != nil {
				continue
			}
			states, err := c.List(project)
			_ = c.Close()
			if err != nil {
				continue
			}
			allDone := true
			for _, s := range states {
				if s.Status != "exited" && s.Status != "stopped" {
					allDone = false
					break
				}
			}
			if allDone {
				stopOnce.Do(func() { close(stop) })
				closeAllFollows()
			}
		}
	}
}

func init() {
	upCmd.Flags().BoolVarP(&upDetach, "detach", "d", false, "Run in the background (don't follow logs)")
	upCmd.Flags().BoolVar(&upBuild, "build", false, "Build services before starting")
}
