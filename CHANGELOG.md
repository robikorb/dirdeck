# Changelog

All notable changes are recorded here. The project follows semantic versioning
after the first stable release.

## Unreleased

### Changed

- **The transfer strip is two labelled buttons instead of four directional
  icons.** Copy and Move now follow the active pane — the arrow points at the
  destination and the accessible name says which volume it is — and the strip
  states the reason when they are unavailable: "Backup is read-only.", "Select
  items in Media first.", "A transfer is already running." Previously seven
  unlabeled 28px icons sat between the panes, chevrons and double-arrows were
  indistinguishable at 14px, and with a read-only destination every control was
  disabled except **Delete permanently** — leaving the destructive action as the
  only thing you could press, with nothing on screen explaining why.
- **Phones now have a purpose-built single-pane workspace.** The active folder
  owns the screen, locations open in a drawer, file actions stay in a thumb
  dock, and details open as a bottom sheet. Copy and Move become a guided
  source-to-destination flow instead of stacking two unusably short panes.
  Desktop keeps the orthodox dual-pane workflow and density.
- List and grid entries now use roving keyboard focus and valid selection
  semantics. The active pane is announced, the editor is reachable with `E`,
  and rename, new-folder, and permanent-delete dialogs trap focus, close with
  Escape, and restore focus.
- Uploads now have a configurable per-file server limit
  (`DIRDECK_MAX_UPLOAD_BYTES`, 1 TiB by default). Unknown-length streams are
  bounded too, and stale staging cleanup leaves files younger than 24 hours
  alone so concurrent uploads cannot delete each other.
- Transfer and upload progress animations now use compositor transforms rather
  than repeatedly laying out width.
- Transfer controls now name the full destination folder. Transfer history
  uses a readable Copy/Move label and separate progress facts instead of a log
  prefix and one dense telemetry sentence.

- **`scripts/update.sh` now works on both stacks and needs no version number.**
  It detects whether the image stack or the source stack is running, and on the
  image stack resolves the newest published release itself, records it in
  `.env`, and pulls. `./scripts/update.sh 0.2.0-rc.6` pins an exact version.
  Stack detection lives in `scripts/lib-stack.sh` and prefers what is actually
  running over what happens to be on disk.

### Fixed

- **`scripts/update.sh` aborted on any install made from `compose.yml`.** It
  assumed the source stack unconditionally, so its first step — `backup.sh` —
  tried to archive `compose.override.yml`, `config/volumes.yaml` and the
  `secrets/` files, none of which an image install has. `tar` exited non-zero,
  `set -e` stopped the script, and the update never ran. Anyone testing from a
  Git checkout who followed the README quick start hit this. Had it continued,
  it would have rebuilt from source and switched the app to the other stack's
  state volume, making every setting and the administrator password look lost.

- **The documented upgrade path could silently downgrade the application.** The
  README told you to pass `DIRDECK_VERSION` inline. That works once, but
  `compose.yml` still contains the version it shipped with, so the next plain
  `docker compose up -d` — after a reboot, or when adding a mount — recreated
  the container on the older image with no warning. Reproduced going from
  rc.5 back to rc.4. The version now goes in `.env`, which Compose reads every
  time.
- The README's update command was pinned to the current release, so it told a
  user on rc.5 to upgrade to rc.5. It is a placeholder now.
- "Updating without losing settings" documented only `scripts/update.sh`, which
  fast-forwards a Git checkout and builds from `compose.build.yml`. Anyone who
  installed the documented way — download `compose.yml`, `docker compose up -d`
  — has neither a checkout nor that file. Both paths are now written out
  separately.
- `docs/UPGRADING.md` named `liquid-glass-file-manager_app-state` as *the*
  default state volume and stated the default had not changed. That is true of
  the source stack only; the image stack uses `dirdeck-data`. Backup and restore
  aimed at the wrong volume would not have failed loudly, because Docker creates
  a missing volume on demand.

## 0.2.0-rc.5 - 2026-07-27

A UX review pass. Three of the fixes below are regressions introduced in rc.3
and rc.4; each now has a test or a CI check so the same class of mistake cannot
land silently again.

### Added

- **Status region.** Errors, transfer completions and cancellations now appear
  in a region anchored above the panes, independent of the Inspector. Failures
  are `role="alert"` and stay until dismissed; completions are `role="polite"`
  and clear after six seconds. Previously every one of these rendered only
  inside the Inspector, which is closed by default below 1440px — so clicking
  Edit on an unreadable file, or finishing a 400 GB copy, produced no visible
  change at all. This is also the first `aria-live` region in the application:
  transfer progress and failures were entirely silent to screen readers.
- `scripts/check-theme-tokens.mjs`, run in CI. Rejects a custom property that
  resolves to itself, and requires the two light-theme token blocks to declare
  the same set of tokens with the same values.
- `scripts/check-css-order.mjs`, run in CI. Rejects a breakpoint override whose
  property is re-declared by a base rule later in the file, where source order
  silently cancels it.

### Fixed

- **Destructive colour and dialog elevation were missing from the dark theme**,
  which is the default. `--danger-text` and `--shadow-strong` were defined as
  `var(--danger-text)` and `var(--shadow-strong)` — self-referential, so they
  resolved to nothing. The context menu's "Delete permanently", the delete
  dialog's warning icon and editor errors all rendered in ordinary white, and
  the delete dialog, context menu and editor window had no shadow. Introduced in
  rc.3 by a bulk replacement that rewrote the token definitions along with their
  usages. The light theme was unaffected, which is why it went unnoticed.
- **Arrow keys and Ctrl/Cmd+A walked the unsorted entry list.** The panes render
  a sorted array but the keyboard handler in `App` indexed the raw load order,
  so sorting by size and pressing Down moved the highlight to a row elsewhere on
  screen, and Select-all selected a different 500 items than the ones displayed.
  In an application whose next action may be a permanent delete this is a
  data-loss path, not an inconvenience. Introduced in rc.4 with sortable
  columns. Arrow navigation now also scrolls the selected row into view, which
  matters under virtualization where the row may not be mounted.
- **The stacked layout below 1280px was broken by CSS source order.** The
  breakpoint set `.transfer-strip { flex-direction: row }` but the base rule
  below it set `column` at equal specificity, so the strip stayed vertical: a
  278px near-empty bar wedged between the panes, pushing the destination pane
  below the fold. It is now 50px and horizontal. All responsive blocks moved to
  the end of the stylesheet, with a comment explaining why they must stay there.
- Opening a file whose contents are not valid UTF-8 returned `500 internal
  error`. `ErrInvalidText`, `ErrTooLarge` and `ErrNotFile` were mapped only in
  `writeTransferError`, so the editor's own error path fell through to the
  internal-error default. They now return 415, 413 and 400 with a usable
  message.
- Disabled icon buttons computed `opacity: 1` and kept their hover background,
  so an unavailable action was indistinguishable from an available one.

## 0.2.0-rc.4 - 2026-07-26

### Added

- **Sortable columns.** Click Name, Date modified, or Size to sort a pane; click
  the active column to reverse it. Each pane sorts independently and the choice
  is persisted with the rest of its state. Folders always sort first, and names
  use natural order so `clip-2` precedes `clip-10`.
- Real screenshots in the README, captured from a running container.

### Changed

- Grid thumbnails fill the card instead of sitting in a 48-pixel icon tile, so
  grid view is usable for browsing photos. The height is fixed rather than an
  aspect ratio, because an aspect ratio would make the card height track the
  column width and break the grid's constant row pitch.
- Timestamps drop the seconds and use a short month name. The previous
  `toLocaleString` output spent a third of a narrow pane on the date and pushed
  filenames into an ellipsis; the date and size columns are narrower too.

### Fixed

- The Compose quick start could not work. `compose.yml` defaulted to
  `ghcr.io/robikorb/dirdeck:latest`, but `latest` is deliberately only published
  for stable releases, so the tag did not exist and a fresh install failed on the
  first command. The file now pins an exact version, the README explains how to
  move between versions, and CI refuses a pin without a matching Git tag.
- Renaming a file to change only its capitalisation returned `500 internal error`
  on macOS and Windows. Those filesystems are case-insensitive through Docker
  Desktop, so `notes.txt` and `Notes.txt` are one entry and the no-replace rename
  reported a collision against the file itself. The server now compares device and
  inode to tell that from a real collision and renames through a temporary name.

## 0.2.0-rc.3 - 2026-07-26

### Added

- **New folder.** `F7` or the toolbar button creates a directory in the folder a
  pane is showing; an empty folder offers it directly. A name already taken by a
  file or folder is refused rather than silently succeeding.

### Fixed

- The light theme reached dialogs, the context menu, previews, and the login card
  as dark surfaces. Only the main panels had been converted to tokens; 47 further
  hard-coded colours were still dark literals, and the explicit
  `data-theme='light'` block was missing every token added later, so choosing
  light left dark text on dark dialogs. Both light blocks are now generated from
  one source and verified identical.

## 0.2.0-rc.2 - 2026-07-26

### Added

- **Browser upload.** Drag files onto a pane or use the toolbar button. Uploads
  stream straight to disk with no server-side buffering, run one at a time with
  per-file progress and cancellation, and offer skip, keep both, replace, and
  keep-both-for-all on a name conflict. Bytes land in a staging file and are
  promoted with a single rename, so a dropped connection never leaves partial
  data under the real name. Folder upload is not included.

### Added

- **Light theme.** Both themes are one token set: the system preference applies
  by default and the sidebar footer overrides it. Every surface reads tokens, so
  the second theme is a palette rather than a second stylesheet.
- **Density control.** List rows switch between compact and comfortable. Row
  height is owned by one constant in `App.tsx` that feeds both the stylesheet and
  the virtualization arithmetic, so density cannot desynchronise them.
- **Folder upload.** Drop a folder and its whole tree is recreated at the
  destination, or pick one with the new **Folder** button. The client walks the
  tree and sends one request per file with its relative path; the server creates
  each directory level through the same validated `mkdir` used everywhere else.
  Large trees report a running count rather than thousands of progress rows.

### Fixed

- Accent-coloured text failed WCAG AA. `#3b82f6` measures 4.30:1 on the stronger
  panel surface, below the 4.5:1 threshold, so accent text now uses a separate
  lighter token. Every text token on both panel surfaces and the filled button
  now measure 5.17:1 or better in both themes.
- Upload is a filled primary button rather than one outlined control among
  several, and Folder reads as its secondary sibling.
- An upload queue can now be stopped. **Stop all** halts the whole batch; the
  per-file cancel only ever skipped the file in flight, so stopping a dropped
  `node_modules` meant clicking Cancel once per file for tens of thousands of
  files. Dropping more than 25 files also asks for confirmation first, showing
  the count and total size.
- A cancelled upload is reported as cancelled rather than failed.
- The upload control is a labelled **Upload** button instead of an unlabelled
  arrow among four other unlabelled icons, and an empty folder now shows its
  drop target instead of an empty rectangle.
- Dropped folders are detected with `webkitGetAsEntry` and reported as skipped.
  The previous size-and-type guess also discarded genuinely empty files, which
  the server accepts.
- `scripts/backup.sh` no longer archives the wrong volume. It reads
  `DIRDECK_DATA_VOLUME`, falls back to the pre-rename `LGFM_DATA_VOLUME`, and
  refuses to run when the named volume does not exist. Docker creates a missing
  volume on demand, so a name that did not resolve previously produced an empty
  archive and reported success. Backup retention now matches archives written
  under either naming scheme.

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
