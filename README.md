<p align="center">
  <img src="frontend/public/logo.svg" alt="DirDeck" width="460">
</p>

# DirDeck

[![CI](https://github.com/robikorb/dirdeck/actions/workflows/ci.yml/badge.svg)](https://github.com/robikorb/dirdeck/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-66bfff.svg)](LICENSE)

**A fast, self-hosted dual-pane file manager for your homelab.**

Total Commander-style two-pane workflow in the browser, packaged as one Docker
container. Mount only the folders and disks you want it to see, then browse,
copy, move, rename, edit, preview, and delete files across them.

The backend is written in Go, the frontend uses React and TypeScript, and the
complete UI is embedded into one container image. Application state is stored
in SQLite inside a persistent Docker volume.

## Features

- Authenticated single-user web interface with Argon2id password hashing.
- Dual-pane list and grid browsing.
- Dedicated item selectors plus Cmd/Ctrl+click, Shift+click, and Cmd/Ctrl+A
  multiple selection.
- Batch copy and move jobs with combined byte and file progress.
- Complete transfer traversal that is independent of hidden-file display
  preferences and UI listing limits.
- Skip, replace, rename, and apply-to-all conflict handling.
- Permanent batch deletion with an explicit confirmation dialog.
- Safe single-item rename without overwriting an existing destination.
- Monaco-based editor for bounded UTF-8 text and code files.
- Image thumbnails, media preview, text preview, favorites, and recent folders.
- Server-enforced read-only volumes.
- Non-root container process with configurable PUID, PGID, and umask.
- Persistent jobs and automatic forward-only SQLite migrations.
- Backup and update scripts that preserve settings.

## Quick start

Requirements: Docker Engine or Docker Desktop with Compose v2. Nothing else —
no Git, no Go, no Node, no build.

```bash
mkdir dirdeck && cd dirdeck
curl -O https://raw.githubusercontent.com/robikorb/dirdeck/main/compose.yml
```

Open `compose.yml` and point the mount at your storage:

```yaml
    volumes:
      - /srv/media:/mnt/volumes/media      # <- your folder or disk
      - dirdeck-data:/var/lib/file-manager
```

Then start it and read the generated password:

```bash
docker compose up -d
docker compose logs dirdeck | grep -A5 "administrator account"
```

```text
│ DirDeck created its first administrator account.         │
│   username: admin                                        │
│   password: iDKKmue2kuGRvjUYP5eUxnqx                     │
```

Open [http://127.0.0.1:3002](http://127.0.0.1:3002) and sign in. The password is
printed once and only its Argon2id hash is stored, so save it now.

Every directory you mount under `/mnt/volumes/` shows up automatically — the
name after `/mnt/volumes/` is what you see in the sidebar. No config file, no
restart dance.

## Making a volume writable

**Every volume starts read-only.** Browse it first and confirm you are looking
at the right disk. Only then opt in:

```yaml
    environment:
      DIRDECK_WRITABLE: "media"      # comma-separated, or "*" for all
```

```bash
docker compose up -d
```

This is deliberate. DirDeck can permanently delete files, and a typo in a bind
mount should cost you nothing.

## Building from source

Contributors and anyone who prefers not to pull an image:

```bash
git clone https://github.com/robikorb/dirdeck.git
cd dirdeck
./setup.sh
```

That compiles the Go binary and the frontend, prompts for credentials, and uses
`compose.build.yml` with disposable fixtures. See
[CONTRIBUTING.md](CONTRIBUTING.md).

## Updating without losing settings

```bash
./scripts/update.sh
```

The updater stops the app briefly, creates a backup, fast-forwards the Git
checkout, rebuilds the image, recreates the container, and verifies readiness.
The stable Docker volume `liquid-glass-file-manager_app-state`, local `.env`,
credentials, and volume configuration are preserved.

Never run `docker compose down -v` unless you intentionally want to delete the
application database. Mounted user files are never stored in the application
state volume. See [docs/UPGRADING.md](docs/UPGRADING.md).

## Safety model

- Browser requests contain a configured volume ID and relative path, never an
  unrestricted host path.
- Absolute paths, traversal, nested roots, and symlink traversal are rejected.
- Read-only capability is enforced by the backend for every mutation.
- Copy and move jobs use unmistakable partial names and verify files before the
  final rename.
- Transfers enumerate every source entry, including dotfiles, and never inherit
  the browser's hidden-file filter or 10,000-item display cap.
- Batch jobs run serially instead of starting hundreds of concurrent copies.
- The volume root can never be deleted.
- Every recursive deletion path, including conflict replacement and
  cross-filesystem move cleanup, never follows symlinks and refuses nested
  filesystem boundaries.
- Editor writes are size limited, conflict checked, and atomically replaced.

This application exposes whatever storage the operator mounts. Do not mount the
Docker socket, block devices, the host root, or sensitive folders that users
should not access. Prefer LAN, VPN, or Tailscale access. If a reverse proxy
provides HTTPS, set `DIRDECK_SECURE_COOKIE=true`.

## Documentation

| Document | Purpose |
|----------|---------|
| [docs/INSTALL.md](docs/INSTALL.md) | Installation, credentials, environment, and health |
| [docs/USER-GUIDE.md](docs/USER-GUIDE.md) | Selection, copy, move, delete, rename, editor, and previews |
| [docs/STORAGE-MOUNTS.md](docs/STORAGE-MOUNTS.md) | Adding local disks and network shares safely |
| [docs/TRANSFERS.md](docs/TRANSFERS.md) | Durable batch jobs, progress, conflicts, cancellation |
| [docs/UPGRADING.md](docs/UPGRADING.md) | Backup, update, migration, and rollback |
| [docs/OPERATIONS.md](docs/OPERATIONS.md) | Troubleshooting and unavailable mounts |
| [docs/SECURITY.md](docs/SECURITY.md) | Threat model and enforced boundaries |
| [docs/AUTH.md](docs/AUTH.md) | Authentication, sessions, and CSRF |
| [docs/API.md](docs/API.md) | HTTP API |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Components and runtime design |
| [docs/KEYBOARD.md](docs/KEYBOARD.md) | Keyboard and selection shortcuts |
| [docs/RELEASE-TEST.md](docs/RELEASE-TEST.md) | Clean-install release acceptance test |
| [docs/PRODUCT-ROADMAP.md](docs/PRODUCT-ROADMAP.md) | Public-launch priorities and rebrand proposal |
| [CHANGELOG.md](CHANGELOG.md) | Release history |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Development and pull-request rules |
| [SECURITY.md](SECURITY.md) | Private vulnerability reporting policy |

## Development

```bash
cd frontend
npm ci
npm run lint
npm run build

cd ../backend
go test ./...
go vet ./...
```

Automated tests use temporary directories and disposable fixtures only. Never
point tests at real user storage.

Pull requests run the same backend, frontend, Compose, and container build
checks in GitHub Actions. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Licensed under the [MIT License](LICENSE).
