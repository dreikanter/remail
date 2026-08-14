package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSlug(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Invoice 4417", "invoice-4417"},
		{"Re: [list] Something", "re-list-something"},
		{"   spaces   everywhere   ", "spaces-everywhere"},
		{"!!!", ""},
		{"", ""},
		{"../../etc/passwd", "etc-passwd"},
		{"a/b", "a-b"},
		{"Отчёт за август", "отчёт-за-август"},
	}

	for _, c := range cases {
		if got := Slug(c.in); got != c.want {
			t.Errorf("Slug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A subject is attacker-controlled, so a slug must never introduce path syntax.
func TestSlugNeverContainsPathSyntax(t *testing.T) {
	hostile := []string{
		"../../../../etc/passwd",
		`..\..\evil`,
		"/absolute",
		"..",
		".",
		strings.Repeat("long ", 200),
	}

	for _, in := range hostile {
		got := Slug(in)
		if strings.ContainsAny(got, `/\`) || strings.Contains(got, "..") {
			t.Errorf("Slug(%q) = %q, contains path syntax", in, got)
		}
		if strings.HasPrefix(got, "-") || strings.HasSuffix(got, "-") {
			t.Errorf("Slug(%q) = %q, has a stray separator", in, got)
		}
		if len(got) > maxSlugLen+4 {
			t.Errorf("Slug(%q) = %d bytes, too long", in, len(got))
		}
	}
}

func TestIDIsStableAndDerivedFromMessageID(t *testing.T) {
	a := ID("abc@example.com", []byte("one"))
	b := ID("abc@example.com", []byte("completely different bytes"))

	if a != b {
		t.Errorf("ID changed with the raw bytes (%q vs %q); it must follow Message-ID", a, b)
	}
	if len(a) != idLen {
		t.Errorf("ID length = %d, want %d", len(a), idLen)
	}

	// Distinct messages must not collide.
	if ID("other@example.com", nil) == a {
		t.Error("different Message-IDs produced the same id")
	}

	// With no Message-ID the raw bytes are the only thing left to hash.
	if ID("", []byte("one")) == ID("", []byte("two")) {
		t.Error("different bodies with no Message-ID produced the same id")
	}
}

// The raw filename carries the full receipt time so messages/ can be rebuilt
// from raw/ alone.
func TestRawPathRoundTrip(t *testing.T) {
	when := time.Date(2026, 8, 13, 9, 15, 2, 0, time.UTC)
	path := RawPath("/root", when, "a1b2c3d4")

	if want := filepath.Join("/root", RawDir, "2026-08", "2026-08-13T091502Z-a1b2c3d4.eml"); path != want {
		t.Fatalf("RawPath = %q, want %q", path, want)
	}

	gotTime, gotID, err := ParseRawName(path)
	if err != nil {
		t.Fatalf("ParseRawName: %v", err)
	}
	if !gotTime.Equal(when) {
		t.Errorf("ParseRawName time = %v, want %v", gotTime, when)
	}
	if gotID != "a1b2c3d4" {
		t.Errorf("ParseRawName id = %q, want a1b2c3d4", gotID)
	}
}

func TestParseRawNameRejectsGarbage(t *testing.T) {
	for _, in := range []string{"nonsense.eml", "2026-08-13-a1b2.eml", "no-dashes", ""} {
		if _, _, err := ParseRawName(in); err == nil {
			t.Errorf("ParseRawName(%q) succeeded, want error", in)
		}
	}
}

// A non-UTC receipt time must shard and name by its UTC value, so the same
// message always lands on the same path regardless of the local zone.
func TestRawPathNormalizesToUTC(t *testing.T) {
	zone := time.FixedZone("UTC+5", 5*3600)
	when := time.Date(2026, 8, 13, 2, 0, 0, 0, zone) // 2026-08-12T21:00:00Z

	path := RawPath("/root", when, "abcd1234")
	if !strings.Contains(path, "2026-08-12T210000Z") {
		t.Errorf("RawPath = %q, want the UTC timestamp", path)
	}
}
