package state

import (
	"encoding/json"
	"testing"
)

func TestParseUIDSetRoundTrip(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"1", "1"},
		{"1:5", "1:5"},
		{"1:9000,9002:9500", "1:9000,9002:9500"},
		{"5:1", "1:5"},         // reversed range normalizes
		{"3,1,2", "1:3"},       // out of order and adjacent merges
		{"1:5,4:9", "1:9"},     // overlapping merges
		{"1:5,6:9", "1:9"},     // adjacent merges
		{"1:5,7:9", "1:5,7:9"}, // a real gap survives
	}

	for _, c := range cases {
		set, err := ParseUIDSet(c.in)
		if err != nil {
			t.Fatalf("ParseUIDSet(%q): %v", c.in, err)
		}
		if got := set.String(); got != c.want {
			t.Errorf("ParseUIDSet(%q).String() = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseUIDSetRejectsGarbage(t *testing.T) {
	for _, in := range []string{"x", "1:x", "0", "0:5", "-1", "1:"} {
		if _, err := ParseUIDSet(in); err == nil {
			t.Errorf("ParseUIDSet(%q) succeeded, want error", in)
		}
	}
}

func TestAddMergesIntoSingleRange(t *testing.T) {
	var s UIDSet
	for i := uint32(1); i <= 1000; i++ {
		s.Add(i)
	}

	if got := s.String(); got != "1:1000" {
		t.Fatalf("String() = %q, want a single merged range", got)
	}
	if got := s.Len(); got != 1000 {
		t.Errorf("Len() = %d, want 1000", got)
	}
	if got := s.Max(); got != 1000 {
		t.Errorf("Max() = %d, want 1000", got)
	}
}

// Adding UIDs out of order must produce the same set as adding them in order;
// a sync that retries can revisit UIDs in any sequence.
func TestAddOutOfOrder(t *testing.T) {
	var s UIDSet
	for _, uid := range []uint32{9, 1, 5, 2, 8, 3, 7, 4, 6} {
		s.Add(uid)
	}
	if got := s.String(); got != "1:9" {
		t.Fatalf("String() = %q, want 1:9", got)
	}
}

func TestContains(t *testing.T) {
	s, err := ParseUIDSet("1:5,10:12,100")
	if err != nil {
		t.Fatal(err)
	}

	in := []uint32{1, 3, 5, 10, 11, 12, 100}
	out := []uint32{0, 6, 9, 13, 99, 101}

	for _, uid := range in {
		if !s.Contains(uid) {
			t.Errorf("Contains(%d) = false, want true", uid)
		}
	}
	for _, uid := range out {
		if s.Contains(uid) {
			t.Errorf("Contains(%d) = true, want false", uid)
		}
	}
}

func TestAddRangeIgnoresInvalid(t *testing.T) {
	var s UIDSet
	s.AddRange(0, 5) // UID 0 is not valid
	s.AddRange(9, 3) // reversed, already normalized by callers
	if got := s.String(); got != "" {
		t.Errorf("String() = %q, want empty", got)
	}
}

// The set must not wrap around at the top of the UID space.
func TestAddNearMaxUID(t *testing.T) {
	var s UIDSet
	max := ^uint32(0)
	s.Add(max)
	s.Add(max - 1)

	if !s.Contains(max) || !s.Contains(max-1) {
		t.Fatalf("lost UIDs near the top of the space: %q", s.String())
	}
	if got := s.Len(); got != 2 {
		t.Errorf("Len() = %d, want 2", got)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	set, err := ParseUIDSet("1:9000,9002:9500")
	if err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	// The compact form is the point: a JSON array of the same UIDs would be
	// tens of kilobytes.
	if len(data) > 32 {
		t.Errorf("marshalled to %d bytes, expected a compact sequence set: %s", len(data), data)
	}

	var back UIDSet
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.String() != set.String() {
		t.Errorf("round trip changed the set: %q -> %q", set.String(), back.String())
	}
	if back.Len() != 9499 {
		t.Errorf("Len() = %d, want 9499", back.Len())
	}
}
