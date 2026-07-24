# Backup, update, and rollback

## What persists

| Data | Location | Normal update behavior |
|------|----------|------------------------|
| SQLite, sessions, preferences, transfer history | Docker volume named by `LGFM_DATA_VOLUME` | preserved |
| Administrator bootstrap files | `secrets/` | preserved and gitignored |
| Volume registry | `config/volumes.yaml` | preserved and gitignored |
| Local runtime variables | `.env` | preserved and gitignored |
| Host-specific bind mounts | `compose.override.yml` | preserved and gitignored |
| User media and documents | host bind mounts | never stored in app state |

The default state volume name is `liquid-glass-file-manager_app-state`. Keep this
name stable after installation.

## Backup

```bash
./scripts/backup.sh
```

The script:

1. briefly stops the service for a consistent SQLite snapshot;
2. archives the named application state volume;
3. separately archives `.env`, the Compose override, volume registry, and
   credentials;
4. restarts the service if it was running.

Archives are written to `backups/` with UTC timestamps and are explicitly set
to mode `0600`. The configuration archive contains the administrator bootstrap
secret and must be protected like a password. The state archive contains
sessions and must receive the same protection.

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

Never use:

```bash
docker compose down -v
```

The `-v` flag deletes the application state volume.

## Manual update

```bash
./scripts/backup.sh
git pull --ff-only
docker compose up -d --build --remove-orphans
docker compose ps
curl -fsS http://127.0.0.1:${LGFM_PORT:-3002}/api/ready
```

## Rollback

Keep the backup paths printed before an update.

1. Stop the application with `docker compose down`.
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
docker compose down
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
LGFM_DATA_VOLUME=liquid-glass-file-manager_app-state-restored
```

Start the matching application version, verify login and `/api/ready`, and
inspect preferences and transfer history. Keep the old volume until the restore
is accepted. Never delete either volume as part of the restore test.

## Interrupted transfers

On restart, queued jobs resume. Jobs interrupted while Running or Cancelling
are marked Failed. Partial files are never promoted to final names. Move
recovery never assumes the source was safely deleted.
