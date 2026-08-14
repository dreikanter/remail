package export

import (
	"fmt"
	"mime"
	"path/filepath"
	"strings"
	"unicode"
)

// maxFilenameLen stays inside the 255-byte limit common to ext4, APFS, and
// HFS+ even after a dedupe suffix.
const maxFilenameLen = 100

// sanitizeFilename reduces an attachment's declared filename to a single, safe
// path component. The name is attacker-controlled, and "../../.ssh/authorized_keys"
// is a legal MIME filename parameter.
func sanitizeFilename(name string, index int, mediaType string) string {
	// Cut anything readable as a directory on either separator convention.
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = filepath.Base(name)

	name = strings.Map(func(r rune) rune {
		switch {
		case r == 0 || unicode.IsControl(r):
			return -1
		case r == '/' || r == '\\' || r == ':':
			return '-'
		}
		return r
	}, name)

	// A leading dot hides the file; "." and ".." are not names.
	name = strings.TrimLeft(name, ". ")
	name = strings.TrimRight(name, " ")

	if name == "" {
		return fallbackName(index, mediaType)
	}
	return truncateName(name, maxFilenameLen)
}

func fallbackName(index int, mediaType string) string {
	ext := ".bin"
	if exts, err := mime.ExtensionsByType(mediaType); err == nil && len(exts) > 0 {
		ext = exts[0]
	}
	return fmt.Sprintf("part-%d%s", index, ext)
}

// truncateName keeps the extension and cuts on rune boundaries.
func truncateName(name string, limit int) string {
	if len(name) <= limit {
		return name
	}

	ext := filepath.Ext(name)
	if len(ext) > 16 { // not really an extension
		ext = ""
	}
	stem := name[:len(name)-len(ext)]

	keep := limit - len(ext)
	if keep < 1 {
		return string([]rune(name)[:1]) + ext
	}
	for len(stem) > keep {
		r := []rune(stem)
		stem = string(r[:len(r)-1])
	}
	return stem + ext
}

// uniqueName resolves collisions as invoice.pdf, invoice-2.pdf, invoice-3.pdf.
func uniqueName(taken map[string]bool, name string) string {
	if !taken[strings.ToLower(name)] {
		taken[strings.ToLower(name)] = true
		return name
	}

	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d%s", stem, i, ext)
		if !taken[strings.ToLower(candidate)] {
			taken[strings.ToLower(candidate)] = true
			return candidate
		}
	}
}
