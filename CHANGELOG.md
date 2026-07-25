# Changelog

All notable changes are recorded here. The project follows semantic versioning
after the first stable release.

## 0.2.0-rc.1 - 2026-07-25

### Changed

- **The project is now called DirDeck.** Existing installations upgrade without
  configuration changes: `LGFM_*` environment variables still work and log a
  deprecation notice, the Docker state volume default is unchanged, the Compose
  project name is deliberately not pinned, API paths and the session cookie name
  are untouched, and GitHub redirects the old repository URL. See
  [docs/UPGRADING.md](docs/UPGRADING.md#the-liquid-glass-file-manager-dirdeck-rename).

### Added

- **Prebuilt multi-architecture images on GHCR.** Installing no longer compiles
  Go, npm, and Monaco locally. A release workflow publishes
  `ghcr.io/robikorb/dirdeck` for `linux/amd64` and `linux/arm64` with SBOM and
  build provenance; `latest` moves only for stable tags.
- **Zero-configuration install.** `compose.yml` is self-contained: download it,
  point one bind mount at your storage, and start. No clone, no build, no config
  files. Source builds moved to `compose.build.yml`.
- **Automatic volume discovery.** Every directory bind-mounted under
  `/mnt/volumes/` is registered on startup, read-only until its name is listed
  in `DIRDECK_WRITABLE`. Mounting a volumes file still takes precedence.
- **First-run administrator.** With no credential files mounted, a 24 character
  password is generated once and printed to the container log. Only the Argon2id
  hash is stored, and a password is generated only when no administrator exists,
  so restarts never reset it.
- Configurable login throttling, session lifetime, host bind address, and backup
  retention through documented environment variables.
- Graceful HTTP and transfer-worker shutdown for container restarts.
- A restrictive same-origin Content Security Policy.

### Changed

- The 72-pixel icon rail is gone. Volumes, Favorites, and Recent are now
  collapsible sections inside a single locations sidebar, so all three are
  visible at once instead of one at a time behind a mode switch, and the panes
  get the reclaimed width.

### Fixed

- Closing the Inspector no longer strips 280 pixels from the layout. The empty
  grid track was still maximized to its full width before the panes expanded, so
  the space stayed reserved and unusable. The closed Inspector is now a slim
  rail carrying its own reopen button, in the position the panel was closed
  from, and the panes reclaim the width.
- Shutdown now closes open transfer event streams first. An open stream never
  returns to idle, so `http.Server.Shutdown` previously blocked for its whole
  deadline and left transfer workers no time to clean staging files or record
  durable status. HTTP and transfer shutdown now use separate budgets.
- List rows keep a fixed height and clip long names instead of wrapping. Wrapped
  rows grew past the height that row virtualization assumes, which desynchronised
  the scroll offset and overflowed the pane horizontally.
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
- List rows and grid cards now have dedicated selection controls, so folders
  retain one-click navigation while multi-selection no longer requires modifier
  keys. Shift-click range selection and Cmd/Ctrl selection remain available.
- Closing the Inspector now removes its responsive overlay completely instead
  of leaving an invisible layer that intercepted pane clicks.
- Copy and move now traverse complete source trees, including dotfiles and
  entries beyond the browser's display cap.
- Cross-filesystem moves and replace-on-conflict cleanup now use the same
  descriptor-based, symlink-safe and mount-boundary-safe deletion path as the
  permanent-delete action.
- Recursive transfer planning now runs in the background, applies free-space
  checks before writing, batches durable progress updates, and responds to
  cancellation around the final rename.
- SQLite now enables foreign keys, a busy timeout, WAL journaling, and NORMAL
  synchronous mode using the correct driver options.
- Thumbnail requests retry transient saturation with bounded backoff, and list
  and grid views virtualize large directories instead of mounting every entry
  in the DOM.
- Session cookie tokens are stored as SHA-256 digests, expired or revoked
  sessions are pruned on startup, and invalid usernames follow the password
  verification timing path.
- Backups created by Linux containers retain private permissions and old
  archive pairs are pruned according to the configured retention count.

### Changed

- Compose binds to localhost by default. LAN or VPN exposure now requires the
  explicit `DIRDECK_BIND_ADDR=0.0.0.0` opt-in.
- CI runs the Go race detector and starts the Compose stack to verify the ready
  endpoint after a container build.

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
