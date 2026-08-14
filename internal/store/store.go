// Package store owns the on-disk layout of a mail directory.
//
//	<root>/remail.json                     config
//	<root>/.remail/state.json              sync bookkeeping
//	<root>/raw/2026-08/<date>-<id>.eml     source of truth, never modified
//	<root>/messages/2026-08/<date>-<slug>-<id>/
//	    message.md                         readable text, YAML frontmatter
//	    message.html                       original HTML, when present
//	    <attachment files>
//	    inline/<embedded images>
//
// Both trees sit under a YYYY-MM shard so a large mailbox does not put tens of
// thousands of entries in one directory.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/dreikanter/remail/internal/atomicfile"
)

const (
	RawDir      = "raw"
	MessagesDir = "messages"

	// MessageFile is the readable rendering of a message.
	MessageFile = "message.md"
	// HTMLFile holds the original HTML body when the message had one.
	HTMLFile = "message.html"
	// InlineDir holds embedded images, kept out of the way of real attachments.
	InlineDir = "inline"

	idLen      = 8
	maxSlugLen = 60
)

// ID derives a message's stable identifier: the first 8 hex characters of the
// SHA-256 of its Message-ID header.
//
// Using the header rather than the IMAP UID means the id survives a
// UIDVALIDITY reset, is identical across providers, and collapses Gmail's
// per-label duplicates of the same message onto one entry. Messages with no
// Message-ID (rare, but legal) fall back to hashing the raw bytes.
func ID(messageID string, raw []byte) string {
	messageID = strings.TrimSpace(messageID)
	var sum [sha256.Size]byte
	if messageID != "" {
		sum = sha256.Sum256([]byte(messageID))
	} else {
		sum = sha256.Sum256(raw)
	}
	return hex.EncodeToString(sum[:])[:idLen]
}

// Shard is the YYYY-MM directory a timestamp belongs to.
func Shard(t time.Time) string { return t.UTC().Format("2006-01") }

func day(t time.Time) string { return t.UTC().Format("2006-01-02") }

// rawStamp is the timestamp format embedded in raw filenames.
//
// The full receipt time is carried here, not just the date, so rebuilding
// messages/ from raw/ recovers the exact timestamp without consulting any other
// file. That is what lets messages/ be deleted outright and regenerated.
const rawStamp = "2006-01-02T150405Z"

// RawPath is where a message's original bytes live, relative to root.
func RawPath(root string, t time.Time, id string) string {
	name := fmt.Sprintf("%s-%s.eml", t.UTC().Format(rawStamp), id)
	return filepath.Join(root, RawDir, Shard(t), name)
}

// ParseRawName recovers the receipt time and id from a raw filename.
func ParseRawName(path string) (time.Time, string, error) {
	name := strings.TrimSuffix(filepath.Base(path), ".eml")

	i := strings.LastIndex(name, "-")
	if i < 0 {
		return time.Time{}, "", fmt.Errorf("unrecognized raw filename %q", filepath.Base(path))
	}
	stamp, id := name[:i], name[i+1:]

	t, err := time.Parse(rawStamp, stamp)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("unrecognized raw filename %q: %w", filepath.Base(path), err)
	}
	return t.UTC(), id, nil
}

// MessageDir is the exported directory for a message, relative to root.
func MessageDir(root string, t time.Time, subject, id string) string {
	name := day(t)
	if slug := Slug(subject); slug != "" {
		name += "-" + slug
	}
	name += "-" + id
	return filepath.Join(root, MessagesDir, Shard(t), name)
}

// Slug renders a subject as a filesystem-safe directory fragment.
//
// Letters and digits survive (including non-Latin ones, which keeps subjects
// readable in `ls` for non-English mail); everything else collapses to a single
// hyphen. Path separators are not letters or digits, so they cannot survive.
func Slug(subject string) string {
	var b strings.Builder
	var pendingSep bool

	for _, r := range subject {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if pendingSep && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingSep = false
			b.WriteRune(unicode.ToLower(r))
		default:
			pendingSep = true
		}
		if b.Len() >= maxSlugLen {
			break
		}
	}

	return strings.Trim(b.String(), "-")
}

// WriteRaw stores original message bytes, creating parents as needed. It
// reports whether the file was newly written; an existing file is left alone
// because raw/ is append-only and byte-identical on a refetch.
func WriteRaw(root string, t time.Time, id string, raw []byte) (string, bool, error) {
	path := RawPath(root, t, id)
	if _, err := os.Stat(path); err == nil {
		return path, false, nil
	} else if !os.IsNotExist(err) {
		return "", false, err
	}
	if err := atomicfile.Write(path, raw, 0o600); err != nil {
		return "", false, err
	}
	return path, true, nil
}

// ReadRaw returns the stored bytes for a raw path.
func ReadRaw(path string) ([]byte, error) { return os.ReadFile(path) }

// RawFiles lists every stored .eml under root, oldest first. Used to rebuild
// messages/ from the source of truth.
func RawFiles(root string) ([]string, error) {
	base := filepath.Join(root, RawDir)
	var files []string

	err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && path == base {
				return filepath.SkipAll
			}
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".eml") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(files)
	return files, nil
}

// EnsureLayout creates the directories a mail directory needs.
func EnsureLayout(root string) error {
	for _, d := range []string{RawDir, MessagesDir, ".remail"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o700); err != nil {
			return err
		}
	}
	return nil
}

// CopyInto streams src into a file, used for attachments so a large one never
// needs to be buffered in memory.
func CopyInto(path string, src io.Reader) error {
	return atomicfile.WriteFrom(path, func(w io.Writer) error {
		_, err := io.Copy(w, src)
		return err
	}, 0o600)
}
