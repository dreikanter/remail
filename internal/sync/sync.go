// Package sync fetches new mail into a mail directory and exports it.
package sync

import (
	"context"
	"fmt"
	"time"

	"github.com/dreikanter/remail/internal/config"
	"github.com/dreikanter/remail/internal/export"
	"github.com/dreikanter/remail/internal/source"
	"github.com/dreikanter/remail/internal/source/imapsrc"
	"github.com/dreikanter/remail/internal/state"
	"github.com/dreikanter/remail/internal/store"
)

// flushEvery bounds how much progress an interrupted sync can lose. State is
// only ever behind the filesystem, never ahead, so a re-run re-fetches at most
// this many messages rather than duplicating or skipping any.
const flushEvery = 25

// Result summarizes a sync for the caller to report.
type Result struct {
	Fetched  int `json:"fetched"`
	Exported int `json:"exported"`
	Rebuilt  int `json:"rebuilt"`
	Skipped  int `json:"skipped"`
}

// Options adjusts a run.
type Options struct {
	// Rebuild re-exports every stored message even when the format is current.
	Rebuild bool
	// Offline skips the server entirely and only rebuilds from raw/.
	Offline bool
	// Progress, when set, is called as each message is stored.
	Progress func(n int, subject string)
}

// Run performs a sync against the mail directory at root.
func Run(ctx context.Context, root string, opts Options) (*Result, error) {
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	st, err := state.Load(root)
	if err != nil {
		return nil, err
	}
	if err := store.EnsureLayout(root); err != nil {
		return nil, err
	}

	res := &Result{}

	// A format bump means every message directory was produced by older code,
	// so they are regenerated from raw/ before anything new arrives.
	if opts.Rebuild || st.ExportFormat != export.Format {
		n, err := Rebuild(ctx, root)
		if err != nil {
			return nil, err
		}
		res.Rebuilt = n
		st.ExportFormat = export.Format
		if err := st.Save(root); err != nil {
			return nil, err
		}
	}

	if opts.Offline {
		return res, nil
	}

	password, err := cfg.Password()
	if err != nil {
		return nil, err
	}

	src, err := imapsrc.Open(imapsrc.Config{
		Addr:     cfg.Addr(),
		Host:     cfg.Host,
		Account:  cfg.Account,
		Password: password,
		Mailbox:  cfg.Mailbox,
	})
	if err != nil {
		return nil, err
	}
	defer src.Close() //nolint:errcheck // best-effort logout

	if err := fetch(ctx, root, cfg, st, src, res, opts); err != nil {
		// Persist whatever was fetched before the failure.
		_ = st.Save(root)
		return nil, err
	}

	st.LastSync = time.Now().UTC()
	if err := st.Save(root); err != nil {
		return nil, err
	}
	return res, nil
}

func fetch(
	ctx context.Context,
	root string,
	cfg *config.Config,
	st *state.State,
	src source.Source,
	res *Result,
	opts Options,
) error {
	// UIDs are only meaningful within one UIDVALIDITY epoch. When the server
	// changes it, previously recorded UIDs describe different messages, so the
	// mailbox is rescanned. Nothing is re-downloaded twice on disk: the id is
	// derived from Message-ID, so refetched mail lands on the same paths.
	if st.UIDValidity != src.UIDValidity() {
		st.Reset(src.UIDValidity())
	}

	fromUID := st.NextUID()

	// since_days bounds the first sync only. Once UIDs are recorded, the UID
	// range is both cheaper and exact, while IMAP's SINCE has day granularity.
	var since time.Time
	if st.Fetched.Len() == 0 && cfg.SinceDays > 0 {
		since = time.Now().AddDate(0, 0, -cfg.SinceDays)
	}

	refs, err := src.List(ctx, fromUID, since)
	if err != nil {
		return err
	}

	var pending int
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if st.Fetched.Contains(ref.UID) {
			res.Skipped++
			continue
		}

		raw, err := src.Fetch(ctx, ref)
		if err != nil {
			return err
		}

		msg, err := storeMessage(root, raw, ref.Date)
		if err != nil {
			return fmt.Errorf("UID %d: %w", ref.UID, err)
		}

		res.Fetched++
		res.Exported++
		st.Fetched.Add(ref.UID)
		pending++

		if opts.Progress != nil {
			opts.Progress(res.Fetched, msg.Subject)
		}

		if pending >= flushEvery {
			if err := st.Save(root); err != nil {
				return err
			}
			pending = 0
		}
	}
	return nil
}

func storeMessage(root string, raw []byte, date time.Time) (*store.Message, error) {
	// The id depends on the Message-ID header, so the raw path is only known
	// after a parse. Export does that parse and returns the record.
	id, err := export.IdentifyID(raw)
	if err != nil {
		return nil, err
	}

	rawPath, _, err := store.WriteRaw(root, date, id, raw)
	if err != nil {
		return nil, err
	}
	return export.Message(root, rawPath, raw, date)
}

// Rebuild regenerates messages/ from raw/, discarding whatever was there.
func Rebuild(ctx context.Context, root string) (int, error) {
	files, err := store.RawFiles(root)
	if err != nil {
		return 0, err
	}

	var n int
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return n, err
		}

		date, _, err := store.ParseRawName(path)
		if err != nil {
			return n, err
		}
		raw, err := store.ReadRaw(path)
		if err != nil {
			return n, err
		}
		if _, err := export.Message(root, path, raw, date); err != nil {
			return n, fmt.Errorf("%s: %w", path, err)
		}
		n++
	}
	return n, nil
}
