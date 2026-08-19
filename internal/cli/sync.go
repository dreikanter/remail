package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/dreikanter/remail/internal/sync"
)

func newSync() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync [options]",
		Short: "fetch new mail and export it to text",
		Long: `Fetches messages that arrived since the last run, stores each original
under raw/, and exports a readable copy under messages/. Mail is never
marked as read and nothing is written to the server.

messages/ is derived and safe to delete; it is rebuilt from raw/.
Interrupting a sync is safe: the next run resumes where it stopped.`,
		Example: `  remail sync
  remail sync --offline --rebuild
  remail --path ~/mail sync`,
		Args: cobra.NoArgs,
		RunE: runSync,
	}
	cmd.Flags().Bool("rebuild", false, "re-export every stored message from raw/")
	cmd.Flags().Bool("offline", false, "skip the server, only rebuild from raw/")
	return cmd
}

func runSync(cmd *cobra.Command, _ []string) error {
	dir, err := mailDir(cmd)
	if err != nil {
		return err
	}
	rebuild, _ := cmd.Flags().GetBool("rebuild")
	offline, _ := cmd.Flags().GetBool("offline")

	// Stop between messages, not mid-write, so the next run resumes cleanly.
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
		// Ctrl-C is a normal way to stop a sync, and "context canceled" is not
		// what the person who pressed it needs to read.
		if ctx.Err() != nil && errors.Is(err, context.Canceled) {
			return errors.New("interrupted")
		}
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
