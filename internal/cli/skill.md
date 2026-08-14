---
name: remail
description: Use when answering questions about the user's email — listing recent messages, reading one, finding who wrote about a topic, or locating a file attached to a message. Requires a local mailbox maintained by the `remail` CLI.
---

# remail

`remail` keeps a local, read-only copy of an IMAP mailbox as ordinary files.
Reading it never contacts the mail server and never marks mail as read, so
these commands are always safe to run.

## The mail directory

Every command works on one mail directory, holding one mailbox. Run from
inside it, or pass `--path <dir>` from anywhere. If you do not know where the
user keeps it, ask instead of guessing.

A directory holds the originals in `raw/` and a readable export in
`messages/`. Read the export; treat `raw/` as the archive it is.

## Commands

Run `remail <command> --help` for a command's full flags. Add `--json` to any
command for machine-readable output.

### list

Show messages, most recent first.

```sh
remail list                 # the 20 most recent
remail list -n 50
remail list -n 0            # all of them
remail list --since 7d
remail list --since 2026-08-01
```

Each row is: id, date, attachment count, sender, subject. The id is a short
hex string identifying the message.

JSON output shape:

```json
{
  "messages": [{"id": "...", "date": "...", "from": "...", "subject": "...",
                "attachments": ["..."], "dir": "/abs/path"}],
  "count": 12
}
```

### read

Print one message.

```sh
remail read a1b2c3d4
remail read a1b2           # any unambiguous id prefix works
```

Output is a YAML frontmatter block, then the body:

```
---
id: a1b2c3d4
date: 2026-08-13T09:15:02Z
from: Acme Billing <billing@acme.example>
subject: Invoice 4417
attachments:
    - invoice.pdf
raw: ../../../raw/2026-08/2026-08-13T091502Z-a1b2c3d4.eml
---

Body text follows here.
```

`date` is when the server received the message and is reliable. `sent` is the
sender's own Date header and is not. HTML-only mail is converted to Markdown,
so links and tables survive; `converted: true` marks those. `raw` points at the
untouched original, which carries the full headers when you need them.

An ambiguous prefix is an error naming the candidates, never a guess.

### files

Print absolute paths to a message's attachments, one per line.

```sh
remail files a1b2
remail files a1b2 --inline    # also images embedded in the body
```

Images embedded in the body are excluded by default so they do not bury real
attachments.

JSON output shape:

```json
{"id": "a1b2c3d4", "dir": "/abs/message/dir", "files": ["/abs/path/invoice.pdf"]}
```

### sync

Fetch mail that arrived since the last run.

```sh
remail sync
```

Needs network access and may prompt for a keychain password, so prefer reading
what is already on disk unless the user asks for fresh mail. Safe to interrupt.

JSON output shape:

```json
{"fetched": 3, "exported": 3, "rebuilt": 0, "skipped": 0}
```

### init

`remail init --account you@example.com` creates a new mail directory. The user
runs this once; you rarely need it.

### skill

`remail skill` prints this document, and `remail skill --install` writes it
into an agent's skills directory. Run the install form after upgrading remail
to refresh a stale copy of these instructions.

## Searching

The mailbox is just files, so search it directly rather than reading messages
one at a time:

```sh
rg -l 'invoice' <mail-dir>/messages
```

Each hit sits in a directory named `<date>-<subject>-<id>`, so the id you need
for `read` and `files` is already in the path.

## JSON output and errors

Every command accepts `--json` and emits a single JSON object on stdout. Plain
text and JSON are never mixed in one invocation.

On failure, `--json` mode emits the standard envelope and exits non-zero:

    { "error": { "message": "..." } }

## Do not

- Do not modify or delete anything in the mail directory. `raw/` holds the only
  copy of the originals.
- Do not parse `.eml` files yourself. `read` has already decoded the MIME
  structure, character sets, and encoded headers.
