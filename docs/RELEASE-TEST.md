# Release acceptance test

The release candidate must be installable without unpublished local knowledge.

## Public RC workflow

1. Run the complete backend and frontend test suite with the race detector.
2. Scan tracked files for credentials, private host paths, databases, and
   mounted user content.
3. Publish a clearly marked semantic prerelease such as `v0.2.0-rc.1` only after
   explicit approval. A prerelease tag must never move `latest`.
   Bump the pinned default in `compose.yml` to the tag being published; CI
   checks that the pinned tag has a matching Git tag, but only a real install
   proves the published image exists.
4. Confirm the published image manifest lists both `linux/amd64` and
   `linux/arm64`, and that it is pullable anonymously.

5. **Create the GitHub Release object.** The Release workflow publishes the
   image only; pushing a tag does not produce a release page, and a tag without
   one is invisible on the Releases tab — which is where people look for what
   changed and how to install. v0.2.0-rc.3 through rc.5 were each published
   without one and had to be backfilled.

   ```bash
   gh release create v<version> \
     --title "DirDeck v<version>" \
     --notes-file <notes> \
     --prerelease --verify-tag
   ```

   The notes are the matching `CHANGELOG.md` section plus an install block
   pinned to that exact version. `--prerelease` is mandatory for a candidate: it
   keeps the tag out of the "Latest release" slot, the same guarantee withholding
   `latest` gives on the image side. When a release supersedes an earlier one
   that had a defect, say so at the top of the older notes rather than editing
   them into silence.

### Operator path — the one that matters

6. Use a clean host directory that has never contained this project.
7. Install exactly the way the README says: download `compose.yml` from the
   published ref and run `docker compose up -d`. Do not clone, do not build from
   source, and **do not edit the file** — editing the image tag to work around a
   missing one is how a broken default reaches users. Remove any locally built
   image with the same name first, or a stale local copy will satisfy the pull
   and hide the problem.
8. Confirm the first start prints an administrator password exactly once, that
   only its hash is stored, and that restarting neither reprints it nor resets
   it.
9. Confirm a directory bind-mounted under `/mnt/volumes/` is discovered without
   any registry file, and that it stays read-only until named in
   `DIRDECK_WRITABLE`.
10. Verify login, health, ready, and persistence across a container restart.
11. Confirm Compose binds to `127.0.0.1` by default and that LAN access appears
    only after explicitly setting `DIRDECK_BIND_ADDR=0.0.0.0`.

### Source path

12. Separately clone the repository and run `./setup.sh`, which builds from
    source through `compose.build.yml`.
13. Confirm `compose.override.yml`, `config/volumes.yaml`, `.env`, and secrets
    remain untracked and unchanged after a pull and rebuild.

### Upgrade path

14. From the previous release, run `./scripts/update.sh` and confirm settings,
    credentials, transfer history, and mounted files survive.
15. Confirm a pre-rename `.env` using `LGFM_*` still resolves, including
    `LGFM_BIND_ADDR`, which Compose must translate rather than silently drop.
16. Confirm `./scripts/backup.sh` archives the volume named in `.env` and
    refuses to run when that volume does not exist.

## Storage validation order

1. Keep the disposable RO and RW fixtures enabled.
2. Test single and multiple selection.
3. Test upload: drag several files onto a pane, confirm serial per-file
   progress, cancel one mid-file, and confirm no partial file and no
   `.dirdeck-upload-*` staging file survive. Repeat a name to exercise skip,
   keep both, replace, and keep-both-for-all. Confirm the upload control is
   disabled on a read-only volume and that the API rejects it with `403`. Then
   drop a nested folder and confirm the tree is recreated with its files, that
   dropping it again with keep-both or replace behaves, and that a `dir`
   containing `..` is rejected.
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
