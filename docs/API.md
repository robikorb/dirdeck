# HTTP API overview

Base URL in the example stack: `http://127.0.0.1:3002`. JSON request/response
bodies use UTF-8. Authenticated routes require the `lgfm_session` cookie.
Mutating routes also require CSRF (see [AUTH.md](AUTH.md)).

Errors look like: `{ "error": "message" }`.

## Health (unauthenticated)

| Method | Path | Response |
|--------|------|----------|
| `GET` | `/api/health` | `{ "status": "ok" }` |
| `GET` | `/api/ready` | `{ "status": "ready" }` or `503` `{ "status": "not_ready" }` |

## Auth

See [AUTH.md](AUTH.md).

| Method | Path | Body | Response |
|--------|------|------|----------|
| `POST` | `/api/auth/login` | `{ "username", "password" }` | `{ "username", "csrfToken", "expiresAt" }` |
| `POST` | `/api/auth/logout` | — | `{ "status": "ok" }` |
| `GET` | `/api/auth/session` | — | `{ "authenticated", ... }` |

## Volumes and browse (Phase 0 + 3)

All require authentication. Paths are **relative to the volume root** (empty or
`.` means the root). Absolute paths and `..` are rejected.

| Method | Path | Query | Response |
|--------|------|-------|----------|
| `GET` | `/api/volumes` | — | `{ "volumes": [ { id, name, readOnly, showHiddenFiles, thumbnails, available } ] }` |
| `GET` | `/api/volumes/{id}/list` | `path` | `{ "entries": [ … ], "truncated": false }` |
| `GET` | `/api/volumes/{id}/stat` | `path` | file/dir metadata object |
| `GET` | `/api/volumes/{id}/download` | `path` | octet-stream; supports HTTP `Range` |
| `GET` | `/api/volumes/{id}/thumbnail` | `path` | JPEG thumbnail (Phase 3) |
| `GET` | `/api/volumes/{id}/preview` | `path`, optional `kind` | inline media **or** JSON text/docx preview (Phase 3+) |
| `GET` | `/api/volumes/{id}/content` | `path` | Raw bounded UTF-8 content for the editor |
| `PUT` | `/api/volumes/{id}/content` | `path` | Atomically save `{ content, expectedModTime }` |
| `POST` | `/api/volumes/{id}/folder` | — | Create one directory `{ path, name }` → `201` |
| `POST` | `/api/volumes/{id}/rename` | — | Rename `{ path, newName }` without replacement |
| `DELETE` | `/api/volumes/{id}/entry` | — | Permanently delete `{ paths: [] }`; recursive for folders |

`available` is a live probe of the configured container root. When false, list
and other FS endpoints return `503` `volume unavailable`.

`truncated` is `true` when more than 10,000 entries exist (listing capped).

Symlinks appear in listings but are not traversed, downloaded, thumbnailed, or
previewed as targets.

### Thumbnail — `GET /api/volumes/{id}/thumbnail?path=`

- Requires `thumbnails: true` on the volume registry entry (`403` otherwise).
- Supported extensions: `.jpg`, `.jpeg`, `.png`, `.gif`, `.webp`.
- Returns `image/jpeg` with bounded decode (see [SECURITY.md](SECURITY.md)).
- Fail-closed status codes:
  - `413` size or pixel limit
  - `415` unsupported type
  - `429` concurrency limit
  - `504` decode timeout
  - `503` volume unavailable

### Preview — `GET /api/volumes/{id}/preview?path=&kind=`

Behavior depends on the file type (extension / `.env*` name):

#### Media (images + common audio/video)

- Inline `Content-Disposition` with a proper media `Content-Type`.
- Images: same encoded-byte ceiling as thumbnails.
- Also accepts common media extensions for ranged streaming (`.mp4`, `.webm`,
  `.mp3`, `.wav`, `.ogg`) without server-side decoding.
- Supports HTTP `Range` via `ServeContent`.
- Optional `kind=media` / `kind=image` forces this path when applicable.

#### Text-like and DOCX (JSON body)

When the path is text-like or `.docx`, the response is JSON:

```json
{
  "kind": "text|markdown|json|code|docx",
  "language": "typescript",
  "text": "…",
  "truncated": false,
  "bytesRead": 1024,
  "fileSize": 2048
}
```

| Kind | Extensions / names |
|------|--------------------|
| `text` | `.txt`, `.log`, `.csv` |
| `markdown` | `.md`, `.markdown` |
| `json` | `.json`, `.jsonc` (pretty-printed when valid; raw when invalid) |
| `code` | `.yaml`/`.yml`, `.toml`, `.xml`, `.html`/`.htm`, `.css`, `.js`/`.ts`/`.tsx`/`.jsx`, `.go`, `.py`, `.rs`, `.sh`, `.env` / `.env.*` |
| `docx` | `.docx` (plain text extracted from `word/document.xml`) |

Optional `kind=text|markdown|json|code|docx|auto` documents intent; unknown values
return `400`. Limits and fail-closed rules are in [SECURITY.md](SECURITY.md).

Headers: `X-Preview-Kind`, and `X-Preview-Truncated: true` when capped.

#### Not supported (documented future / out of scope)

- PDF rendering in the browser
- Full Office suites (`.xlsx` / `.pptx`) — skip unless a thin sheet extract is
  added later
- Arbitrary binary formats

Symlinks are not previewed as targets.

### Editor, rename, and delete

The editor accepts the text-like formats listed above except `.docx`. Reads and
writes are limited to 2 MiB, require valid UTF-8, and never follow symlinks.
Writes require a writable volume and CSRF. `expectedModTime` provides optimistic
concurrency, returning `409` if the file changed after opening. Saving stages the
new content in the same directory, flushes it, then atomically renames it over
the original.

Create-folder makes exactly one directory inside an existing folder. `name` is
validated as a single path component. An existing file **or** directory with that
name returns `409` rather than succeeding silently: the internal `Mkdir` treats an
existing directory as success because recursive copy reuses the chain, which would
make the user-facing action appear to do nothing.

Rename is limited to one name component in the existing parent folder. It
rejects separators, traversal, volume roots, symlinks, read-only volumes, and
existing destination names.

Changing only capitalisation works on case-insensitive filesystems. macOS APFS
and Windows NTFS through Docker Desktop treat `notes.txt` and `Notes.txt` as one
entry, so `RENAME_NOREPLACE` reports `EEXIST` against the file being renamed. The
server distinguishes that from a real collision by comparing device and inode,
and renames through a temporary name when they match. A genuine collision is
still `409`.

Delete accepts JSON `{ "paths": ["one", "folder/two"] }` and retains
backward compatibility with `{ "path": "one" }`. It returns `204` on success.
The batch is limited to 500 top-level paths. All paths are validated before the
first deletion, duplicates are removed, and descendants are collapsed when a
selected parent covers them. It is rejected for read-only volumes and for an
empty path, so the configured volume root cannot be deleted. Recursive folder
deletion uses directory file descriptors, never follows symbolic links, and
refuses to cross a nested filesystem mount.

## Favorites, recent, pane state (Phase 3)

Persisted in SQLite under the app data directory. Mutating routes need CSRF.

| Method | Path | CSRF | Description |
|--------|------|------|-------------|
| `GET` | `/api/favorites` | No | List favorites (newest first, max 50) |
| `POST` | `/api/favorites` | Yes | Add favorite `{ volumeId, path, label? }` → `201` |
| `DELETE` | `/api/favorites/{id}` | Yes | Remove favorite |
| `GET` | `/api/recent` | No | Recent locations (newest first, max 30) |
| `POST` | `/api/recent` | Yes | Record visit `{ volumeId, path }` |
| `DELETE` | `/api/recent` | Yes | Clear all recent locations |
| `GET` | `/api/preferences/panes` | No | `{ "paneState": null \| object }` |
| `PUT` | `/api/preferences/panes` | Yes | Persist dual-pane UI state |

### Pane state object

```json
{
  "left": { "volumeId": "fixture-ro", "path": "photos", "view": "grid",
            "sortKey": "modified", "sortDir": "desc" },
  "right": { "volumeId": "fixture-rw", "path": "", "view": "list" },
  "inspectorOpen": true,
  "activePane": "left",
  "updatedAt": "…"
}
```

Paths must be relative (no `..`, no absolute paths). Volume IDs are validated
for separators; missing volumes are ignored on restore by the UI.

`sortKey` accepts `name`, `modified`, or `size`; `sortDir` accepts `asc` or
`desc`. Both are optional — rows written before sorting existed simply omit them
— and any other value is rejected with `400`. Sorting itself is applied in the
browser; the API returns entries in filesystem order.

## Transfers (Phase 1 copy + Phase 2 move)

Durable server-side **copy** and **move** jobs. Closing the browser does not
stop the job.

### Job object (public fields)

```json
{
  "id": "…",
  "kind": "copy|move",
  "status": "queued|running|cancelling|cancelled|failed|completed|conflict",
  "sourceVolumeId": "fixture-ro",
  "sourcePath": "readme.txt",
  "sourcePaths": ["readme.txt"],
  "destVolumeId": "fixture-rw",
  "destDir": "",
  "destName": "readme.txt",
  "conflictPolicy": "prompt|skip|replace|rename",
  "applyToAll": false,
  "bytesTotal": 29,
  "bytesDone": 29,
  "filesTotal": 1,
  "filesDone": 1,
  "bytesPerSecond": 1200000,
  "bytesRemaining": 0,
  "currentPath": "readme.txt",
  "stagingName": ".lgfm-partial-<id>",
  "conflictName": null,
  "errorMessage": null,
  "freeSpaceKnown": true,
  "freeSpaceBytes": 123456789,
  "createdAt": "…",
  "updatedAt": "…",
  "startedAt": "…",
  "finishedAt": "…"
}
```

`conflict` is a wait state used when `conflictPolicy` is `prompt` and the
destination already exists. Resolve it with the conflict endpoint.

For moves, `failed` with an error mentioning source deletion means the
destination was verified complete but the source could not be removed — never
treat that as a successful move.

### Endpoints

| Method | Path | CSRF | Description |
|--------|------|------|-------------|
| `POST` | `/api/transfers` | Yes | Create a copy or move job |
| `GET` | `/api/transfers` | No | List recent jobs |
| `GET` | `/api/transfers/{id}` | No | Job snapshot |
| `POST` | `/api/transfers/{id}/cancel` | Yes | Request cancellation |
| `POST` | `/api/transfers/{id}/conflict` | Yes | Resolve a conflict pause |
| `GET` | `/api/transfers/events` | No* | SSE stream of job updates |

\*SSE requires an authenticated session cookie (EventSource cannot set CSRF
headers; it is read-only).

#### Create — `POST /api/transfers`

Single-item request:

```json
{
  "kind": "copy",
  "sourceVolumeId": "fixture-ro",
  "sourcePath": "readme.txt",
  "destVolumeId": "fixture-rw",
  "destDir": "",
  "conflictPolicy": "prompt"
}
```

Batch request:

```json
{
  "kind": "move",
  "sourceVolumeId": "fixture-rw",
  "sourcePaths": ["notes.txt", "drafts", "photo.jpg"],
  "destVolumeId": "fixture-rw",
  "destDir": "archive",
  "conflictPolicy": "prompt"
}
```

- `kind` is `copy` (default if omitted) or `move`.
- Supply either the backward-compatible `sourcePath` or `sourcePaths`.
- `sourcePaths` accepts 1 to 500 relative paths from one source volume.
- Duplicate paths are removed. Every source is validated before the job is
  persisted or any destination is written.
- `destDir` is the destination directory relative path (empty = volume root).
- Final basename defaults to the source basename unless rename conflict applies.
- Batch jobs process sources serially and report aggregate bytes/files. The
  `currentPath` field identifies the item currently being processed.
- Destination volume must not be read-only.
- Move also requires a writable source volume (source is deleted after verify).
- Exact same-path targets and destinations inside any source directory are rejected.
- Returns `201` with the job object.

Recursive source planning runs in the worker, not in this request, so a
selection containing a huge tree does not hold the HTTP connection open. As a
consequence `bytesTotal` and `filesTotal` are `0` in the `201` response and are
filled in once planning completes. Watch the event stream or re-read the job.

Planning failures — including insufficient destination free space — surface as
a `failed` job with `errorMessage`, not as an error status on this request.

### Upload — `POST /api/volumes/{id}/upload`

Streams one file into a destination directory. One request per file: the body is
a plain stream with no multipart buffering, the browser reports per-file
progress, and a cancelled request aborts exactly one file.

| Query | Meaning |
|---|---|
| `path` | Destination directory, relative to the volume root (empty = root) |
| `name` | File name only. Separators, dot segments, and NUL bytes are rejected |
| `dir` | Optional relative subdirectory inside `path`, created on demand. Used by folder upload |
| `conflict` | `error` (default), `skip`, `replace`, or `rename` |

Body: the raw file bytes. `Content-Length` is used for a free-space check before
anything is written, and the written total must match it exactly.

- Requires authentication and CSRF.
- Read-only volumes return `403`.
- `error` on an existing name returns `409` with `{"conflict": true}` and writes
  nothing; the client can then re-send with an explicit policy.
- `skip` returns `200` with `{"skipped": true}` and leaves the original.
- `rename` appends a counter before the extension and returns the chosen name.
- `replace` overwrites deliberately.
- Insufficient free space returns `507` before any bytes are written.
- Returns `201` with the resulting metadata.

Bytes land in a `.dirdeck-upload-*` staging file in the destination directory and
are promoted with a single rename, so partial data never appears under the real
name. A truncated body removes the staging file and returns `400`. Abandoned
staging files from a killed process are swept when the folder is next used as an
upload destination.

### Folder upload

The client walks a dropped directory tree and sends one request per file, each
carrying its path inside the tree as `dir`. The server creates the missing chain
with the same component-by-component validation used for a single `mkdir`, so a
crafted `dir` cannot escape the volume root, and existing directories are reused
rather than replaced.

There is no separate mkdir endpoint and no batch manifest: a partially uploaded
tree is simply a tree with fewer files in it, which is inspectable and resumable
by dropping the folder again with a conflict policy.

Resumable chunked upload is not implemented; a cancelled file restarts from the
beginning.

#### Resolve conflict — `POST /api/transfers/{id}/conflict`

```json
{
  "action": "skip|replace|rename",
  "applyToAll": false,
  "renameTo": "optional-new-name"
}
```

#### Cancel — `POST /api/transfers/{id}/cancel`

Transitions `queued`/`running`/`conflict` toward `cancelling` then `cancelled`.
Staging files are removed; no complete destination name is left behind for the
in-flight item. Items already completed earlier in a batch remain complete.
Cancelled moves never delete the in-flight source.

### Server-Sent Events — `GET /api/transfers/events`

```text
Content-Type: text/event-stream

event: transfer
data: { …job JSON… }

event: ping
data: {}
```

On reconnect, the client should:

1. Re-open the EventSource
2. `GET /api/transfers` (or per-id) to refresh authoritative state

Browser refresh does not cancel server-side work.

## Unimplemented (intentionally)

- Trash / sync / upload / archive extraction / terminal
- Arbitrary host-path selection from the browser
