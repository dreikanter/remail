package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dreikanter/remail/internal/store"
)

const (
	fromWidth    = 24
	subjectWidth = 60
)

func newList() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "show messages, newest first",
		Args:  cobra.NoArgs,
		RunE:  runList,
	}
	cmd.Flags().IntP("number", "n", 20, "how many to show (0 = all)")
	cmd.Flags().String("since", "", "only messages newer than this (7d, 2026-08-01)")
	return cmd
}

type listResult struct {
	Messages []store.Message `json:"messages"`
	Count    int             `json:"count"`
}

func runList(cmd *cobra.Command, _ []string) error {
	dir, err := mailDir(cmd)
	if err != nil {
		return err
	}
	limit, _ := cmd.Flags().GetInt("number")
	sinceFlag, _ := cmd.Flags().GetString("since")

	since, err := parseSince(sinceFlag)
	if err != nil {
		return err
	}

	messages, err := store.List(dir, limit, since)
	if err != nil {
		return err
	}

	if jsonMode(cmd) {
		return emitJSON(out(cmd), listResult{Messages: messages, Count: len(messages)})
	}

	if len(messages) == 0 {
		fmt.Fprintln(out(cmd), "no messages")
		return nil
	}

	for _, m := range messages {
		marker := "  "
		if n := len(m.Attachments); n > 0 {
			marker = fmt.Sprintf("%d@", n)
		}
		fmt.Fprintf(out(cmd), "%s  %s  %s  %s  %s\n",
			m.ID,
			m.Date.Local().Format("2006-01-02 15:04"),
			marker,
			pad(sender(m.From), fromWidth),
			truncate(oneLine(m.Subject), subjectWidth),
		)
	}
	return nil
}

// parseSince accepts a day offset ("7d") or a calendar date ("2026-08-01").
func parseSince(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}

	if days, ok := strings.CutSuffix(s, "d"); ok {
		var n int
		if _, err := fmt.Sscanf(days, "%d", &n); err == nil && n >= 0 {
			return time.Now().AddDate(0, 0, -n), nil
		}
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("cannot read --since %q: use 7d or 2026-08-01", s)
}

// sender shows the display name when there is one, since "Acme Billing" is more
// use in a list than "billing@bounces.acme.example".
func sender(from string) string {
	from = oneLine(from)
	if i := strings.LastIndex(from, "<"); i > 0 {
		if name := strings.TrimSpace(strings.Trim(from[:i], `"' `)); name != "" {
			return name
		}
	}
	return strings.Trim(from, "<>")
}

func truncate(s string, width int) string {
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width <= 1 {
		return string(r[:width])
	}
	return string(r[:width-1]) + "…"
}

func pad(s string, width int) string {
	s = truncate(s, width)
	if n := width - len([]rune(s)); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}
