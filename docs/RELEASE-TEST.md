# Release acceptance test

The release candidate must be installable without unpublished local knowledge.

## Public RC workflow

1. Run the complete backend and frontend test suite with the race detector.
2. Scan tracked files for credentials, private host paths, databases, and
   mounted user content.
3. Publish a clearly marked semantic prerelease such as `v0.2.0-rc.1` only after
   explicit approval. A prerelease tag must never move `latest`.
4. Confirm the published image manifest lists both `linux/amd64` and
   `linux/arm64`, and that it is pullable anonymously.

### Operator path — the one that matters

5. Use a clean host directory that has never contained this project.
6. Install exactly the way the README says: download `compose.yml` from the
   published ref and run `docker compose up -d`. Do not clone and do not build
   from source; the point is to exercise the published image.
7. Confirm the first start prints an administrator password exactly once, that
   only its hash is stored, and that restarting neither reprints it nor resets
   it.
8. Confirm a directory bind-mounted under `/mnt/volumes/` is discovered without
   any registry file, and that it stays read-only until named in
   `DIRDECK_WRITABLE`.
9. Verify login, health, ready, and persistence across a container restart.
10. Confirm Compose binds to `127.0.0.1` by default and that LAN access appears
    only after explicitly setting `DIRDECK_BIND_ADDR=0.0.0.0`.

### Source path

11. Separately clone the repository and run `./setup.sh`, which builds from
    source through `compose.build.yml`.
12. Confirm `compose.override.yml`, `config/volumes.yaml`, `.env`, and secrets
    remain untracked and unchanged after a pull and rebuild.

### Upgrade path

13. From the previous release, run `./scripts/update.sh` and confirm settings,
    credentials, transfer history, and mounted files survive.
14. Confirm a pre-rename `.env` using `LGFM_*` still resolves, including
    `LGFM_BIND_ADDR`, which Compose must translate rather than silently drop.
15. Confirm `./scripts/backup.sh` archives the volume named in `.env` and
    refuses to run when that volume does not exist.

## Storage validation order

1. Keep the disposable RO and RW fixtures enabled.
2. Test single and multiple selection.
3. Test upload: drag several files onto a pane, confirm serial per-file
   progress, cancel one mid-file, and confirm no partial file and no
   `.dirdeck-upload-*` staging file survive. Repeat a name to exercise skip,
   keep both, replace, and keep-both-for-all. Confirm the upload control is
   disabled on a read-only volume and that the API rejects it with `403`.
4. Test batch copy, move, conflict apply-to-all, cancel, rename, editor save,
   and batch delete using fixture files only.
5. Copy and cross-filesystem-move a fixture tree containing dotfiles and more
   entries than a deliberately lowered display-list limit; verify every source
   reaches the destination before the move source disappears.
6. Add real host storage as Docker read-only mounts and registry read-only
   volumes.
7. Verify browsing, preview, read-only editor mode, hidden-file policy, and
   unavailable-mount behavior.
8. Create a dedicated disposable write-test directory.
8. Enable write access only for that directory.
9. Test copy, move, rename, editor save, and deletion with generated test files.
10. Enable write access for a real volume only after explicit approval.

## Upgrade validation

1. Create preferences, favorites, and completed batch jobs.
2. Run `./scripts/backup.sh`.
3. Update to the next candidate with `./scripts/update.sh`.
4. Confirm login, preferences, volume registry, history, and files remain.
   A one-time login is expected when upgrading from plaintext session storage
   to session-token digests.
5. Confirm the database schema migration version.
6. Exercise the documented rollback on disposable state, never on the primary
   installation first.

## Release completion

Promote the candidate to `v0.1.0` only when:

- clean installation succeeds;
- fixture operations succeed;
- read-only real mounts remain read-only;
- update preserves state;
- backup archives are usable;
- documentation contains every required step;
- no secret or local absolute path is tracked.

The repository URL and private vulnerability-reporting channel must point to
the live public project before tagging the candidate. Release image coordinates
remain optional until automated container publication is introduced.
