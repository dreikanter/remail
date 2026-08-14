# remail

A local, read-only mirror of an IMAP inbox, stored as plain files an agent or a
text editor can read directly.

```
remail sync            fetch new mail
remail list            most recent first
remail read a1b2       print a message
remail files a1b2      absolute paths to its attachments
remail skill           the agent skill for this CLI
```

## Why

Mail archives usually force a choice between a format tools can read and a
format humans can read. Maildir keeps the originals but buries attachments
inside base64 blobs and names files unreadably. A search index makes queries
fast but puts the archive inside a database that only one tool understands.

`remail` keeps both, in the filesystem:

- `raw/` holds untouched `.eml` originals. This is the source of truth. If the
  readable format ever changes, everything is regenerated from here.
- `messages/` holds the readable form: one directory per message, with a
  `message.md` and attachments as ordinary files.

`messages/` is disposable. Delete it and the next `remail sync` rebuilds it.
Nothing is stored there that cannot be regenerated.

## Install

```sh
go install github.com/dreikanter/remail/cmd/remail@latest
```

## Setup

Mail lives in one directory, and its configuration lives with it. There is
nothing in your home directory to keep in sync between machines.

```sh
mkdir ~/mail && cd ~/mail
remail init --account you@gmail.com
```

`init` prints the command to store your password. On macOS:

```sh
security add-generic-password -U -s remail -a you@gmail.com -w
```

Gmail requires an [app password](https://support.google.com/accounts/answer/185833),
not your account password, and app passwords require 2FA on the account.

Then:

```sh
remail sync
```

Every command works on the current directory. Use `--path` to point somewhere
else, so `remail --path ~/mail list` works from anywhere. One directory is one
mailbox; a second mailbox is a second directory.

## Layout

```
~/mail/
  remail.json                                  config
  raw/2026-08/2026-08-13T091502Z-5239df7a.eml  original, never modified
  messages/2026-08/2026-08-13-invoice-4417-5239df7a/
    message.md                                 headers + body
    message.html                               original HTML, if any
    invoice 4417.pdf                           attachments as real files
    inline/logo.png                            embedded images, kept apart
```

Both trees shard by month so a large mailbox never puts tens of thousands of
entries in one directory.

`message.md` starts with a YAML frontmatter block and then the body:

```yaml
---
id: 5239df7a
date: 2026-08-13T09:15:02Z
from: Acme Billing <billing@acme.example>
subject: Invoice 4417 for August
attachments:
    - invoice 4417.pdf
raw: ../../../raw/2026-08/2026-08-13T091502Z-5239df7a.eml
---
```

Every message points back at its original, so full headers are always one hop
away. Messages are addressed by `id`, or any unambiguous prefix of one.

HTML-only mail is converted to Markdown rather than flattened, so links,
headings, and tables survive. The original HTML is kept beside it.

Since it is all just files:

```sh
rg -l 'invoice' ~/mail/messages          # search everything
remail files a1b2 | xargs open           # open the attachments
```

## Agent use

Every command takes `--json` and reports failures as `{"error": {"message": ...}}`,
so an agent can use the same commands without parsing tables.

`remail skill` prints a skill document that teaches an agent to read a mailbox
with these commands. Install it for Claude Code with:

```sh
remail skill --install
```

That writes `~/.claude/skills/remail/SKILL.md`. Re-run it after upgrading to
refresh the copy; an unchanged file is left alone, and one you have edited is
reported as a conflict rather than overwritten (pass `--force` to replace it).

## Reading is read-only

Two guarantees are structural, not a matter of care:

- The mailbox is opened with `EXAMINE` and messages are fetched with `PEEK`, so
  syncing never marks your mail as read. No code path issues `STORE`, `APPEND`,
  `EXPUNGE`, or `COPY`.
- Nothing is deleted locally. Remote deletions are ignored; `raw/` only grows.

## Configuration

```json
{
  "provider": "gmail",
  "account": "you@gmail.com",
  "pass_cmd": "security find-generic-password -s remail -a you@gmail.com -w",
  "since_days": 365
}
```

That is the whole file. `provider` fills in host, port, and TLS; `gmail`,
`fastmail`, and `icloud` are known. Add `host`, `port`, or `mailbox` to override
any of it.

`pass_cmd` is any command that prints the password on stdout — the macOS
keychain, `pass`, `op read`, an encrypted file. `REMAIL_PASSWORD` overrides it,
which is what scheduled runs should use since a keychain prompt has nobody to
answer it.

`since_days` bounds the first sync only. Later runs fetch by UID, which is exact
and costs one round trip when there is nothing new.

Because `pass_cmd` is executed, `remail.json` is treated as trusted input and is
refused if it is writable by anyone but you.

## Scheduling

`sync` is a plain command; scheduling belongs to the OS. On macOS a LaunchAgent
running `remail --path ~/mail sync` every few minutes works, provided it runs in
your GUI session — a root LaunchDaemon cannot reach your keychain.

## Limitations

- One mailbox per directory, `INBOX` by default.
- Deletion and retention are not implemented. `sync` records every UID it has
  seen so a future `prune` can tell remote deletions from mail it never fetched.
- Gmail labels and thread ids are not stored. `go-imap` v2 cannot request
  Gmail's `X-GM-*` extensions. Standard `In-Reply-To` and `References` headers
  are kept instead, which is what threading actually needs.
