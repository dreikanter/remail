// Package export turns a stored .eml into the readable form: message.md with
// YAML frontmatter, the original HTML when there was one, and attachments as
// ordinary files.
//
// Everything this package writes is derived. The messages/ tree can be deleted
// at any time and rebuilt from raw/, which is what keeps the archive portable
// across future changes to this format.
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

	// Registers decoders for legacy charsets so ISO-8859, KOI8, and CJK mail
	// comes out as UTF-8 rather than mojibake.
	_ "github.com/emersion/go-message/charset"

	"github.com/dreikanter/remail/internal/atomicfile"
	"github.com/dreikanter/remail/internal/store"
)

// Format is the export layout version. Raising it makes sync rebuild every
// message directory from raw/ on the next run.
const Format = 1

// Thresholds for deciding between a text/plain part and the converted HTML
// when a message carries both.
//
// Senders frequently ship a placeholder plain part ("View this email in your
// browser") beside the real content. Two signals identify one: a part that is
// short in absolute terms while the HTML carries more, or a part dwarfed by the
// HTML regardless of its own size. Neither test can lose information, because
// message.html is written either way and raw/ always holds the original.
const (
	stubMaxLen = 200
	stubRatio  = 10
)

// IdentifyID returns a message's stable id without exporting it.
//
// The id is needed before the raw file can be placed, since it is part of the
// path. Only the header is parsed here; go-message reads bodies lazily, so this
// does not cost a second full pass over the message.
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
//
// The message directory is replaced wholesale rather than merged, so a re-export
// after a format change cannot leave stale attachments behind.
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
	// Inline parts are written before the body is rendered so their final
	// filenames are known and cid: references can be pointed at them.
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

// cidMap pairs each inline part's Content-ID with the filename it was actually
// written under, which may differ after sanitizing and collision handling.
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
		// A conversion failure must not lose the message; fall back to whatever
		// plain text exists.
		if plain != "" {
			return plain, false, nil
		}
		return "", false, fmt.Errorf("convert HTML body: %w", err)
	}
	// A cid: URL only means something to a mail client. Repointing it at the
	// saved file makes the image resolve in any markdown viewer or editor.
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

// toMarkdown converts an HTML body. Markdown is used rather than flat text
// because it preserves link targets, headings, and tables while staying
// greppable and readable in a plain editor.
func toMarkdown(html string) (string, error) {
	conv := converter.NewConverter(
		converter.WithPlugins(
			base.NewBasePlugin(),
			commonmark.NewCommonmarkPlugin(),
			// The table plugin ignores layout tables marked
			// role="presentation", which is how most marketing mail is built,
			// and renders only genuine data tables.
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
		// Built by hand rather than with Address.String(), which quotes every
		// display name. This value is read by humans and by `list`; the
		// round-trippable form is in raw/ if it is ever needed.
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
