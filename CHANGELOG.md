# Changelog

All notable changes are recorded here. The project follows semantic versioning
after the first stable release.

## Unreleased

### Fixed

- The login form no longer assumes that every installation uses `admin` as its
  administrator username.
- Folder listings now refresh when the browser regains focus, every 30 seconds
  while visible, from a per-pane refresh button, or by clicking the active
  volume again.
- Folders open with one unmodified click, editable files use an explicit pencil
  action, and range selection no longer selects browser text.
- Grid cards now use wider responsive columns, consistent card heights, and
  clamped long names instead of narrow columns with excessive wrapping.
- File and folder context menus expose open/edit, copy, move, copy-path,
  rename, and permanent-delete actions with permission-aware disabled states.

## 0.1.0-rc.2 - 2026-07-24

### Added

- Authenticated dual-pane browser with list and grid views.
- Server-enforced read-only and read-write volume registry.
- Durable single and batch copy/move jobs for up to 500 selected paths.
- Aggregate progress, cancellation, conflict prompts, and apply-to-all.
- Permanent batch deletion with explicit confirmation and safe recursive
  traversal.
- Same-folder no-replace rename.
- Monaco text/code editor with atomic saves and stale-write protection.
- Image, media, text, Markdown, JSON, source-code, and DOCX previews.
- Favorites, recent folders, persistent pane state, and keyboard shortcuts.
- Guided Docker setup, persistent state volume, backup, and update scripts.
- Docker Desktop credential-helper discovery in non-interactive macOS shells.
- Clean application-state volumes without shell profile boilerplate.
- Locale-stable backup creation on macOS and Linux hosts.
- amd64 and arm64-compatible source build.
- Original SVG wordmark, app icon, favicon, and in-app brand treatment.

### Security

- Argon2id administrator password hashing, strict sessions, CSRF, and login
  rate limiting.
- Relative-path-only API, no symlink traversal, no volume-root deletion, and
  no nested-filesystem recursive deletion.
- Credential rotation now revokes existing sessions while unchanged restarts
  preserve them.

### Database

- Migration 4 adds durable batch source paths while preserving old transfer
  rows.
