// Package source abstracts where mail comes from.
//
// Everything downstream works on raw RFC 822 bytes, so a second backend only
// has to answer "which messages exist" and "give me the bytes".
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
	// UIDValidity is the server's UID epoch. A change invalidates recorded UIDs.
	UIDValidity() uint32

	// List returns messages with UID >= fromUID, restricted to those received
	// at or after since when since is non-zero.
	List(ctx context.Context, fromUID uint32, since time.Time) ([]Ref, error)

	// Fetch returns one message's original bytes without marking it read.
	Fetch(ctx context.Context, ref Ref) ([]byte, error)

	Close() error
}
