# Changelog

## [Unreleased]

## [0.1.3] - 2026-08-25

### Changed

- Building from source now needs Go 1.27.

## [0.1.2] - 2026-08-19

### Changed

- Updated dependencies. Building from source now needs Go 1.25.8.

## [0.1.1] - 2026-08-19

### Fixed

- `sync` no longer hangs forever on a stalled connection, and Ctrl-C stops it. [#7]
- `pass_cmd` now runs in the mail directory, so relative paths in it work. [#7]
- `pass_cmd` times out after two minutes instead of waiting on a prompt forever. [#7]
- `sync` announces a rebuild instead of going quiet for minutes. [#7]

## [0.1.0] - 2026-08-14

### Added

- Initial release. `remail` keeps a local, read-only mirror of an IMAP inbox in
  a single directory and exports every message as plain text plus real
  attachment files.
- `remail init` scaffolds a mail directory: writes `remail.json`, creates the
  data directories, and prints the command to store the password in the system
  keychain.
- `remail sync` fetches new mail and exports it. Incremental by UID, so a
  repeat run costs one round trip. Never marks mail as read and never writes to
  the server.
- `remail list` shows messages most recent first, one line each, with the
  attachment count.
- `remail read <id>` prints a message as text on stdout.
- `remail files <id>` prints absolute paths to a message's attachments, one per
  line, for piping into other tools.
- `remail skill` prints a skill document that teaches an agent to read a
  mailbox with this CLI. `--install` writes it into the skills directory of
  every detected agent — Claude Code, Codex, pi, and the shared `~/.agents`
  directory — or one named with `--agent`. Each action says why it was chosen:
  an unchanged file is skipped as already up to date, and an edited one is a
  conflict unless `--force` is given.
- The skill document now tells agents how to work with PDF attachments:
  extract the text with `pdftotext`, `markitdown`, or `pdfplumber` rather than
  reading whole documents into context, and fall back to reading the PDF
  directly when the layout carries the meaning.
- Every command accepts `--json` for machine-readable output and `--path` to
  point at a mail directory other than the current one.
- Messages are stored twice: the untouched `.eml` original in `raw/`, and a
  readable `message.md` with YAML frontmatter in `messages/`. The `messages/`
  tree is disposable and rebuilt from `raw/` when the export format changes.
- HTML-only mail is converted to Markdown so links and structure survive; the
  original HTML is kept alongside it.
- Attachments are written as ordinary files next to the message. Embedded
  images go to an `inline/` subdirectory so they do not bury real attachments.
- The password is read from a configurable shell command (`pass_cmd`), so any
  secret store works — macOS Keychain, `pass`, 1Password, an encrypted file.
  `REMAIL_PASSWORD` overrides it for headless and scheduled runs.

[#7]: https://github.com/dreikanter/remail/pull/7
