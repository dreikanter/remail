package store

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// frontmatterLimit caps how much of message.md a listing reads, instead of
// parsing multi-megabyte .eml files.
const frontmatterLimit = 32 << 10

const fence = "---"

// Frontmatter is the YAML header block at the top of message.md, and the
// record `list` reads.
type Frontmatter struct {
	ID string `yaml:"id" json:"id"`

	// Date is the server's receipt time (INTERNALDATE). It drives sort order and
	// paths because, unlike the Date header, the sender cannot set it.
	Date time.Time `yaml:"date" json:"date"`

	// Sent is the Date header as the sender wrote it, kept for reference.
	Sent string `yaml:"sent,omitempty" json:"sent,omitempty"`

	From    string   `yaml:"from" json:"from"`
	To      []string `yaml:"to,omitempty" json:"to,omitempty"`
	Cc      []string `yaml:"cc,omitempty" json:"cc,omitempty"`
	ReplyTo string   `yaml:"reply_to,omitempty" json:"reply_to,omitempty"`
	Subject string   `yaml:"subject" json:"subject"`

	MessageID  string   `yaml:"message_id,omitempty" json:"message_id,omitempty"`
	InReplyTo  string   `yaml:"in_reply_to,omitempty" json:"in_reply_to,omitempty"`
	References []string `yaml:"references,omitempty" json:"references,omitempty"`
	ListID     string   `yaml:"list_id,omitempty" json:"list_id,omitempty"`

	Attachments []string `yaml:"attachments,omitempty" json:"attachments,omitempty"`
	Inline      []string `yaml:"inline,omitempty" json:"inline,omitempty"`

	// HTML reports that the message had an HTML part, preserved as message.html.
	HTML bool `yaml:"html,omitempty" json:"html,omitempty"`
	// Converted reports that the body below came from that HTML rather than
	// from a text/plain part, so a search miss can be explained.
	Converted bool `yaml:"converted,omitempty" json:"converted,omitempty"`

	// Raw points at the original .eml, relative to the message directory.
	Raw string `yaml:"raw" json:"raw"`
}

// Message is a Frontmatter plus where it was found.
type Message struct {
	Frontmatter
	Dir string `json:"dir"`
}

// Render returns message.md contents: the frontmatter block followed by body.
func Render(fm *Frontmatter, body string) ([]byte, error) {
	// Must be marshalled, not formatted: subjects routinely contain colons,
	// quotes, and newlines from folded headers.
	meta, err := yaml.Marshal(fm)
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer
	b.WriteString(fence + "\n")
	b.Write(meta)
	b.WriteString(fence + "\n\n")
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteString("\n")
	return b.Bytes(), nil
}

// ReadFrontmatter parses the header block of a message.md.
func ReadFrontmatter(path string) (*Frontmatter, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only

	head, err := io.ReadAll(io.LimitReader(f, frontmatterLimit))
	if err != nil {
		return nil, err
	}

	block, err := extractFrontmatter(head)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	var fm Frontmatter
	if err := yaml.Unmarshal(block, &fm); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &fm, nil
}

func extractFrontmatter(data []byte) ([]byte, error) {
	s := string(data)
	if !strings.HasPrefix(s, fence+"\n") {
		return nil, errors.New("missing YAML frontmatter")
	}
	rest := s[len(fence)+1:]

	end := strings.Index(rest, "\n"+fence+"\n")
	if end < 0 {
		return nil, errors.New("unterminated YAML frontmatter")
	}
	return []byte(rest[:end+1]), nil
}

// shards returns the YYYY-MM directories under messages/, newest first.
func shards(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, MessagesDir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	slices.Reverse(names)
	return names, nil
}

func readShard(root, shard string) ([]Message, error) {
	dir := filepath.Join(root, MessagesDir, shard)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var out []Message
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name(), MessageFile)
		fm, err := ReadFrontmatter(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // a directory mid-write, or hand-deleted content
			}
			return nil, err
		}
		out = append(out, Message{Frontmatter: *fm, Dir: filepath.Join(dir, e.Name())})
	}
	return out, nil
}

// List returns messages newest first. A limit of 0 means no limit; a non-zero
// since drops anything older. Shards are read newest first and scanning stops
// once the limit is filled, so a large archive costs only the recent months.
func List(root string, limit int, since time.Time) ([]Message, error) {
	names, err := shards(root)
	if err != nil {
		return nil, err
	}

	var out []Message
	for _, shard := range names {
		batch, err := readShard(root, shard)
		if err != nil {
			return nil, err
		}
		for _, m := range batch {
			if !since.IsZero() && m.Date.Before(since) {
				continue
			}
			out = append(out, m)
		}

		// Every remaining shard is older, so nothing newer is left to find.
		if limit > 0 && len(out) >= limit {
			break
		}
	}

	slices.SortStableFunc(out, func(a, b Message) int { return b.Date.Compare(a.Date) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Find resolves a message reference: a full directory name, a full id, or any
// unambiguous id prefix.
func Find(root, ref string) (*Message, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errors.New("no message id given")
	}
	ref = strings.TrimSuffix(ref, string(os.PathSeparator))

	names, err := shards(root)
	if err != nil {
		return nil, err
	}

	var matches []Message
	for _, shard := range names {
		batch, err := readShard(root, shard)
		if err != nil {
			return nil, err
		}
		for _, m := range batch {
			if filepath.Base(m.Dir) == ref || strings.HasPrefix(m.ID, ref) {
				matches = append(matches, m)
			}
		}
	}

	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no message matching %q", ref)
	case 1:
		return &matches[0], nil
	default:
		var ids []string
		for _, m := range matches {
			ids = append(ids, m.ID)
		}
		slices.Sort(ids)
		return nil, fmt.Errorf("%q matches %d messages (%s)", ref, len(matches), strings.Join(ids, ", "))
	}
}
