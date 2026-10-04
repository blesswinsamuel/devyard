package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"

	"connectrpc.com/connect"

	"github.com/blesswinsamuel/devyard/internal/client"
	pb "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/internal/globalconfig"
)

// newAuthCmd manages the dashboard password (web.password_hash in the
// global config). A running daemon applies changes immediately; without
// one, the file is written and the next daemon start picks it up.
func newAuthCmd(c *Context) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage the web dashboard password",
	}
	cmd.AddCommand(newSetPasswordCmd(c), newAuthClearCmd(c))
	return cmd
}

func newSetPasswordCmd(c *Context) *cobra.Command {
	var fromStdin bool
	cmd := &cobra.Command{
		Use:   "set-password",
		Short: "Set the web dashboard password (bcrypt-hashed in the global config)",
		Long: "Prompts for the password and stores its bcrypt hash as web.password_hash\n" +
			"in the global config. The API, terminals and logs then require a login\n" +
			"before they load. A running daemon applies it immediately; otherwise it\n" +
			"applies when the daemon starts.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			pw, err := readNewPassword(c, fromStdin)
			if err != nil {
				return err
			}
			if cl, ok := c.tryDial(ctx); ok {
				defer cl.Close()
				if _, err := cl.SetWebPassword(ctx, connect.NewRequest(&pb.SetWebPasswordRequest{Password: pw})); err != nil {
					return err
				}
				c.Errorf("devyard: dashboard password set (applied immediately; everyone must log in again)\n")
				return nil
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
			if err != nil {
				return fmt.Errorf("auth: hash password: %w", err)
			}
			if err := saveGlobalConfig(func(cfg *globalconfig.Config) { cfg.Web.PasswordHash = string(hash) }); err != nil {
				return err
			}
			c.Errorf("devyard: dashboard password set (applies when the daemon starts)\n")
			return nil
		},
	}
	cmd.Flags().BoolVar(&fromStdin, "stdin", false, "Read the password from stdin (one line) instead of a prompt")
	return cmd
}

func newAuthClearCmd(c *Context) *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Remove the web dashboard password",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if cl, ok := c.tryDial(ctx); ok {
				defer cl.Close()
				if _, err := cl.SetWebPassword(ctx, connect.NewRequest(&pb.SetWebPasswordRequest{Clear: true})); err != nil {
					return err
				}
				c.Errorf("devyard: dashboard password cleared (the dashboard is open again)\n")
				return nil
			}
			if err := saveGlobalConfig(func(cfg *globalconfig.Config) { cfg.Web.PasswordHash = "" }); err != nil {
				return err
			}
			c.Errorf("devyard: dashboard password cleared (applies when the daemon starts)\n")
			return nil
		},
	}
}

// tryDial connects to a running daemon, reporting whether one answered.
func (c *Context) tryDial(ctx context.Context) (*client.Client, bool) {
	d, err := dirs()
	if err != nil {
		return nil, false
	}
	cl := client.Dial(d.Socket())
	if _, err := cl.Ping(ctx, time.Second); err != nil {
		cl.Close()
		return nil, false
	}
	return cl, true
}

// saveGlobalConfig loads the global config, applies mutate and saves it.
func saveGlobalConfig(mutate func(*globalconfig.Config)) error {
	d, err := dirs()
	if err != nil {
		return err
	}
	path := d.GlobalConfig()
	cfg, err := globalconfig.Load(path, os.Stderr)
	if err != nil {
		return err
	}
	mutate(cfg)
	return globalconfig.Save(path, cfg)
}

// readNewPassword reads the password once from stdin (--stdin) or twice
// from a terminal prompt.
func readNewPassword(c *Context, fromStdin bool) (string, error) {
	if fromStdin {
		line, err := bufio.NewReader(c.In).ReadString('\n')
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("auth: read stdin: %w", err)
		}
		return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("auth: stdin is not a terminal (use --stdin to read the password from stdin)")
	}
	c.Errorf("New dashboard password: ")
	pw, err := term.ReadPassword(fd)
	c.Errorf("\n")
	if err != nil {
		return "", fmt.Errorf("auth: read password: %w", err)
	}
	if len(pw) == 0 {
		return "", errors.New("auth: password must not be empty")
	}
	c.Errorf("Confirm dashboard password: ")
	confirm, err := term.ReadPassword(fd)
	c.Errorf("\n")
	if err != nil {
		return "", fmt.Errorf("auth: read password: %w", err)
	}
	if string(pw) != string(confirm) {
		return "", errors.New("auth: passwords do not match")
	}
	return string(pw), nil
}
