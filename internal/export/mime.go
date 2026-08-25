package export

import (
	"errors"
	"io"
	"mime"
	"strings"

	"github.com/emersion/go-message"
)

// maxPartBytes caps a decoded MIME part, so a hostile message cannot exhaust
// memory during an unattended sync.
const maxPartBytes = 64 << 20

type filePart struct {
	Name      string
	MediaType string
	Data      []byte
	// ContentID, without angle brackets, matches the cid: reference in the body.
	ContentID string
}

// parts is everything the walk found in one message.
type parts struct {
	Plain       string
	HTML        string
	Attachments []filePart
	Inline      []filePart
}

// walk collects the first usable text bodies and every file-like part. It stops
// at message/rfc822, so a forwarded message stays one attachment instead of
// having its body hoisted into the parent.
func walk(e *message.Entity, p *parts, index *int) error {
	mediaType, _, err := e.Header.ContentType()
	if err != nil {
		mediaType = "text/plain"
	}
	mediaType = strings.ToLower(mediaType)

	if mr := e.MultipartReader(); mr != nil && mediaType != "message/rfc822" {
		for {
			part, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return nil // keep the parts found before a malformed boundary
			}
			if err := walk(part, p, index); err != nil {
				return err
			}
		}
	}

	return leaf(e, mediaType, p, index)
}

func leaf(e *message.Entity, mediaType string, p *parts, index *int) error {
	*index++

	disp, dispParams := disposition(&e.Header)
	_, ctParams, _ := e.Header.ContentType()

	filename := dispParams["filename"]
	if filename == "" {
		filename = ctParams["name"]
	}

	// An unnamed text part that is not marked as an attachment is a body.
	isBody := filename == "" && disp != "attachment"
	if isBody {
		switch mediaType {
		case "text/plain":
			if p.Plain == "" {
				body, err := readPart(e)
				if err != nil {
					return err
				}
				p.Plain = body
			}
			return nil
		case "text/html":
			if p.HTML == "" {
				body, err := readPart(e)
				if err != nil {
					return err
				}
				p.HTML = body
			}
			return nil
		}
	}

	data, err := io.ReadAll(io.LimitReader(e.Body, maxPartBytes))
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}

	part := filePart{
		Name:      sanitizeFilename(filename, *index, mediaType),
		MediaType: mediaType,
		Data:      data,
		ContentID: strings.Trim(strings.TrimSpace(e.Header.Get("Content-Id")), "<>"),
	}

	// Kept separately so a real attachment is not buried among spacer GIFs.
	if isInline(disp, mediaType, &e.Header) {
		p.Inline = append(p.Inline, part)
	} else {
		p.Attachments = append(p.Attachments, part)
	}
	return nil
}

func isInline(disp, mediaType string, h *message.Header) bool {
	if disp == "attachment" {
		return false
	}
	hasCID := strings.TrimSpace(h.Get("Content-Id")) != ""
	return hasCID || (disp == "inline" && strings.HasPrefix(mediaType, "image/"))
}

func readPart(e *message.Entity) (string, error) {
	// Already transfer-decoded and, via the charset import, converted to UTF-8.
	b, err := io.ReadAll(io.LimitReader(e.Body, maxPartBytes))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// disposition reads Content-Disposition, falling back to a manual parse.
//
// go-message returns the whole raw header as the disposition value when a
// parameter is unquoted and contains a space (filename=my report.pdf), which
// real senders emit. Trusting it would yield a garbage filename, not an error.
func disposition(h *message.Header) (string, map[string]string) {
	disp, params, err := h.ContentDisposition()
	if err == nil {
		return strings.ToLower(strings.TrimSpace(disp)), params
	}

	raw := h.Get("Content-Disposition")
	if raw == "" {
		return "", nil
	}

	value, rest, _ := strings.Cut(raw, ";")
	out := map[string]string{}
	if name := rawParam(rest, "filename"); name != "" {
		out["filename"] = name
	}
	return strings.ToLower(strings.TrimSpace(value)), out
}

// rawParam pulls one parameter out of a header mime.ParseMediaType rejected,
// quoted or running to the next parameter.
func rawParam(s, key string) string {
	lower := strings.ToLower(s)
	i := strings.Index(lower, key+"=")
	if i < 0 {
		return ""
	}
	v := strings.TrimSpace(s[i+len(key)+1:])

	if strings.HasPrefix(v, `"`) {
		if end := strings.Index(v[1:], `"`); end >= 0 {
			return decodeWord(v[1 : 1+end])
		}
		return decodeWord(strings.Trim(v, `"`))
	}

	// A following parameter is the only reliable terminator.
	if end := strings.Index(v, ";"); end >= 0 {
		v = v[:end]
	}
	return decodeWord(strings.TrimSpace(v))
}

func decodeWord(s string) string {
	decoded, err := new(mime.WordDecoder).DecodeHeader(s)
	if err != nil {
		return s
	}
	return decoded
}
