// Package cli implements the remail command surface.
package cli

import (
	"errors"
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

// help is the top-level help. It aims to fit one screen while still answering
// the questions a first-time user actually has: what the commands are, how to
// get started, and how mail is stored.
const help = summary + `

usage: remail <command> [options]

commands:
  init     create a mail directory and its config
  sync     fetch new mail and export it to text
  list     show messages, most recent first
  read     print one message to stdout
  files    print absolute paths to a message's attachments
  skill    print or install the agent skill for this CLI

options:
  -p, --path <dir>   mail directory (default: current directory)
      --json         machine-readable output
      --version      print version

getting started:
  mkdir ~/mail && cd ~/mail
  remail init --account you@gmail.com
  remail sync

  init writes remail.json and prints the command to store your password.
  Gmail needs an app password, not your account password.

messages:
  Each message has a short id, shown by list. Any unambiguous prefix works.

  remail list --since 7d
  remail read a1b2
  remail files a1b2 | xargs open

  Mail is stored as ordinary files: untouched originals in raw/, readable
  text and attachments in messages/. Search them with anything you like.

One directory is one mailbox. Run remail <command> --help for its options.
`

// errSilent fails the process without printing anything further, for commands
// whose own output already explains the failure.
var errSilent = errors.New("silent failure")

// Execute runs the command line. It returns the process exit code.
func Execute() int {
	root := newRoot()
	if err := root.Execute(); err != nil {
		if !errors.Is(err, errSilent) {
			reportError(root, err)
		}
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

	root.AddCommand(newInit(), newSync(), newList(), newRead(), newFiles(), newSkill())
	return root
}

// placeholders name the value a flag takes, so help reads "--since <when>"
// rather than "--since string". Only flags that take a value appear here.
var placeholders = map[string]string{
	"path":       "dir",
	"since":      "when",
	"number":     "n",
	"account":    "email",
	"provider":   "name",
	"since-days": "n",
	"agent":      "name",
}

// commandHelp renders help for one subcommand: what it does, how to invoke it,
// the flags it accepts, and a worked example or two. Global options stay in the
// top-level help instead of being repeated under every command.
func commandHelp(cmd *cobra.Command) string {
	var b strings.Builder
	fmt.Fprintf(&b, "remail %s — %s\n\nusage: remail %s\n", cmd.Name(), cmd.Short, cmd.Use)

	if flags := flagLines(cmd); len(flags) > 0 {
		b.WriteString("\noptions:\n")
		for _, line := range flags {
			b.WriteString(line)
		}
	}
	if cmd.Long != "" {
		fmt.Fprintf(&b, "\n%s\n", strings.TrimSpace(cmd.Long))
	}
	if cmd.Example != "" {
		fmt.Fprintf(&b, "\nexamples:\n%s\n", strings.TrimRight(cmd.Example, "\n"))
	}
	return b.String()
}

func flagLines(cmd *cobra.Command) []string {
	type entry struct{ name, usage string }

	var entries []entry
	width := 0
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Name == "help" {
			return
		}

		name := "    --" + f.Name
		if f.Shorthand != "" {
			name = "-" + f.Shorthand + ", --" + f.Name
		}
		if f.Value.Type() != "bool" {
			if p, ok := placeholders[f.Name]; ok {
				name += " <" + p + ">"
			} else {
				name += " <value>"
			}
		}

		if len(name) > width {
			width = len(name)
		}
		entries = append(entries, entry{name, f.Usage})
	})

	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		lines = append(lines, fmt.Sprintf("  %-*s   %s\n", width, e.name, e.usage))
	}
	return lines
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
