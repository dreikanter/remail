package export

import (
	"fmt"
	"mime"
	"path/filepath"
	"strings"
	"unicode"
)

// maxFilenameLen keeps names well inside the 255-byte limit common to ext4,
// APFS, and HFS+ even after a dedupe suffix is appended.
const maxFilenameLen = 100

// sanitizeFilename turns an attachment's declared filename into something safe
// to create inside a message directory.
//
// The name arrives from the network and is fully attacker-controlled: writing
// it verbatim is a path traversal bug, since "../../.ssh/authorized_keys" is a
// perfectly legal MIME filename parameter. Everything here is about making the
// result a single, harmless path component.
func sanitizeFilename(name string, index int, mediaType string) string {
	// Cut anything that could be read as a directory, on either separator
	// convention: a Windows-style "..\\..\\x" must not survive on Unix.
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

	// A leading dot would hide the file; "." and ".." are not names at all.
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

// truncateName shortens a name while keeping its extension, cutting on rune
// boundaries so a multi-byte name never ends in a broken character.
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

// uniqueName resolves collisions within one message directory by inserting a
// counter before the extension: invoice.pdf, invoice-2.pdf, invoice-3.pdf.
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
