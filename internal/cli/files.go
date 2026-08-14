package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/dreikanter/remail/internal/store"
)

func newFiles() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "files <id>",
		Short: "print a message's attachment paths",
		Args:  cobra.ExactArgs(1),
		RunE:  runFiles,
	}
	cmd.Flags().Bool("inline", false, "include embedded images")
	return cmd
}

type filesResult struct {
	ID    string   `json:"id"`
	Dir   string   `json:"dir"`
	Files []string `json:"files"`
}

func runFiles(cmd *cobra.Command, args []string) error {
	dir, err := mailDir(cmd)
	if err != nil {
		return err
	}
	withInline, _ := cmd.Flags().GetBool("inline")

	msg, err := store.Find(dir, args[0])
	if err != nil {
		return err
	}

	// Absolute paths, so the output can be piped straight into another tool
	// regardless of the caller's working directory.
	paths := make([]string, 0, len(msg.Attachments))
	for _, name := range msg.Attachments {
		paths = append(paths, filepath.Join(msg.Dir, name))
	}
	if withInline {
		for _, name := range msg.Inline {
			paths = append(paths, filepath.Join(msg.Dir, store.InlineDir, name))
		}
	}

	if jsonMode(cmd) {
		return emitJSON(out(cmd), filesResult{ID: msg.ID, Dir: msg.Dir, Files: paths})
	}

	for _, p := range paths {
		fmt.Fprintln(out(cmd), p)
	}
	return nil
}
