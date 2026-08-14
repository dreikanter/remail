package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/dreikanter/remail/internal/sync"
)

func newSync() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "fetch new mail and export it",
		Args:  cobra.NoArgs,
		RunE:  runSync,
	}
	cmd.Flags().Bool("rebuild", false, "re-export every stored message from raw/")
	cmd.Flags().Bool("offline", false, "skip the server; only rebuild from raw/")
	return cmd
}

func runSync(cmd *cobra.Command, _ []string) error {
	dir, err := mailDir(cmd)
	if err != nil {
		return err
	}
	rebuild, _ := cmd.Flags().GetBool("rebuild")
	offline, _ := cmd.Flags().GetBool("offline")

	// Ctrl-C stops between messages rather than mid-write, so the archive is
	// always left consistent and the next run resumes.
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	opts := sync.Options{Rebuild: rebuild, Offline: offline}
	if !jsonMode(cmd) {
		opts.Progress = func(n int, subject string) {
			fmt.Fprintf(out(cmd), "%4d  %s\n", n, oneLine(subject))
		}
	}

	res, err := sync.Run(ctx, dir, opts)
	if err != nil {
		return err
	}

	if jsonMode(cmd) {
		return emitJSON(out(cmd), res)
	}

	switch {
	case res.Fetched == 0 && res.Rebuilt == 0:
		fmt.Fprintln(out(cmd), "no new mail")
	case res.Rebuilt > 0:
		fmt.Fprintf(out(cmd), "%s fetched, %s rebuilt\n",
			plural(res.Fetched, "message", "messages"),
			plural(res.Rebuilt, "message", "messages"))
	default:
		fmt.Fprintf(out(cmd), "%s fetched\n", plural(res.Fetched, "message", "messages"))
	}
	return nil
}
