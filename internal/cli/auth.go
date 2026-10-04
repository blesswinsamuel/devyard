package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"

	"github.com/blesswinsamuel/devyard/internal/globalconfig"
)

// newAuthCmd manages the dashboard password (web.password_hash in the
// global config). The daemon applies it at startup.
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
			"before they load; restart the daemon to apply it.",
		RunE: func(cmd *cobra.Command, args []string) error {
			pw, err := readNewPassword(c, fromStdin)
			if err != nil {
				return err
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
			if err != nil {
				return fmt.Errorf("auth: hash password: %w", err)
			}
			if err := saveGlobalConfig(func(cfg *globalconfig.Config) { cfg.Web.PasswordHash = string(hash) }); err != nil {
				return err
			}
			c.Errorf("devyard: dashboard password set (restart to apply: `devyard daemon restart`)\n")
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
			if err := saveGlobalConfig(func(cfg *globalconfig.Config) { cfg.Web.PasswordHash = "" }); err != nil {
				return err
			}
			c.Errorf("devyard: dashboard password cleared (restart to apply: `devyard daemon restart`)\n")
			return nil
		},
	}
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
