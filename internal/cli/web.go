package cli

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/devyard/internal/globalconfig"
	"github.com/blesswinsamuel/devyard/internal/web"
)

func newUICmd(ctx *CLIContext) *cobra.Command {
	host := globalconfig.DefaultHost
	port := globalconfig.DefaultPort
	if cfg, err := globalconfig.Load(); err == nil {
		host = cfg.Web.Host
		port = cfg.Web.Port
	}

	var webHost string
	var webPort int

	cmd := &cobra.Command{
		Use:     "ui",
		Aliases: []string{"web"},
		Short:   "Start the browser dashboard",
		Long:    "Start the web dashboard server, connecting to a running daemon over its control socket.",
		RunE: func(cmd *cobra.Command, args []string) error {
			slog.SetDefault(slog.New(slog.NewTextHandler(ctx.Err, nil)))

			socket, err := dialDaemon()
			if err != nil {
				ctx.Errorln("devyard: no daemon running (is it up?)")
				return err
			}

			addr := fmt.Sprintf("%s:%d", webHost, webPort)
			srv := web.NewServer(addr, socket)
			if err := srv.ListenAndServe(); err != nil {
				return err
			}
			ctx.Errorf("devyard: dashboard at http://%s\n", srv.Addr())

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			<-sigCh

			return srv.Close()
		},
	}

	cmd.Flags().StringVar(&webHost, "host", host, "Web dashboard listen host")
	cmd.Flags().IntVar(&webPort, "port", port, "Web dashboard listen port")

	return cmd
}
