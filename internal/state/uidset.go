package state

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// uidRange is an inclusive range of UIDs.
type uidRange struct{ Lo, Hi uint32 }

// UIDSet is a set of IMAP UIDs held as sorted, merged ranges and serialized as
// an IMAP sequence set ("1:9000,9002:9500").
//
// UIDs in a mailbox are near-contiguous, so a mailbox of 100k messages usually
// collapses to a handful of ranges. Storing the same set as a JSON array of
// integers would cost hundreds of kilobytes and grow without bound; this costs
// tens of bytes and stays readable.
type UIDSet struct {
	ranges []uidRange
}

// ParseUIDSet reads an IMAP sequence set. An empty string is an empty set.
// The special "*" wildcard is not accepted: it has no meaning once stored.
func ParseUIDSet(s string) (*UIDSet, error) {
	set := &UIDSet{}
	s = strings.TrimSpace(s)
	if s == "" {
		return set, nil
	}

	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi, found := strings.Cut(part, ":")
		lv, err := parseUID(lo)
		if err != nil {
			return nil, fmt.Errorf("invalid sequence set %q: %w", s, err)
		}
		hv := lv
		if found {
			if hv, err = parseUID(hi); err != nil {
				return nil, fmt.Errorf("invalid sequence set %q: %w", s, err)
			}
		}
		if hv < lv {
			lv, hv = hv, lv
		}
		set.AddRange(lv, hv)
	}
	return set, nil
}

func parseUID(s string) (uint32, error) {
	v, err := strconv.ParseUint(strings.TrimSpace(s), 10, 32)
	if err != nil {
		return 0, err
	}
	if v == 0 {
		return 0, fmt.Errorf("UID 0 is not valid")
	}
	return uint32(v), nil
}

// Add records a single UID.
func (s *UIDSet) Add(uid uint32) { s.AddRange(uid, uid) }

// AddRange records an inclusive range, merging it with any range it touches or
// overlaps so the representation stays minimal.
func (s *UIDSet) AddRange(lo, hi uint32) {
	if lo == 0 || hi < lo {
		return
	}

	// First range that could touch or follow the new one. Ranges are adjacent
	// when hi+1 == next.Lo, so search with lo-1 to merge them too.
	probe := lo
	if probe > 1 {
		probe--
	}
	i := lowerBound(s.ranges, probe)

	merged := uidRange{Lo: lo, Hi: hi}
	j := i
	for j < len(s.ranges) && s.ranges[j].Lo <= saturatingInc(hi) {
		merged.Lo = min(merged.Lo, s.ranges[j].Lo)
		merged.Hi = max(merged.Hi, s.ranges[j].Hi)
		j++
	}

	s.ranges = slices.Replace(s.ranges, i, j, merged)
}

// saturatingInc avoids wrapping to 0 at the top of the UID space.
func saturatingInc(v uint32) uint32 {
	if v == ^uint32(0) {
		return v
	}
	return v + 1
}

// lowerBound returns the index of the first range whose Hi is >= v.
func lowerBound(ranges []uidRange, v uint32) int {
	lo, hi := 0, len(ranges)
	for lo < hi {
		mid := (lo + hi) / 2
		if ranges[mid].Hi < v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// Contains reports whether uid is in the set.
func (s *UIDSet) Contains(uid uint32) bool {
	i := lowerBound(s.ranges, uid)
	return i < len(s.ranges) && s.ranges[i].Lo <= uid && uid <= s.ranges[i].Hi
}

// Max returns the highest UID in the set, or 0 when empty.
func (s *UIDSet) Max() uint32 {
	if len(s.ranges) == 0 {
		return 0
	}
	return s.ranges[len(s.ranges)-1].Hi
}

// Len returns how many UIDs the set holds.
func (s *UIDSet) Len() int {
	var n int
	for _, r := range s.ranges {
		n += int(r.Hi-r.Lo) + 1
	}
	return n
}

// String renders the set as an IMAP sequence set.
func (s *UIDSet) String() string {
	var b strings.Builder
	for i, r := range s.ranges {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatUint(uint64(r.Lo), 10))
		if r.Hi != r.Lo {
			b.WriteByte(':')
			b.WriteString(strconv.FormatUint(uint64(r.Hi), 10))
		}
	}
	return b.String()
}

// MarshalJSON stores the set as a sequence-set string rather than an array, so
// state.json stays small and hand-readable.
func (s UIDSet) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// UnmarshalJSON reads the sequence-set string form.
func (s *UIDSet) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	parsed, err := ParseUIDSet(str)
	if err != nil {
		return err
	}
	*s = *parsed
	return nil
}
