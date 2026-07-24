# Release acceptance test

The release candidate must be installable without unpublished local knowledge.

## Public RC workflow

1. Run the complete backend and frontend test suite.
2. Scan tracked files for credentials, private host paths, databases, and
   mounted user content.
3. Publish a clearly marked semantic prerelease such as `v0.1.0-rc.2` only
   after explicit approval.
4. Use a clean host directory that has never contained this project.
5. Clone only the public repository.
6. Follow the public README without copying local files from a development
   machine.
7. Run `./setup.sh`.
8. Verify login, health, ready, and persistence after restart.
9. Confirm `compose.override.yml`, `config/volumes.yaml`, `.env`, and secrets
   remain untracked and unchanged after a pull/rebuild.

Any undocumented command, copied local config, or developer-only assumption is
a release defect and must be fixed before retesting.

## Storage validation order

1. Keep the disposable RO and RW fixtures enabled.
2. Test single and multiple selection.
3. Test batch copy, move, conflict apply-to-all, cancel, rename, editor save,
   and batch delete using fixture files only.
4. Add real host storage as Docker read-only mounts and registry read-only
   volumes.
5. Verify browsing, preview, read-only editor mode, hidden-file policy, and
   unavailable-mount behavior.
6. Create a dedicated disposable write-test directory.
7. Enable write access only for that directory.
8. Test copy, move, rename, editor save, and deletion with generated test files.
9. Enable write access for a real volume only after explicit approval.

## Upgrade validation

1. Create preferences, favorites, and completed batch jobs.
2. Run `./scripts/backup.sh`.
3. Update to the next candidate with `./scripts/update.sh`.
4. Confirm login, preferences, volume registry, history, and files remain.
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
