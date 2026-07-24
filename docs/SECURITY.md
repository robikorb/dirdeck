# Security model

## Threat model

This application exposes mounted host storage through a browser. Treat every API
request, filename, directory entry, archive, image, and media file as untrusted.
The primary risks are:

- unauthenticated access to private files;
- path traversal or symlink escape;
- cross-site requests that trigger writes;
- unsafe overwrite, move, or cancellation behavior;
- partial files appearing complete;
- resource exhaustion from large directories, files, or previews;
- secrets or absolute host paths leaking through logs and API responses.

The application is not a security boundary for storage that was never mounted
into the container. It must not receive the Docker socket, privileged mode,
host PID access, or block-device access.

## Authentication and sessions

Phase 0 implements single-user authentication before directory browsing.

- Bootstrap credentials come from operator-managed secret files.
- Store only an Argon2id password hash.
- Use random, opaque, revocable server-side sessions.
- Use HTTP-only and `SameSite=Strict` cookies.
- Use secure cookies behind HTTPS.
- Rotate the session after login.
- Rate-limit failed authentication.
- Require CSRF and same-origin checks for state-changing requests.
- Never place credentials or session tokens in URLs.

Health and readiness endpoints may remain unauthenticated, but they must reveal
only service status.

## Storage boundary

The backend loads a validated, immutable volume registry at startup. Every file
operation is expressed as:

```text
volume ID + relative path
```

The browser cannot submit an unrestricted absolute path. A shared backend
resolver must:

- reject absolute client paths and `..`;
- reject NUL bytes and invalid path encoding;
- stay beneath an already-open volume root;
- avoid check-then-open races;
- prevent symlink and magic-link traversal;
- enforce the volume's read-only capability for every mutating operation.

Phase 0 lists symlinks but does not traverse them.

## Transfer safety

Phase 0 storage-boundary tests are required before write APIs. Phase 1 adds
durable **copy** jobs; Phase 2 adds **move**. All transfers are server-side jobs
with SQLite persistence.

- Write into a destination-side staging name (`.lgfm-partial-<job-id>`).
- Use bounded buffers and context cancellation.
- Preserve partial data only under an unmistakably partial name.
- Verify expected size before final rename.
- Delete a move source only after destination verification succeeds.
- Attempt same-filesystem move via rename first; fall back only on `EXDEV` /
  unsupported-operation.
- Reconcile interrupted jobs on startup; never mark them completed.
- Treat conflict policy as explicit job input (`skip` / `replace` / `rename` /
  `prompt` with optional apply-to-all).
- Reject every write API that targets a read-only volume; reject moves from
  read-only sources.
- Never silently convert a move or copy failure into success.

Fixtures and automated tests must use disposable directories only — never real
user storage.

Trash, synchronization, uploads, arbitrary archives, and terminal access are
outside the initial implementation.

## Permanent deletion

Deletion is intentionally permanent in this release; it is never presented as
a recycle bin.

- The UI requires an explicit modal confirmation showing the number of selected
  items, sample names, and an additional recursive-folder warning.
- The API accepts at most 500 top-level relative paths on one writable volume.
- Every path is validated before the first deletion begins. An invalid,
  unavailable, read-only, traversal, or root target rejects the batch. A
  selected symlink may be unlinked, but its target is never followed.
- Duplicate paths are removed. Descendant selections are collapsed when their
  selected parent will already remove them.
- Recursive deletion operates through directory file descriptors, never
  follows symbolic links, and refuses to cross a nested filesystem boundary.
- The configured volume root cannot be deleted.
- Deletion is not transactional. A runtime storage failure after deletion
  begins can leave a partially deleted batch, so the confirmation copy must
  never claim rollback or trash recovery.

## Thumbnails and media previews (Phase 3)

Image decoding is untrusted input processing. Thumbnails and image previews
fail closed under explicit limits:

| Limit | Default | Enforcement |
|-------|---------|-------------|
| Encoded file size | 16 MiB | Reject before / during read (`413`) |
| Decoded pixels (width×height) | 40 megapixels | `DecodeConfig` then reject (`413`) |
| Decode wall time | 5 seconds | Context timeout (`504`) |
| Concurrent thumbnail jobs | 2 | Non-blocking semaphore (`429`) |
| Thumbnail max edge | 256 px | Resample then JPEG encode |

Additional rules:

- Volume registry `thumbnails: false` disables thumbnail generation (`403`).
- Only `.jpg` / `.jpeg` / `.png` / `.gif` / `.webp` are decoded.
- Symlink targets are not opened for thumbnails or previews.
- Path resolution uses the same volume-ID + relative-path resolver as browse.
- Preview streams use `Content-Disposition: inline` and HTTP `Range` for media;
  image previews share the encoded-byte ceiling.
- Responses set `X-Content-Type-Options: nosniff` and do not log file bytes.

Directory listings are capped at 10,000 entries (`truncated: true`) to limit
memory on huge NAS trees. Unavailable mounts return `503` after startup if the
root later disappears — see [OPERATIONS.md](OPERATIONS.md).

## Text and DOCX previews

Text-like and `.docx` inspector previews reuse the authenticated path resolver.
They never accept absolute host paths from the browser.

| Limit | Default | Enforcement |
|-------|---------|-------------|
| Text preview bytes returned | 512 KiB | Read cap; response `truncated: true` |
| Binary / NUL sniff | any NUL → reject | `415` `file appears to be binary` |
| DOCX zip size | 8 MiB | Reject before extract (`413`) |
| `word/document.xml` uncompressed | 2 MiB | Reject (`413`) |
| DOCX extract | zip + XML text nodes only | Corrupt / missing part → `422` |

Additional rules:

- JSON is pretty-printed when `json.Unmarshal` succeeds; invalid JSON still
  returns the truncated raw UTF-8 text.
- Markdown is returned as source text; the UI applies a sanitized lightweight
  renderer (no raw HTML execution). The API does not execute HTML.
- DOCX extraction does **not** shell out to LibreOffice or other helpers.
- PDF, XLSX, and PPTX are not decoded for preview in this release.
- Grid thumbnails remain image-only; text files keep icons.

## Rename and text editing

Rename and editor writes use the same authenticated volume-ID plus relative-path
boundary as browsing and transfers.

- Rename is limited to the existing parent directory, rejects symlinks and
  volume roots, and never replaces an existing destination.
- Editable files are limited to an allow-listed text/code set and 2 MiB.
- Saves require CSRF protection and reject invalid UTF-8.
- The client sends the last observed modification time; a changed file returns
  `409` instead of silently overwriting newer content.
- Saves write, flush, and atomically rename a same-directory staging file while
  preserving the original permission mode.
- Read-only volumes may be opened in the editor but cannot be saved or renamed.
- The Monaco editor and its language workers are bundled in the application;
  no file content is sent to a CDN or external editor service.

## Required security tests

Before write operations are enabled, automated tests must cover:

- plain and encoded `..` traversal;
- absolute paths;
- symlink escape at every path level;
- symlink replacement during an operation;
- read-only mount enforcement;
- unknown or duplicated volume IDs;
- overlapping configured roots;
- cross-volume destination validation;
- cancellation during file and directory copy;
- restart during a transfer;
- conflict policies;
- batch copy/move aggregate progress and apply-to-all behavior;
- batch deletion prevalidation, root rejection, descendant collapse, symlink
  refusal, read-only enforcement, and the 500-item limit;
- cross-filesystem move failure before and after destination completion;
- authentication, session expiry, CSRF, and login rate limiting.

Phase 3 adds coverage for thumbnail byte/pixel/concurrency limits, preference
path rejection, listing truncation, unavailable-mount responses, text preview
size/truncation and binary rejection, DOCX extract fail-closed behavior, and
JSON pretty-print. Rename/editor coverage includes CSRF, read-only enforcement,
no-replace rename, stale-write conflict, traversal rejection, byte limits, and
mode preservation.

Use disposable fixture roots only. Tests must never point at real user storage.
