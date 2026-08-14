# Changelog

## [Unreleased]

### Added

- Initial release. `remail` keeps a local, read-only mirror of an IMAP inbox in
  a single directory and exports every message as plain text plus real
  attachment files.
- `remail init` scaffolds a mail directory: writes `remail.json`, creates the
  data directories, and prints the one command needed to store the password in
  the system keychain.
- `remail sync` fetches new mail and exports it. Incremental by UID, so a repeat
  run costs one round trip when there is nothing new. Never marks mail as read
  and never writes to the server.
- `remail list` shows messages most recent first, one line each, with the
  attachment count.
- `remail read <id>` prints a message as text on stdout.
- `remail files <id>` prints absolute paths to a message's attachments, one per
  line, for piping into other tools.
- Every command accepts `--json` for machine-readable output and `--path` to
  point at a mail directory other than the current one.
- Messages are stored twice: the untouched original in `raw/` as an `.eml` file,
  and a readable `message.md` with YAML frontmatter in `messages/`. The
  `messages/` tree is disposable and rebuilt from `raw/` whenever the export
  format changes.
- HTML-only mail is converted to Markdown so links and structure survive; the
  original HTML is kept alongside it.
- Attachments are written as ordinary files next to the message. Inline images
  are separated into an `inline/` subdirectory so they do not bury real
  attachments.
- The password is read from a configurable shell command (`pass_cmd`), so any
  secret store works — macOS Keychain, `pass`, 1Password, or an encrypted file.
  `REMAIL_PASSWORD` overrides it for headless and scheduled runs.
