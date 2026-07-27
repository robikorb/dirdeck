# Backup, update, and rollback

## What persists

| Data | Location | Normal update behavior |
|------|----------|------------------------|
| SQLite, sessions, preferences, transfer history | Docker volume named by `DIRDECK_DATA_VOLUME` | preserved |
| Administrator bootstrap files | `secrets/` | preserved and gitignored |
| Volume registry | `config/volumes.yaml` | preserved and gitignored |
| Local runtime variables | `.env` | preserved and gitignored |
| Host-specific bind mounts | `compose.override.yml` | preserved and gitignored |
| User media and documents | host bind mounts | never stored in app state |

**The default state volume name depends on which stack you installed**, and
getting this wrong points backup and restore at the wrong volume — Docker
creates a missing volume on demand rather than failing, so the mistake looks
like success:

| Install | Compose file | Default state volume |
|---|---|---|
| Quick start (image) | `compose.yml` | `dirdeck-data` |
| From source | `compose.build.yml` | `liquid-glass-file-manager_app-state` |

Confirm which one you have before running anything below:

```bash
docker compose config | grep -A2 '^volumes:'
```

Either way, `DIRDECK_DATA_VOLUME` overrides it. Keep the name stable after
installation.

## Version notes

### The Liquid Glass File Manager → DirDeck rename

The project was renamed. **Existing installations need no configuration
changes**; everything below is compatible on purpose.

| Surface | Before | Now | What you must do |
|---|---|---|---|
| Environment prefix | `LGFM_*` | `DIRDECK_*` | Nothing. Old names still work and log a one-line deprecation notice |
| State volume default (source stack) | `liquid-glass-file-manager_app-state` | unchanged | Nothing. The default was **not** changed for existing installs, so no database moves. The image stack introduced in 0.2.0 uses `dirdeck-data`; see the table above |
| Compose project name | directory name | unchanged | Nothing. The project name is deliberately not pinned, so your containers are not orphaned |
| Git remote | `…/liquid-glass-file-manager.git` | `…/dirdeck.git` | Nothing. GitHub redirects the old URL |
| API paths | `/api/…` | unchanged | Nothing |
| Session cookie | `lgfm_session` | unchanged | Nothing. Renaming it would sign everyone out |
| Container binary and user | `lgfm` | `dirdeck` | Nothing. Internal to the image |

Compose variable substitution cannot see the application's own alias handling,
so `compose.yml` spells out each fallback explicitly, for example
`${DIRDECK_BIND_ADDR:-${LGFM_BIND_ADDR:-127.0.0.1}}`. This matters: without it a
legacy `LGFM_BIND_ADDR=0.0.0.0` would be silently ignored and the application
would drop back to localhost, cutting off network access after an upgrade.

To migrate at your own pace, rename the variables in `.env` from `LGFM_` to
`DIRDECK_` and restart.

With a Compose install you will not see deprecation notices: Compose resolves
`.env` itself and always passes `DIRDECK_*` into the container, so the
application never sees a legacy name. The notices appear only when `LGFM_*` is
set directly in the container environment, for example with a bare `docker run`:

```text
config: LGFM_BIND_ADDR is deprecated, rename it to DIRDECK_BIND_ADDR
```

Optionally update the remote to its new name:

```bash
git remote set-url origin https://github.com/robikorb/dirdeck.git
```

The transfer staging prefix `.lgfm-partial-` and the editor temporary prefix
`.lgfm-edit-` are intentionally unchanged. Renaming them would leave partial
files from interrupted jobs unrecognised by the cleanup routine.

### Source installs must now name the build stack

`compose.yml` is now the standalone, image-based stack for operators, and the
source build moved to `compose.build.yml`. Compose auto-merges a local
`compose.override.yml` into the default file only, so an override written for
the source service name would break the standalone stack.

`setup.sh`, `scripts/update.sh`, and `scripts/backup.sh` handle this for you —
they pass `-f compose.build.yml` plus your override when one exists. If you drive
Compose by hand in a source checkout, do the same:

```bash
docker compose -f compose.build.yml -f compose.override.yml up -d --build
```

**The update that crosses this change fails once.** `scripts/update.sh` pulls the
new files while the old copy of itself is still running, so it reaches the
Compose step without knowing about `compose.build.yml` and stops with:

```text
service "file-manager" has neither an image nor a build context specified
```

Nothing is broken and the application keeps running on the previous image. Run
`./scripts/update.sh` a second time; the new script is on disk by then and
completes normally.

Your project name, containers, state volume, credentials, and volume registry
are unchanged. Nothing is migrated.

### Upgrading to 0.2.0-rc.7

No database or mount migration is required. Existing credentials, sessions,
favorites, pane state, transfer history, and host bind mounts are preserved.

`DIRDECK_MAX_UPLOAD_BYTES` is new and optional. It defaults to 1 TiB per file;
set the value in bytes in `.env` only when a different ceiling is required.

The desktop keeps the two-pane workflow. Phones now show one active pane and
use a guided source-to-destination step for Copy and Move.

## Backup

```bash
./scripts/backup.sh
```

The script:

1. briefly stops the service for a consistent SQLite snapshot;
2. archives the named application state volume;
3. separately archives `.env`, the Compose override, volume registry, and
   credentials;
4. prunes old archive pairs according to `DIRDECK_BACKUP_RETENTION` (default 10);
5. restarts the service if it was running.

Archives are written to `backups/` with UTC timestamps and are explicitly set
to mode `0600`. The configuration archive contains the administrator bootstrap
secret and must be protected like a password. The state archive contains
session digests and must receive the same protection.

The script does not back up mounted user storage. Use the storage owner's
normal backup solution for those disks and shares.

## Update

```bash
./scripts/update.sh
```

The script refuses tracked local modifications, creates a backup, runs
`git pull --ff-only`, rebuilds the container, recreates it, and waits for the
readiness endpoint.

The backend applies additive, forward-only SQLite migrations during startup.
Migration 4 adds persisted batch source paths while retaining all existing
jobs and preferences.

The security-hardening update that introduced hashed session tokens invalidates
cookies created by older builds once; sign in again after the update. Settings,
users, transfer history, and mounted files are preserved.

Never use:

```bash
docker compose down -v
```

The `-v` flag deletes the application state volume.

## Manual update

```bash
./scripts/backup.sh
git pull --ff-only
docker compose -f compose.build.yml -f compose.override.yml up -d --build --remove-orphans
docker compose -f compose.build.yml -f compose.override.yml ps
curl -fsS http://127.0.0.1:${DIRDECK_PORT:-3002}/api/ready
```

## Rollback

Keep the backup paths printed before an update.

1. Stop the application with `docker compose -f compose.build.yml -f compose.override.yml down`.
2. Check out the previous known-good Git tag.
3. Do not run an older binary against a database already migrated by a newer
   incompatible release.
4. Restore the matching pre-update state archive into a newly created empty
   state volume.
5. Restore the matching config archive.
6. Start the previous version and verify `/api/ready`.

Restoring a state archive overwrites application state and is intentionally not
automated by the normal updater. Confirm the exact volume and backup timestamp
before performing a restore.

### Exact restore procedure

Use this only with an explicitly selected backup pair. The state destination
must be a new empty Docker volume; do not extract over a running database.

```bash
docker compose -f compose.build.yml -f compose.override.yml down
docker volume create liquid-glass-file-manager_app-state-restored
docker run --rm \
  -v liquid-glass-file-manager_app-state-restored:/state \
  -v "$PWD/backups:/backup:ro" \
  alpine:3.22 \
  tar -xzf /backup/lgfm-state-YYYYMMDDTHHMMSSZ.tar.gz -C /state
```

Extract the matching configuration archive into a separate temporary directory,
inspect its `.env`, `compose.override.yml`, registry, and secrets, then copy the
approved files into the checkout. Set:

```dotenv
DIRDECK_DATA_VOLUME=liquid-glass-file-manager_app-state-restored
```

Start the matching application version, verify login and `/api/ready`, and
inspect preferences and transfer history. Keep the old volume until the restore
is accepted. Never delete either volume as part of the restore test.

## Interrupted transfers

On restart, queued jobs resume. Jobs interrupted while Running or Cancelling
are marked Failed. Partial files are never promoted to final names. Move
recovery never assumes the source was safely deleted.
