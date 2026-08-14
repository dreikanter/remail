// Package imapsrc implements source.Source over IMAP.
//
// Read-only is structural rather than a matter of care: the mailbox is opened
// with EXAMINE and bodies are fetched with the PEEK variant, so neither
// selecting the mailbox nor reading a message can set \Seen. No code path here
// issues STORE, APPEND, EXPUNGE, or COPY.
package imapsrc

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/dreikanter/remail/internal/source"
)

// Client is a read-only IMAP mailbox.
type Client struct {
	c           *imapclient.Client
	uidValidity uint32
}

// Config is what imapsrc needs to connect. It is a plain struct rather than the
// config package's type so this backend does not depend on the file format.
type Config struct {
	Addr     string
	Host     string
	Account  string
	Password string
	Mailbox  string
}

// Open connects, authenticates, and selects the mailbox read-only.
func Open(cfg Config) (*Client, error) {
	c, err := imapclient.DialTLS(cfg.Addr, &imapclient.Options{
		TLSConfig: &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12},
	})
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", cfg.Addr, err)
	}

	if err := c.Login(cfg.Account, cfg.Password).Wait(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("login as %s: %w", cfg.Account, err)
	}

	data, err := c.Select(cfg.Mailbox, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("open mailbox %q: %w", cfg.Mailbox, err)
	}

	return &Client{c: c, uidValidity: data.UIDValidity}, nil
}

// UIDValidity implements source.Source.
func (cl *Client) UIDValidity() uint32 { return cl.uidValidity }

// List implements source.Source.
func (cl *Client) List(ctx context.Context, fromUID uint32, since time.Time) ([]source.Ref, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	criteria := &imap.SearchCriteria{}
	if fromUID > 0 {
		var set imap.UIDSet
		set.AddRange(imap.UID(fromUID), 0) // 0 means "*", i.e. fromUID:*
		criteria.UID = []imap.UIDSet{set}
	}
	if !since.IsZero() {
		// IMAP SINCE compares dates only; the time of day is ignored.
		criteria.Since = since
	}

	data, err := cl.c.UIDSearch(criteria, &imap.SearchOptions{ReturnAll: true}).Wait()
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	uids := data.AllUIDs()
	if len(uids) == 0 {
		return nil, nil
	}

	// A "N:*" range always matches the last message in the mailbox even when
	// its UID is below N, so the server can hand back a message already
	// fetched. Filtering here is what keeps an idle sync a genuine no-op.
	kept := uids[:0]
	for _, uid := range uids {
		if uint32(uid) >= fromUID {
			kept = append(kept, uid)
		}
	}
	if len(kept) == 0 {
		return nil, nil
	}

	return cl.describe(kept)
}

// describe fetches metadata only, so listing stays cheap on a large mailbox.
func (cl *Client) describe(uids []imap.UID) ([]source.Ref, error) {
	cmd := cl.c.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{
		UID:          true,
		InternalDate: true,
		RFC822Size:   true,
	})
	defer cmd.Close() //nolint:errcheck // error surfaced by cmd.Close below

	var refs []source.Ref
	for msg := cmd.Next(); msg != nil; msg = cmd.Next() {
		var ref source.Ref
		for item := msg.Next(); item != nil; item = msg.Next() {
			switch item := item.(type) {
			case imapclient.FetchItemDataUID:
				ref.UID = uint32(item.UID)
			case imapclient.FetchItemDataInternalDate:
				ref.Date = item.Time
			case imapclient.FetchItemDataRFC822Size:
				ref.Size = item.Size
			}
		}
		if ref.UID != 0 {
			refs = append(refs, ref)
		}
	}

	if err := cmd.Close(); err != nil {
		return nil, fmt.Errorf("fetch metadata: %w", err)
	}
	return refs, nil
}

// Fetch implements source.Source.
func (cl *Client) Fetch(ctx context.Context, ref source.Ref) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Peek is what makes reading a message leave \Seen untouched. Without it
	// the archive would silently mark the user's mail as read.
	section := &imap.FetchItemBodySection{Peek: true}

	cmd := cl.c.Fetch(imap.UIDSetNum(imap.UID(ref.UID)), &imap.FetchOptions{
		UID:         true,
		BodySection: []*imap.FetchItemBodySection{section},
	})
	defer cmd.Close() //nolint:errcheck // error surfaced by cmd.Close below

	var raw []byte
	for msg := cmd.Next(); msg != nil; msg = cmd.Next() {
		for item := msg.Next(); item != nil; item = msg.Next() {
			body, ok := item.(imapclient.FetchItemDataBodySection)
			if !ok {
				continue
			}
			b, err := io.ReadAll(body.Literal)
			if err != nil {
				return nil, fmt.Errorf("read UID %d: %w", ref.UID, err)
			}
			raw = b
		}
	}

	if err := cmd.Close(); err != nil {
		return nil, fmt.Errorf("fetch UID %d: %w", ref.UID, err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("UID %d returned no data", ref.UID)
	}
	return raw, nil
}

// Close logs out and closes the connection.
func (cl *Client) Close() error {
	if err := cl.c.Logout().Wait(); err != nil {
		_ = cl.c.Close()
		return err
	}
	return cl.c.Close()
}

var _ source.Source = (*Client)(nil)
