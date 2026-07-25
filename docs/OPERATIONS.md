# Operator notes: mounts, large directories, and reconnects

## Volume availability

Configured roots must exist as directories at **container startup**. The YAML
registry load fails closed if a path is missing, not a directory, nested, or
overlapping.

After startup, a mount can still become unreachable (NAS disconnect, USB unplug,
host remount race). The API then:

| Endpoint | Behavior |
|----------|----------|
| `GET /api/volumes` | Sets `available: false` for unreachable roots |
| `GET /api/volumes/{id}/list` (and other FS ops) | `503` with `{"error":"volume unavailable"}` |

The UI shows an **offline** badge, surfaces the error in the pane, and offers
**Retry**. Volume availability is refreshed on visibility changes and about
every 30 seconds.

### Operator reconnect steps

1. Restore the host mount (remount SMB/NFS/USB, fix network).
2. Confirm the path is visible **inside** the container:
   `docker compose exec file-manager ls /mnt/volumes/<id>`.
3. Click **Refresh** in the Volumes side panel (or `Ctrl/Cmd+R`), or wait for
   the periodic probe.
4. If the root was missing at process start, restart the container after the
   host mount is healthy — startup validation still requires the directory.

Changing `volumes.yaml` always requires a controlled container restart.

## UI after image rebuild

`index.html` is served with `Cache-Control: no-cache`; fingerprinted files under
`/assets/` may be cached long-term. If the browser still shows a pre-rebuild UI
after `compose … up --build`, hard-refresh (**Cmd+Shift+R**) or use a private
window once.

## Large directories

Directory listings are capped at **10,000** visible entries (`truncated: true`
when more exist). Hidden files still follow the volume `showHiddenFiles`
policy. The UI shows a truncation notice. Operators should organize deep trees
or browse into subfolders rather than relying on an unbounded listing.

This browser-only cap never applies to copy or move jobs. Transfers enumerate
the complete source tree, including dotfiles, before writing. Planning is
performed asynchronously after the durable job is created, so large trees can
remain in `queued` state while totals and free-space requirements are computed.

## Slow NAS mounts

List, stat, preview, and thumbnail calls share the safe path resolver. Slow I/O
increases latency but does not bypass authentication or path boundaries.
Thumbnail generation also enforces decode **time**, **byte**, **pixel**, and
**concurrency** limits (see [SECURITY.md](SECURITY.md)). Oversized or slow
decodes fail closed (`413` / `504` / `429`) instead of starving the process.

## Transfer reconnects

Browser refresh does **not** cancel server-side copy/move jobs. The UI
re-subscribes to SSE and refreshes `GET /api/transfers` on reconnect. See
[TRANSFERS.md](TRANSFERS.md).

### Controlled container stop

`docker compose stop`, `restart`, and `./scripts/update.sh` send `SIGTERM`. The
server then runs a three-step shutdown:

1. Open transfer event streams are released. A browser tab holding an SSE
   connection would otherwise keep the HTTP server busy for its whole deadline.
2. The HTTP server stops accepting connections and drains in-flight requests
   (up to 10 seconds). Long downloads are the usual reason this takes time.
3. Transfer workers are cancelled and awaited on a **separate** 15-second
   budget, so they always get their full grace period to remove staging files
   and record durable job status even when step 2 used all of its own time.

Worst case is 25 seconds, which is why `compose.yml` sets
`stop_grace_period: 30s`. If you lower that value, lower the two server budgets
in `backend/cmd/server/main.go` to match, or Docker will `SIGKILL` the process
mid-cleanup.

A clean stop marks in-flight jobs Cancelled and removes their
`.lgfm-partial-*` files. Startup reconciliation remains the fallback after a
host crash or forced termination — see
[UPGRADING.md](UPGRADING.md#interrupted-transfers).

Expected log lines on a clean stop:

```text
shutdown requested
```

`http shutdown: context deadline exceeded` means a request did not finish
within step 2. Transfer cleanup still runs afterwards on its own budget.

## Fixtures in the default stack

`compose.yml` initially bind-mounts only disposable `fixtures/ro` and
`fixtures/rw`. Do not point automated tests at real user storage. Add real
storage only with the read-only-first process in
[STORAGE-MOUNTS.md](STORAGE-MOUNTS.md), then use disposable files before
enabling writes.

## Batch operations

One batch transfer appears as one server-side job and continues after the
browser closes. Sources run serially, so a 500-item selection does not create
500 concurrent disk jobs.

If a batch is cancelled or fails:

- the in-flight staging file is removed;
- earlier completed items remain complete;
- later items are not started;
- a move never deletes an in-flight source before destination verification.

Permanent batch deletion is different: it has full prevalidation but no
rollback. A storage failure after deletion begins can leave a partially
deleted selection. Back up important data and inspect the confirmation dialog.
