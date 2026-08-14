// Package state tracks what a mail directory has already fetched, so a repeat
// sync costs one round trip instead of a full mailbox scan.
package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/dreikanter/remail/internal/atomicfile"
)

// Dir is the state directory inside a mail directory.
const Dir = ".remail"

// FileName is the state file inside Dir.
const FileName = "state.json"

// Format is the state schema version.
const Format = 1

// State is the on-disk sync bookkeeping.
type State struct {
	Format int `json:"format"`

	// ExportFormat records which export layout produced messages/. When the
	// binary's export.Format is newer, sync rebuilds the tree from raw/.
	ExportFormat int `json:"export_format"`

	// UIDValidity is the server's UID epoch. If it changes, every stored UID is
	// meaningless and the mailbox must be rescanned.
	UIDValidity uint32 `json:"uidvalidity"`

	// Fetched is every UID successfully written to raw/, kept so a future prune
	// can diff local state against the server. Recording only the high-water
	// mark would make remote deletions undetectable after the fact.
	Fetched UIDSet `json:"fetched"`

	LastSync time.Time `json:"last_sync,omitzero"`
}

// Path returns the state file path for a mail directory.
func Path(dir string) string { return filepath.Join(dir, Dir, FileName) }

// Load reads state for a mail directory. A missing file yields a zero state,
// which is what a first sync expects.
func Load(dir string) (*State, error) {
	data, err := os.ReadFile(Path(dir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &State{Format: Format}, nil
		}
		return nil, err
	}

	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.Format > Format {
		return nil, errors.New("state.json was written by a newer remail; upgrade remail")
	}
	s.Format = Format
	return &s, nil
}

// Save writes state atomically.
func (s *State) Save(dir string) error {
	s.Format = Format
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(Path(dir), append(data, '\n'), 0o600)
}

// NextUID is the lowest UID a sync still needs to ask the server about.
func (s *State) NextUID() uint32 { return s.Fetched.Max() + 1 }

// Reset clears fetch progress after a UIDVALIDITY change, keeping the export
// format so an unnecessary rebuild is not triggered.
func (s *State) Reset(uidValidity uint32) {
	s.UIDValidity = uidValidity
	s.Fetched = UIDSet{}
}
