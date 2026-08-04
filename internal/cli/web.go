package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/globalconfig"
	"github.com/blesswinsamuel/local-compose/internal/web"
)

var (
	webHost string
	webPort int
)

var webCmd = &cobra.Command{
	Use:   "web",
	Short: "Start the web UI",
	Long:  "Start the web UI server, connecting to a running daemon over its control socket.",
	RunE: func(cmd *cobra.Command, args []string) error {
		socket, err := dialDaemon()
		if err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: no daemon running (is it up?)\n")
			return err
		}

		addr := fmt.Sprintf("%s:%d", webHost, webPort)
		srv := web.NewProxyServer(addr, socket)
		if err := srv.ListenAndServe(); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "local-compose: web UI at http://%s\n", srv.Addr())

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		return srv.Close()
	},
}

func init() {
	host := globalconfig.DefaultHost
	port := globalconfig.DefaultPort
	if cfg, err := globalconfig.Load(); err == nil {
		host = cfg.Web.Host
		port = cfg.Web.Port
	}
	webCmd.Flags().StringVar(&webHost, "host", host, "Web UI listen host")
	webCmd.Flags().IntVar(&webPort, "port", port, "Web UI listen port")
}
