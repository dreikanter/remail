// Package cli implements the remail command surface.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Version is set at build time via -ldflags. See the Makefile.
var Version = "dev"

const summary = "remail — local, read-only mirror of an IMAP inbox"

// help is the entire help text. It is deliberately one screen: a wall of
// generated flag documentation makes a tool harder to use, not easier.
const help = summary + `

usage: remail <command> [options]

  init     set up a mail directory
  sync     fetch new mail and export it
  list     show messages, newest first
  read     print one message
  files    print a message's attachment paths

options:
  -p, --path <dir>   mail directory (default: current directory)
      --json         machine-readable output
      --version      print version

Messages are addressed by id, or any unambiguous prefix of one:
  remail read a1b2
`

// Execute runs the command line. It returns the process exit code.
func Execute() int {
	root := newRoot()
	if err := root.Execute(); err != nil {
		reportError(root, err)
		return 1
	}
	return 0
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "remail",
		Short:         summary,
		Version:       version(),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown command %q", args[0])
			}
			fmt.Fprint(cmd.OutOrStdout(), help)
			return nil
		},
	}

	root.PersistentFlags().StringP("path", "p", "", "mail directory")
	root.PersistentFlags().Bool("json", false, "machine-readable output")

	// Pre-declaring help lets it be hidden; otherwise cobra injects a visible
	// one into every subcommand.
	root.PersistentFlags().BoolP("help", "h", false, "")
	_ = root.PersistentFlags().MarkHidden("help")

	root.SetVersionTemplate("remail {{.Version}}\n")
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetHelpCommand(&cobra.Command{Hidden: true})

	// Bypass cobra's template engine entirely. Cobra's generated help is the
	// wall of text this tool is meant to avoid.
	root.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		if cmd.Parent() == nil {
			fmt.Fprint(cmd.OutOrStdout(), help)
			return
		}
		fmt.Fprint(cmd.OutOrStdout(), commandHelp(cmd))
	})

	root.AddCommand(newInit(), newSync(), newList(), newRead(), newFiles())
	return root
}

// commandHelp renders a few lines for one subcommand: what it does, how to
// invoke it, and only the flags it actually accepts. Global options stay in the
// top-level help rather than being repeated under every command.
func commandHelp(cmd *cobra.Command) string {
	var b strings.Builder
	fmt.Fprintf(&b, "remail %s — %s\n\nusage: remail %s\n", cmd.Name(), cmd.Short, cmd.Use)

	var flags []string
	width := 0
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Name == "help" {
			return
		}
		name := "--" + f.Name
		if f.Shorthand != "" {
			name = "-" + f.Shorthand + ", " + name
		}
		if len(name) > width {
			width = len(name)
		}
		flags = append(flags, name+"\x00"+f.Usage)
	})

	if len(flags) > 0 {
		b.WriteString("\n")
		for _, entry := range flags {
			name, usage, _ := strings.Cut(entry, "\x00")
			fmt.Fprintf(&b, "  %-*s   %s\n", width, name, usage)
		}
	}
	return b.String()
}

func version() string {
	if Version != "dev" && Version != "" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// reportError writes a failure in whichever form the caller asked for.
func reportError(root *cobra.Command, err error) {
	asJSON, _ := root.PersistentFlags().GetBool("json")
	if asJSON {
		writeJSON(os.Stdout, errorEnvelope{Error: errorBody{Message: err.Error()}})
		return
	}
	fmt.Fprintln(os.Stderr, "remail: "+err.Error())
}

// mailDir resolves --path, defaulting to the current directory.
func mailDir(cmd *cobra.Command) (string, error) {
	p, err := cmd.Flags().GetString("path")
	if err != nil {
		return "", err
	}
	if p == "" {
		p, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func jsonMode(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("json")
	return v
}

func out(cmd *cobra.Command) io.Writer { return cmd.OutOrStdout() }

// oneLine collapses a header value so it cannot break a one-line-per-message
// listing. Subjects legitimately contain newlines after header unfolding.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return strings.Join(strings.Fields(s), " ")
}
