package cli

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/dreikanter/remail/internal/store"
)

func newRead() *cobra.Command {
	return &cobra.Command{
		Use:   "read <id>",
		Short: "print one message",
		Args:  cobra.ExactArgs(1),
		RunE:  runRead,
	}
}

type readResult struct {
	store.Message
	Body string `json:"body"`
}

func runRead(cmd *cobra.Command, args []string) error {
	dir, err := mailDir(cmd)
	if err != nil {
		return err
	}

	msg, err := store.Find(dir, args[0])
	if err != nil {
		return err
	}

	data, err := os.ReadFile(filepath.Join(msg.Dir, store.MessageFile))
	if err != nil {
		return err
	}

	if jsonMode(cmd) {
		return emitJSON(out(cmd), readResult{Message: *msg, Body: string(data)})
	}

	_, err = out(cmd).Write(data)
	return err
}
