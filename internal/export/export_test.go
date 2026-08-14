package export

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dreikanter/remail/internal/store"
)

var testDate = time.Date(2026, 8, 13, 9, 15, 2, 0, time.UTC)

// build assembles a message with CRLF line endings, as the wire format requires.
func build(lines ...string) []byte {
	return []byte(strings.Join(lines, "\r\n"))
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// exportInto runs a full export in a temp directory and returns the record.
func exportInto(t *testing.T, raw []byte) (string, *store.Message) {
	t.Helper()

	root := t.TempDir()
	id, err := IdentifyID(raw)
	if err != nil {
		t.Fatalf("IdentifyID: %v", err)
	}
	rawPath, _, err := store.WriteRaw(root, testDate, id, raw)
	if err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}

	msg, err := Message(root, rawPath, raw, testDate)
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	return root, msg
}

func newsletter() []byte {
	return build(
		`From: "Acme Billing" <billing@acme.example>`,
		`To: user@example.com`,
		`Subject: =?utf-8?B?SW52b2ljZSA0NDE3IOKAlCDQvtGC0YfRkdGC?=`,
		`Date: Thu, 13 Aug 2026 09:15:02 +0000`,
		`Message-ID: <abc123@acme.example>`,
		`In-Reply-To: <prev@acme.example>`,
		`References: <root@acme.example> <prev@acme.example>`,
		`MIME-Version: 1.0`,
		`Content-Type: multipart/mixed; boundary="MIX"`,
		``,
		`--MIX`,
		`Content-Type: multipart/alternative; boundary="ALT"`,
		``,
		`--ALT`,
		`Content-Type: text/plain; charset=utf-8`,
		``,
		`View this email in your browser`,
		`--ALT`,
		`Content-Type: text/html; charset=utf-8`,
		``,
		`<html><body><h1>Invoice 4417</h1>`,
		`<p>Please see the <a href="https://acme.example/i/4417">invoice</a>.</p>`,
		`<table role="presentation"><tr><td>layout cell</td></tr></table>`,
		`<table><tr><th>Item</th><th>Amount</th></tr><tr><td>Widget</td><td>10</td></tr></table>`,
		`</body></html>`,
		`--ALT--`,
		`--MIX`,
		`Content-Type: application/pdf`,
		`Content-Disposition: attachment; filename="../../etc/passwd.pdf"`,
		`Content-Transfer-Encoding: base64`,
		``,
		b64("PDF-BYTES"),
		`--MIX`,
		`Content-Type: image/png`,
		`Content-ID: <logo@acme.example>`,
		`Content-Disposition: inline; filename="logo.png"`,
		`Content-Transfer-Encoding: base64`,
		``,
		b64("PNG-BYTES"),
		`--MIX--`,
		``,
	)
}

func TestExportNewsletter(t *testing.T) {
	root, msg := exportInto(t, newsletter())

	if msg.MessageID != "abc123@acme.example" {
		t.Errorf("MessageID = %q", msg.MessageID)
	}
	if want := store.ID("abc123@acme.example", nil); msg.ID != want {
		t.Errorf("ID = %q, want %q (derived from Message-ID)", msg.ID, want)
	}

	// RFC 2047 encoded-word, mixed script.
	if want := "Invoice 4417 — отчёт"; msg.Subject != want {
		t.Errorf("Subject = %q, want %q", msg.Subject, want)
	}

	if msg.InReplyTo != "prev@acme.example" {
		t.Errorf("InReplyTo = %q", msg.InReplyTo)
	}
	if len(msg.References) != 2 {
		t.Errorf("References = %v, want 2 entries", msg.References)
	}

	// The plain part is a stub, so the body must come from the HTML.
	if !msg.Converted {
		t.Error("Converted = false, want the HTML body to win over the stub plain part")
	}
	if !msg.HTML {
		t.Error("HTML = false, want the original HTML preserved")
	}

	body, err := os.ReadFile(filepath.Join(msg.Dir, store.MessageFile))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)

	// Link targets must survive; losing them was the reason for choosing
	// markdown over flat text.
	if !strings.Contains(text, "https://acme.example/i/4417") {
		t.Error("converted body lost the link target")
	}
	if !strings.Contains(text, "Invoice 4417") {
		t.Error("converted body lost the heading")
	}
	// Genuine data table renders; the role="presentation" layout table does not.
	if !strings.Contains(text, "| Item") {
		t.Errorf("data table missing from body:\n%s", text)
	}

	if _, err := os.Stat(filepath.Join(msg.Dir, store.HTMLFile)); err != nil {
		t.Errorf("message.html not written: %v", err)
	}

	// The raw pointer must actually resolve from the message directory.
	rawTarget := filepath.Join(msg.Dir, filepath.FromSlash(msg.Raw))
	if _, err := os.Stat(rawTarget); err != nil {
		t.Errorf("frontmatter raw pointer does not resolve: %v", err)
	}

	_ = root
}

// A filename is attacker-controlled. It must never escape the message directory.
func TestExportAttachmentPathTraversal(t *testing.T) {
	root, msg := exportInto(t, newsletter())

	if len(msg.Attachments) != 1 {
		t.Fatalf("Attachments = %v, want exactly one", msg.Attachments)
	}
	name := msg.Attachments[0]
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		t.Fatalf("attachment name %q still contains path syntax", name)
	}
	if name != "passwd.pdf" {
		t.Errorf("attachment name = %q, want passwd.pdf", name)
	}

	data, err := os.ReadFile(filepath.Join(msg.Dir, name))
	if err != nil {
		t.Fatalf("attachment not written: %v", err)
	}
	if string(data) != "PDF-BYTES" {
		t.Errorf("attachment content = %q", data)
	}

	// Nothing may exist outside the message directory.
	if _, err := os.Stat(filepath.Join(root, "etc")); !os.IsNotExist(err) {
		t.Error("traversal escaped the message directory")
	}

	// The inline image is kept, but out of the way of real attachments.
	if len(msg.Inline) != 1 || msg.Inline[0] != "logo.png" {
		t.Fatalf("Inline = %v, want [logo.png]", msg.Inline)
	}
	if _, err := os.Stat(filepath.Join(msg.Dir, store.InlineDir, "logo.png")); err != nil {
		t.Errorf("inline image not written: %v", err)
	}
}

func TestExportPlainTextOnly(t *testing.T) {
	raw := build(
		`From: sender@example.com`,
		`Subject: Plain note`,
		`Message-ID: <plain@example.com>`,
		`Content-Type: text/plain; charset=utf-8`,
		``,
		`Line one.`,
		`Line two.`,
		``,
	)

	_, msg := exportInto(t, raw)

	if msg.Converted {
		t.Error("Converted = true for a message with no HTML part")
	}
	if msg.HTML {
		t.Error("HTML = true for a message with no HTML part")
	}

	body, err := os.ReadFile(filepath.Join(msg.Dir, store.MessageFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Line one.\r\nLine two.") &&
		!strings.Contains(string(body), "Line one.\nLine two.") {
		t.Errorf("plain body not preserved verbatim:\n%s", body)
	}
}

// A substantive plain part must win over the HTML: it is what the sender wrote,
// and no conversion can beat the original.
func TestExportPrefersSubstantivePlainPart(t *testing.T) {
	long := strings.Repeat("This is a real plain text body with actual content. ", 20)
	raw := build(
		`From: sender@example.com`,
		`Subject: Both parts`,
		`Message-ID: <both@example.com>`,
		`MIME-Version: 1.0`,
		`Content-Type: multipart/alternative; boundary="ALT"`,
		``,
		`--ALT`,
		`Content-Type: text/plain; charset=utf-8`,
		``,
		long,
		`--ALT`,
		`Content-Type: text/html; charset=utf-8`,
		``,
		`<html><body><p>`+long+`</p></body></html>`,
		`--ALT--`,
		``,
	)

	_, msg := exportInto(t, raw)

	if msg.Converted {
		t.Error("Converted = true, want the substantive plain part to be used")
	}
	if !msg.HTML {
		t.Error("HTML = false, want the HTML part still preserved on disk")
	}
}

// go-message mis-parses an unquoted filename containing a space, returning the
// whole raw header as the disposition. The fallback parser must recover a
// usable name rather than writing garbage.
func TestExportUnquotedFilenameWithSpace(t *testing.T) {
	raw := build(
		`From: sender@example.com`,
		`Subject: Report`,
		`Message-ID: <report@example.com>`,
		`MIME-Version: 1.0`,
		`Content-Type: multipart/mixed; boundary="MIX"`,
		``,
		`--MIX`,
		`Content-Type: text/plain; charset=utf-8`,
		``,
		`See attached.`,
		`--MIX`,
		`Content-Type: application/pdf`,
		`Content-Disposition: attachment; filename=my report.pdf`,
		`Content-Transfer-Encoding: base64`,
		``,
		b64("REPORT"),
		`--MIX--`,
		``,
	)

	_, msg := exportInto(t, raw)

	if len(msg.Attachments) != 1 {
		t.Fatalf("Attachments = %v, want one", msg.Attachments)
	}
	name := msg.Attachments[0]
	if !strings.HasSuffix(name, ".pdf") {
		t.Errorf("attachment name = %q, want a .pdf name recovered from the raw header", name)
	}
	if strings.Contains(name, "Content-Disposition") {
		t.Errorf("attachment name = %q, raw header leaked into the filename", name)
	}
}

// A message with no Message-ID is legal. It must still get a stable id.
func TestExportWithoutMessageID(t *testing.T) {
	raw := build(
		`From: sender@example.com`,
		`Subject: No id`,
		`Content-Type: text/plain; charset=utf-8`,
		``,
		`Body.`,
		``,
	)

	_, msg := exportInto(t, raw)

	if msg.ID == "" {
		t.Fatal("ID is empty")
	}
	if want := store.ID("", raw); msg.ID != want {
		t.Errorf("ID = %q, want %q (hash of the raw bytes)", msg.ID, want)
	}
}

// Re-exporting must replace the directory, not accumulate stale files.
func TestExportReplacesStaleFiles(t *testing.T) {
	raw := newsletter()
	root, msg := exportInto(t, raw)

	stale := filepath.Join(msg.Dir, "stale-attachment.pdf")
	if err := os.WriteFile(stale, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Message(root, filepath.Join(msg.Dir, filepath.FromSlash(msg.Raw)), raw, testDate); err != nil {
		t.Fatalf("re-export: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale file survived a re-export")
	}
}
