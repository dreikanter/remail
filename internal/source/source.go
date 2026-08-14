// Package source abstracts where mail comes from.
//
// The interface is deliberately small: everything downstream — storage,
// parsing, export — works on raw RFC 822 bytes, so a second backend (JMAP, a
// local Maildir importer) only has to answer "which messages exist" and "give
// me the bytes". Anything wider would be designed against a single
// implementation and probably wrong.
package source

import (
	"context"
	"time"
)

// Ref identifies one message on the server.
type Ref struct {
	// UID is stable for as long as UIDValidity does not change.
	UID uint32
	// Date is the server's receipt time (INTERNALDATE).
	Date time.Time
	Size int64
}

// Source is a read-only view of one remote mailbox.
type Source interface {
	// UIDValidity is the server's UID epoch. When it changes, previously
	// recorded UIDs mean nothing and the mailbox must be rescanned.
	UIDValidity() uint32

	// List returns messages with UID >= fromUID, restricted to those received
	// at or after since when since is non-zero.
	List(ctx context.Context, fromUID uint32, since time.Time) ([]Ref, error)

	// Fetch returns the original bytes of one message without altering server
	// state (in particular without marking it read).
	Fetch(ctx context.Context, ref Ref) ([]byte, error)

	Close() error
}
