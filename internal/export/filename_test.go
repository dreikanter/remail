package export

import (
	"strings"
	"testing"
)

func TestSanitizeFilename(t *testing.T) {
	cases := []struct{ in, want string }{
		{"invoice.pdf", "invoice.pdf"},
		{"../../etc/passwd", "passwd"},
		{`..\..\windows\system32\evil.dll`, "evil.dll"},
		{"/absolute/path.txt", "path.txt"},
		{"..", "part-1.bin"},  // not a name at all
		{".", "part-1.bin"},   // same
		{".hidden", "hidden"}, // must not create a dotfile
		{"", "part-1.bin"},    // no filename declared
		{"   ", "part-1.bin"}, // whitespace only
		{"nul\x00byte.txt", "nulbyte.txt"},
		{"tab\tsep.txt", "tab\tsep.txt"}, // control chars stripped below
		{"отчёт.pdf", "отчёт.pdf"},       // non-ASCII names are fine
	}

	for _, c := range cases {
		got := sanitizeFilename(c.in, 1, "application/octet-stream")
		if c.in == "tab\tsep.txt" {
			if strings.ContainsRune(got, '\t') {
				t.Errorf("sanitizeFilename(%q) = %q, control character survived", c.in, got)
			}
			continue
		}
		if got != c.want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Whatever the input, the result must be a single harmless path component.
func TestSanitizeFilenameAlwaysSafe(t *testing.T) {
	hostile := []string{
		"../../../../../../etc/shadow",
		"..",
		"...",
		"/",
		"//",
		`C:\Windows\evil.exe`,
		"a/b/c/d",
		strings.Repeat("x", 5000) + ".pdf",
		"\x00\x01\x02",
	}

	for _, in := range hostile {
		got := sanitizeFilename(in, 3, "application/pdf")
		switch {
		case got == "" || got == "." || got == "..":
			t.Errorf("sanitizeFilename(%q) = %q, not a usable name", in, got)
		case strings.ContainsAny(got, `/\`):
			t.Errorf("sanitizeFilename(%q) = %q, contains a path separator", in, got)
		case strings.HasPrefix(got, "."):
			t.Errorf("sanitizeFilename(%q) = %q, would create a hidden file", in, got)
		case len(got) > maxFilenameLen:
			t.Errorf("sanitizeFilename(%q) = %d bytes, over the limit", in, len(got))
		}
	}
}

// Truncation must not split a multi-byte character.
func TestTruncateNameKeepsValidUTF8(t *testing.T) {
	name := strings.Repeat("ё", 200) + ".pdf"
	got := truncateName(name, maxFilenameLen)

	if len(got) > maxFilenameLen {
		t.Errorf("truncateName produced %d bytes, over the limit", len(got))
	}
	if !strings.HasSuffix(got, ".pdf") {
		t.Errorf("truncateName(%q...) = %q, lost the extension", name[:10], got)
	}
	for i, r := range got {
		if r == '\uFFFD' {
			t.Fatalf("truncateName split a rune at byte %d: %q", i, got)
		}
	}
}

func TestUniqueName(t *testing.T) {
	taken := map[string]bool{}

	want := []string{"invoice.pdf", "invoice-2.pdf", "invoice-3.pdf"}
	for _, w := range want {
		if got := uniqueName(taken, "invoice.pdf"); got != w {
			t.Errorf("uniqueName = %q, want %q", got, w)
		}
	}

	// Collisions are resolved case-insensitively, because APFS and HFS+ treat
	// Invoice.pdf and invoice.pdf as the same file.
	if got := uniqueName(taken, "INVOICE.pdf"); got == "INVOICE.pdf" {
		t.Errorf("uniqueName = %q, want a case-insensitive collision to be resolved", got)
	}
}

func TestFallbackNameUsesMediaType(t *testing.T) {
	if got := sanitizeFilename("", 7, "application/pdf"); !strings.HasSuffix(got, ".pdf") {
		t.Errorf("sanitizeFilename with no name = %q, want a .pdf extension from the media type", got)
	}
}
