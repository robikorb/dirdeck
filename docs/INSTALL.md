# Installation

## Requirements

- Docker Engine, Docker Desktop, or OrbStack
- Docker Compose v2
- Git
- permission to bind-mount the intended host folders

The image builds for Linux amd64 and arm64.

## Guided installation

From a fresh clone:

```bash
./setup.sh
```

The script checks Docker and Compose, creates local configuration, prompts for
the administrator credentials, starts the container, and waits for
`/api/ready`.

Generated local files:

| Path | Purpose | Git behavior |
|------|---------|--------------|
| `.env` | Bind/port, state volume, runtime identity, auth and backup policy | ignored |
| `secrets/admin_username` | Bootstrap administrator name | ignored, mode 0600 |
| `secrets/admin_password` | Bootstrap administrator password | ignored, mode 0600 |
| `config/volumes.yaml` | Runtime volume registry | ignored |
| `compose.override.yml` | Host-specific Docker bind mounts | ignored |
| `backups/` | Local state and configuration archives | ignored |

The example stack initially mounts only `fixtures/ro` and `fixtures/rw`.
Fixtures are disposable and safe for first-run testing.

## Manual installation

```bash
cp .env.example .env
cp config/volumes.example.yaml config/volumes.yaml
cp compose.override.example.yml compose.override.yml
mkdir -p secrets
printf 'admin\n' > secrets/admin_username
printf 'use-a-long-unique-password\n' > secrets/admin_password
chmod 600 secrets/admin_username secrets/admin_password
docker compose up -d --build
```

Verify:

```bash
docker compose ps
curl -fsS http://127.0.0.1:3002/api/health
curl -fsS http://127.0.0.1:3002/api/ready
```

## Administrator credentials

Credentials are read from files mounted at:

```text
/run/secrets/admin_username
/run/secrets/admin_password
```

The plaintext password is not stored in SQLite. Startup hashes it with Argon2id
and stores only the hash. Changing the secret files and recreating the
container rotates the login:

```bash
printf 'new-long-unique-password\n' > secrets/admin_password
chmod 600 secrets/admin_password
docker compose up -d --force-recreate
```

Credential rotation revokes every existing browser session. An ordinary
restart with unchanged secret files preserves sessions.

Do not place real passwords in `compose.yml`, image build arguments, command
line flags, or Git.

## Zero-configuration install

The published image needs no config files. Two things are derived automatically:

**Volumes.** If no volumes file is present, DirDeck scans `/mnt/volumes/` and
registers every directory it finds there. The directory name becomes the volume
id and the sidebar label. Discovered volumes are **read-only** until listed in
`DIRDECK_WRITABLE`, so a wrong bind mount cannot cost you data. Mounting a
volumes file at `DIRDECK_VOLUMES_FILE` disables discovery and gives you full
control per volume — see [STORAGE-MOUNTS.md](STORAGE-MOUNTS.md).

**Administrator.** If no secret files are mounted, DirDeck generates a 24
character password on the very first start and prints it once:

```bash
docker compose logs dirdeck | grep -A5 "administrator account"
```

Only the Argon2id hash is stored, so the password cannot be recovered later. A
password is generated only when no administrator exists yet — restarting or
upgrading never resets it.

### If you lose the generated password

There is no self-service reset yet. Mount credential files and restart; they
take precedence and overwrite the stored hash:

```bash
printf 'admin\n'          > admin_username
printf 'your-new-password\n' > admin_password
chmod 600 admin_username admin_password
```

```yaml
    volumes:
      - ./admin_username:/run/secrets/admin_username:ro
      - ./admin_password:/run/secrets/admin_password:ro
    environment:
      DIRDECK_ADMIN_USERNAME_FILE: /run/secrets/admin_username
      DIRDECK_ADMIN_PASSWORD_FILE: /run/secrets/admin_password
```

Rotating credentials this way revokes all existing sessions.

## Environment

Edit `.env`:

| Variable | Default | Purpose |
|----------|---------|---------|
| `DIRDECK_BIND_ADDR` | `127.0.0.1` | Host bind address; use `0.0.0.0` only for approved LAN/VPN access |
| `DIRDECK_PORT` | `3002` | Host HTTP port |
| `DIRDECK_DATA_VOLUME` | `liquid-glass-file-manager_app-state` | Stable Docker state volume |
| `PUID` | `1000` | Non-root runtime user ID |
| `PGID` | `1000` | Non-root runtime group ID |
| `UMASK` | `022` | Creation mask for new files |
| `DIRDECK_SECURE_COOKIE` | `false` | Set `true` behind HTTPS |
| `DIRDECK_LOGIN_RATE_LIMIT_MAX` | `10` | Failed attempts allowed per client/window |
| `DIRDECK_LOGIN_RATE_LIMIT_SEC` | `60` | Login rate-limit window in seconds |
| `DIRDECK_SESSION_TTL_HOURS` | `12` | Browser session lifetime |
| `DIRDECK_BACKUP_RETENTION` | `10` | State/config backup pairs retained; `0` keeps all |

Linux operators should normally use the UID and GID that own the writable bind
mounts:

```bash
id -u
id -g
```

The entrypoint changes only the application runtime identity and state
directory. It never recursively changes ownership of mounted user storage.

## Persistent state

SQLite, sessions, preferences, and transfer history live in the explicitly
named Docker volume:

```text
liquid-glass-file-manager_app-state
```

Normal rebuilds, restarts, `docker compose up -d`, and `docker compose down`
preserve it. `docker compose down -v` deletes it and must not be used during a
normal update.

Mounted user files remain on their original host disks. They are not copied
into the state volume.

Keep host-specific bind mounts in `compose.override.yml`, not the tracked
`compose.yml`. Docker Compose loads the override automatically. This keeps
normal `git pull` updates clean while preserving each installation's mounts.

## Adding real storage

Read [STORAGE-MOUNTS.md](STORAGE-MOUNTS.md). The required sequence is:

1. add a Docker bind mount with `read_only: true`;
2. register the same container path with `readOnly: true`;
3. restart and verify browsing, preview, editor read-only mode, and availability;
4. use disposable test files for copy tests;
5. enable write access only for explicitly approved volumes.

Never mount `/`, `/Users`, `/home`, the Docker socket, or a block device simply
for convenience.

## HTTPS and remote access

The default configuration binds only to localhost. For an approved trusted LAN
or VPN deployment:

```dotenv
DIRDECK_BIND_ADDR=0.0.0.0
```

Restrict access with the host firewall. Prefer a VPN or Tailscale for remote
access.

When a reverse proxy terminates HTTPS:

```dotenv
DIRDECK_SECURE_COOKIE=true
```

Preserve `Host`, `Origin`, cookies, and same-origin routing. The application
uses strict same-origin CSRF checks for every state-changing request.

## Logs and shutdown

```bash
docker compose logs -f file-manager
docker compose restart file-manager
docker compose down
```

See [OPERATIONS.md](OPERATIONS.md) for mount outages and transfer recovery, and
[UPGRADING.md](UPGRADING.md) for updates and rollback.

SIGTERM/SIGINT triggers graceful HTTP shutdown and cancellation of active
transfer workers. The container waits for durable status and partial cleanup
within the shutdown grace period.
