package cli

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dreikanter/remail/internal/config"
	"github.com/dreikanter/remail/internal/store"
)

// keychainService is the service name suggested for the stored password.
const keychainService = "remail"

func newInit() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init [options]",
		Short: "create a mail directory and its config",
		Long: `Creates the directory if needed, writes remail.json, and prints the
command to store your password in the system keychain. An existing
config is never overwritten.

The config travels with the mail, so nothing lands in your home
directory and a mailbox can be moved between machines as one folder.`,
		Example: `  remail init --account you@gmail.com
  remail init --account you@fastmail.com --provider fastmail --since-days 90`,
		Args: cobra.NoArgs,
		RunE: runInit,
	}
	cmd.Flags().String("account", "", "email address (required)")
	cmd.Flags().String("provider", "gmail", "preset: "+strings.Join(config.Providers(), ", "))
	cmd.Flags().Int("since-days", 0, "on first sync, skip mail older than this, 0 for all")
	return cmd
}

type initResult struct {
	Path    string `json:"path"`
	Config  string `json:"config"`
	PassCmd string `json:"pass_cmd"`
	Store   string `json:"store_command"`
}

func runInit(cmd *cobra.Command, _ []string) error {
	dir, err := mailDir(cmd)
	if err != nil {
		return err
	}
	account, _ := cmd.Flags().GetString("account")
	provider, _ := cmd.Flags().GetString("provider")
	sinceDays, _ := cmd.Flags().GetInt("since-days")

	if account == "" {
		return fmt.Errorf("--account is required")
	}

	cfg := &config.Config{
		Provider:  provider,
		Account:   account,
		PassCmd:   defaultPassCmd(account),
		SinceDays: sinceDays,
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := store.EnsureLayout(dir); err != nil {
		return err
	}
	if err := cfg.Save(dir); err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("%s already exists", config.Path(dir))
		}
		return err
	}

	res := initResult{
		Path:    dir,
		Config:  config.Path(dir),
		PassCmd: cfg.PassCmd,
		Store:   storeCommand(account),
	}

	if jsonMode(cmd) {
		return emitJSON(out(cmd), res)
	}

	fmt.Fprintf(out(cmd), "Created %s\n\n", res.Config)
	fmt.Fprintf(out(cmd), "Store the password (an app password, not the account password):\n\n  %s\n\n", res.Store)
	fmt.Fprintf(out(cmd), "Then run:\n\n  remail sync\n")
	return nil
}

// defaultPassCmd suggests a retrieval command for the host's usual secret
// store. Any command that prints the password on stdout works; this is only a
// starting point the user is expected to edit if they use pass, 1Password, or
// anything else.
func defaultPassCmd(account string) string {
	switch runtime.GOOS {
	case "darwin":
		return fmt.Sprintf("security find-generic-password -s %s -a %s -w", keychainService, account)
	default:
		return fmt.Sprintf("secret-tool lookup service %s account %s", keychainService, account)
	}
}

func storeCommand(account string) string {
	switch runtime.GOOS {
	case "darwin":
		// -U upserts; plain add fails if the item already exists.
		return fmt.Sprintf("security add-generic-password -U -s %s -a %s -w", keychainService, account)
	default:
		return fmt.Sprintf("secret-tool store --label=%s service %s account %s", keychainService, keychainService, account)
	}
}
