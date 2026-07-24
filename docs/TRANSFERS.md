# Transfer jobs (Phase 1 copy + Phase 2 move)

## Goals

- Copy and **move** files and directories between configured volumes as **durable
  server-side jobs** persisted in SQLite.
- Accept one to 500 selected source paths in one job, processed serially with
  aggregate progress.
- Survive browser close/refresh and container process restart without reporting
  false success.
- Never expose a partial file under its final destination name.
- Enforce read-only mounts on every write path and on every move source (moves
  require deleting the source).

## Job state machine

```text
queued → running → completed
                 → failed
                 → cancelling → cancelled
                 → conflict → (resolve) → running | cancelled | completed
```

| Status | Meaning |
|--------|---------|
| `queued` | Accepted; worker has not started bytes yet |
| `running` | Copy/move in progress |
| `cancelling` | Cancel requested; worker cleaning up |
| `cancelled` | Stopped; no complete destination for the cancelled item |
| `failed` | Error or interrupted; partials keep partial names. For moves, also used when the destination is verified but source deletion failed (never `completed` in that case) |
| `completed` | Verified and renamed to final name(s); for moves, source removed |
| `conflict` | Waiting for skip / replace / rename (when policy is `prompt`) |

Job `kind` is `copy` or `move`.

## Batch model

- A request supplies either legacy `sourcePath` or `sourcePaths`.
- Every source belongs to the same configured source volume and every item is
  copied or moved into the same destination directory.
- The backend deduplicates the selection and validates top-level paths,
  symlinks, destination relationships, and permissions before creating the
  durable job. Recursive planning runs in the worker so a large NAS tree does
  not block the HTTP request.
- A batch contains at most 500 top-level selections. Directories contribute
  their recursively planned files and bytes to aggregate totals.
- Recursive planning and copying use a transfer-only listing path. It includes
  dotfiles, ignores the UI's `showHiddenFiles` preference and 10,000-entry
  display cap, and fails instead of silently skipping an entry that cannot be
  inspected.
- Items run serially in selection order. This prevents a large browser
  selection from creating hundreds of simultaneous disk operations.
- `sourcePath` remains the first item for compatibility. `sourcePaths` is the
  authoritative selection and is persisted as JSON by database migration 4.
- `destName` is the normal destination basename for one source and a summary
  such as `3 items` for a batch.
- `bytesDone`, `bytesTotal`, `filesDone`, and `filesTotal` cover the whole job.
  `currentPath` identifies the source currently being processed.
- If a later item fails or the user cancels, earlier completed items remain in
  their final locations. The in-flight partial is cleaned up and no later item
  starts.

## Staging and verification (copy and cross-filesystem move)

1. Destination writes use a staging name: `.lgfm-partial-<job-id>` (per file,
   under the destination directory; recursive copies use unique staging names
   per file when needed).
2. Data is streamed with a **bounded buffer** (does not load whole files into
   memory).
3. After copy: close, `Sync` where supported, verify size matches source.
4. Atomic rename staging → final name only after verification.
5. On cancel or failure before the final rename: remove staging; do **not** leave
   a final-named incomplete file.

## Move semantics (Phase 2)

1. Attempt an **atomic rename** from source to destination (same or cross volume).
   Do **not** decide same- vs cross-filesystem from path strings alone.
2. Fall back to the verified copy workflow **only** for expected cross-device
   (`EXDEV`) or unsupported-operation results.
3. Cross-filesystem move order: copy → close → flush where supported → verify →
   rename staging to final → **only then** delete the source.
4. If source deletion fails after a verified destination, report `failed` with an
   explicit message (`destination verified but source could not be deleted…`).
   Never report `completed` while the source remains or while only a partial
   destination exists.
5. Read-only volumes cannot be move **sources** (delete required) or move
   **destinations** (write required).

## Conflict policies

| Policy | Behavior when destination exists |
|--------|----------------------------------|
| `prompt` | Pause job in `conflict`; UI/API resolves |
| `skip` | Leave existing destination; count as skipped (source unchanged for moves) |
| `replace` | Remove/overwrite existing, then stage + rename (or atomic move rename) |
| `rename` | Choose a non-colliding name (`file (1).ext`, …) or explicit `renameTo` |

`applyToAll` remembers the chosen action for every later conflict in the same
batch job. It does not affect another job.

## Free space

Before large transfers the engine reports best-effort free space for the
destination volume (`freeSpaceKnown` / `freeSpaceBytes`). If the filesystem
cannot provide a reliable value (common on some NAS mounts), free space is
reported as **unknown**. Unknown free space must **not** fail the job and must
**not** be treated as “sufficient space proven.”

When free space is known, the background planning phase fails a copy or
cross-volume move before writing when the planned byte total exceeds it.
Same-volume moves may use an atomic rename and do not require a second full
copy.

## Self-target protection

Before creating any copy or move job, the engine resolves the source and final
destination to internal absolute paths. It rejects an identical target and
rejects a directory destination located inside the source tree. This prevents
replace from deleting its own source and prevents recursive self-copy loops.

## Restart reconciliation

On process start:

1. Load jobs left in `running` or `cancelling`.
2. Mark them `failed` with an interrupted-transfer message (never `completed`).
3. Leave any `.lgfm-partial-*` files named as partials for operator visibility /
   retry; do not rename them to final names.
4. Re-queue only jobs that were still `queued` and safe to start.
5. Jobs in `conflict` remain `conflict` until the client resolves or cancels.
6. Interrupted **moves** never delete the source during reconcile.

## Read-only enforcement

Any create/copy/move/cancel-cleanup that would write to a volume with
`readOnly: true` is rejected server-side (`403` / job failure). Move also
rejects a read-only **source**. The Phase 0 read-only fixture (`fixture-ro`) must
never be a successful write target or a successful move source.

## Fixtures and tests

Automated tests use **temporary disposable directories**, not real user storage.
The Compose example mounts `fixtures/ro` and `fixtures/rw` for manual demos only.
Do not configure CI or unit tests against production host paths.

Tests cover aggregate batch copy, aggregate batch move, hidden files,
display-limit independence, batch apply-to-all conflicts, the 500-item API
shape, same-filesystem rename, cross-filesystem fallback (including forced
`EXDEV`), failure injection at every move stage, source-delete partial failure,
and restart reconcile.

## Progress events

The worker updates `bytesDone`, `bytesTotal`, `bytesPerSecond`,
`bytesRemaining`, `filesDone` / `filesTotal`, and `currentPath`. Updates are
batched to SQLite on a 200 ms window and broadcast over SSE
(`GET /api/transfers/events`). Clients that
reconnect must fetch job state from the REST API so they never rely on missed
events alone.

## UI expectations

- Copy and Move controls in the dual-pane strip are enabled when selection +
  destination (and for move, writable source) allow the operation.
- Cmd/Ctrl+click toggles items, Shift+click selects a range, and Cmd/Ctrl+A
  selects up to 500 visible entries.
- A batch appears as one transfer row with combined progress. Conflict
  replacement can be applied to the remaining batch.
- Sync and other unimplemented actions stay hidden or disabled.
- Transfer status should remain visible after refresh via job list + SSE
  reconnect.
