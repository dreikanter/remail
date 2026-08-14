// Package export turns a stored .eml into message.md, the original HTML, and
// attachment files.
//
// Everything here is derived: messages/ can be deleted at any time and rebuilt
// from raw/.
package export

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"

	// Legacy charset decoders, so ISO-8859, KOI8, and CJK mail is not mojibake.
	_ "github.com/emersion/go-message/charset"

	"github.com/dreikanter/remail/internal/atomicfile"
	"github.com/dreikanter/remail/internal/store"
)

// Format is the export layout version. Raising it makes sync rebuild
// messages/ from raw/.
const Format = 1

// Thresholds for spotting a placeholder plain part ("View this email in your
// browser") shipped beside the real HTML: short in absolute terms, or dwarfed
// by the HTML.
const (
	stubMaxLen = 200
	stubRatio  = 10
)

// IdentifyID returns a message's stable id, needed before the raw file can be
// placed because it is part of the path. Bodies are read lazily, so this parses
// only the header.
func IdentifyID(raw []byte) (string, error) {
	entity, err := message.Read(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
		return "", fmt.Errorf("parse message: %w", err)
	}

	hdr := mail.Header{Header: entity.Header}
	messageID, _ := hdr.MessageID()
	return store.ID(messageID, raw), nil
}

// Message writes the exported form of one message and returns its index record.
// The directory is replaced wholesale, so a re-export leaves no stale files.
func Message(root, rawPath string, raw []byte, date time.Time) (*store.Message, error) {
	entity, err := message.Read(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
		return nil, fmt.Errorf("parse message: %w", err)
	}

	hdr := mail.Header{Header: entity.Header}
	messageID, _ := hdr.MessageID()
	id := store.ID(messageID, raw)

	subject, err := hdr.Subject()
	if err != nil {
		subject = strings.TrimSpace(entity.Header.Get("Subject"))
	}

	var p parts
	var index int
	if err := walk(entity, &p, &index); err != nil {
		return nil, fmt.Errorf("read parts: %w", err)
	}

	dir := store.MessageDir(root, date, subject, id)
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}

	attachments, err := writeParts(dir, p.Attachments)
	if err != nil {
		return nil, err
	}
	// Written before the body so cid: references can point at final filenames.
	inline, err := writeParts(filepath.Join(dir, store.InlineDir), p.Inline)
	if err != nil {
		return nil, err
	}

	body, converted, err := chooseBody(p, cidMap(p.Inline, inline))
	if err != nil {
		return nil, err
	}

	if p.HTML != "" {
		if err := atomicfile.Write(filepath.Join(dir, store.HTMLFile), []byte(p.HTML), 0o600); err != nil {
			return nil, err
		}
	}

	rel, err := filepath.Rel(dir, rawPath)
	if err != nil {
		rel = rawPath
	}

	fm := &store.Frontmatter{
		ID:          id,
		Date:        date.UTC(),
		Sent:        strings.TrimSpace(entity.Header.Get("Date")),
		From:        addrList(&hdr, "From"),
		To:          addrLists(&hdr, "To"),
		Cc:          addrLists(&hdr, "Cc"),
		ReplyTo:     addrList(&hdr, "Reply-To"),
		Subject:     subject,
		MessageID:   messageID,
		InReplyTo:   firstMsgID(&hdr, "In-Reply-To"),
		References:  msgIDs(&hdr, "References"),
		ListID:      strings.TrimSpace(entity.Header.Get("List-Id")),
		Attachments: attachments,
		Inline:      inline,
		HTML:        p.HTML != "",
		Converted:   converted,
		Raw:         filepath.ToSlash(rel),
	}

	rendered, err := store.Render(fm, body)
	if err != nil {
		return nil, err
	}
	if err := atomicfile.Write(filepath.Join(dir, store.MessageFile), rendered, 0o600); err != nil {
		return nil, err
	}

	return &store.Message{Frontmatter: *fm, Dir: dir}, nil
}

// cidMap pairs each inline part's Content-ID with the name it was written
// under, which sanitizing and collision handling may have changed.
func cidMap(parts []filePart, names []string) map[string]string {
	m := make(map[string]string, len(parts))
	for i, part := range parts {
		if part.ContentID != "" && i < len(names) {
			m[part.ContentID] = path.Join(store.InlineDir, names[i])
		}
	}
	return m
}

// chooseBody picks the text that becomes message.md. It reports whether the
// result came from converted HTML.
func chooseBody(p parts, cids map[string]string) (string, bool, error) {
	plain := strings.TrimSpace(p.Plain)

	if p.HTML == "" {
		return plain, false, nil
	}

	md, err := toMarkdown(p.HTML)
	if err != nil {
		// Never lose the message to a conversion failure.
		if plain != "" {
			return plain, false, nil
		}
		return "", false, fmt.Errorf("convert HTML body: %w", err)
	}
	// A cid: URL resolves only in a mail client; point it at the saved file.
	for cid, rel := range cids {
		md = strings.ReplaceAll(md, "cid:"+cid, rel)
	}
	md = strings.TrimSpace(md)

	if plain == "" {
		return md, true, nil
	}
	if isStub(plain, md) {
		return md, true, nil
	}
	return plain, false, nil
}

func isStub(plain, md string) bool {
	if len(plain) < stubMaxLen && len(md) > len(plain) {
		return true
	}
	return len(plain)*stubRatio < len(md)
}

// toMarkdown converts an HTML body. Markdown rather than flat text, because it
// keeps link targets and structure while staying greppable.
func toMarkdown(html string) (string, error) {
	conv := converter.NewConverter(
		converter.WithPlugins(
			base.NewBasePlugin(),
			commonmark.NewCommonmarkPlugin(),
			// Renders data tables and ignores role="presentation" layout
			// tables, which is how most marketing mail is built.
			table.NewTablePlugin(),
		),
	)
	return conv.ConvertString(html)
}

func writeParts(dir string, list []filePart) ([]string, error) {
	if len(list) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}

	taken := map[string]bool{}
	var names []string
	for _, part := range list {
		name := uniqueName(taken, part.Name)
		if err := atomicfile.Write(filepath.Join(dir, name), part.Data, 0o600); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, nil
}

func addrLists(h *mail.Header, key string) []string {
	list, err := h.AddressList(key)
	if err != nil || len(list) == 0 {
		if raw := strings.TrimSpace(h.Get(key)); raw != "" {
			return []string{raw}
		}
		return nil
	}

	out := make([]string, 0, len(list))
	for _, a := range list {
		// Not Address.String(), which quotes every display name. This is read
		// by humans; the round-trippable form is in raw/.
		if a.Name != "" {
			out = append(out, fmt.Sprintf("%s <%s>", a.Name, a.Address))
		} else {
			out = append(out, a.Address)
		}
	}
	return out
}

func addrList(h *mail.Header, key string) string {
	return strings.Join(addrLists(h, key), ", ")
}

func msgIDs(h *mail.Header, key string) []string {
	ids, err := h.MsgIDList(key)
	if err != nil {
		return nil
	}
	return ids
}

func firstMsgID(h *mail.Header, key string) string {
	ids := msgIDs(h, key)
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}
