// Command remail keeps a local, read-only mirror of an IMAP inbox and exports
// every message as plain text plus real attachment files.
package main

import (
	"os"

	"github.com/dreikanter/remail/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
