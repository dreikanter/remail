package export

import (
	"io"
	"mime"
	"strings"

	"github.com/emersion/go-message"
)

// maxPartBytes caps a single decoded MIME part. Transfer encodings expand, and
// a hostile message should not be able to exhaust memory during an unattended
// sync.
const maxPartBytes = 64 << 20

type filePart struct {
	Name      string
	MediaType string
	Data      []byte
	// ContentID, without angle brackets, links an inline part to the cid:
	// reference that points at it from the HTML body.
	ContentID string
}

// parts is everything the walk found in one message.
type parts struct {
	Plain       string
	HTML        string
	Attachments []filePart
	Inline      []filePart
}

// walk descends the MIME tree collecting the first usable text bodies and every
// file-like part.
//
// It stops at message/rfc822: a forwarded message is stored as a single .eml
// attachment rather than having its bodies hoisted into the parent, which would
// make a forward look like it was written by the forwarder.
func walk(e *message.Entity, p *parts, index *int) error {
	mediaType, _, err := e.Header.ContentType()
	if err != nil {
		mediaType = "text/plain"
	}
	mediaType = strings.ToLower(mediaType)

	if mr := e.MultipartReader(); mr != nil && mediaType != "message/rfc822" {
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				// A malformed boundary should not lose the parts already found.
				return nil
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

	// A text part with no filename that is not explicitly an attachment is a
	// body, not a file.
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

	// Inline parts are the embedded images that make up an HTML layout. They
	// are kept, but separately, so a real attachment is not buried among twenty
	// spacer GIFs.
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
	// Body is already transfer-decoded and, with the charset package imported,
	// converted to UTF-8.
	b, err := io.ReadAll(io.LimitReader(e.Body, maxPartBytes))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// disposition reads Content-Disposition, falling back to a manual parse.
//
// go-message returns the entire raw header as the disposition value when a
// parameter is unquoted and contains a space (filename=my report.pdf), which
// real senders emit. Trusting that would produce a garbage filename rather than
// an obvious error, so the raw header is reparsed by hand in that case.
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

// rawParam pulls one parameter out of a header value that mime.ParseMediaType
// rejected. It accepts a quoted value, or an unquoted one running to the next
// parameter or the end of the header.
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

	// Unquoted: a following parameter is the only reliable terminator, and a
	// bare semicolon inside an unquoted filename is not legal anyway.
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
