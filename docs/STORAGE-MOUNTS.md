# Storage mounts and file operations

## Host storage boundary

The host operating system attaches the disk, share, or folder first. Docker then
bind-mounts that existing host path into the application container.

The application manages only paths visible under `/mnt/volumes` inside the
container. It does not need privileged mode, host PID access, the Docker socket,
or direct block-device access.

This boundary is intentional. A web application inside a normal container
cannot safely browse the whole host or attach arbitrary host folders that were
not granted when the container was created.

## Compose pattern

Copy the tracked template once:

```bash
cp compose.override.example.yml compose.override.yml
```

Use long-form bind mounts in the local `compose.override.yml` so source, target,
and read-only state remain clear. The file is gitignored and survives updates:

```yaml
services:
  file-manager:
    volumes:
      - type: bind
        source: /host/path/documents
        target: /mnt/volumes/documents
        read_only: true
      - type: bind
        source: /host/path/external-drive
        target: /mnt/volumes/external-drive
        read_only: true
```

Add `read_only: true` to a bind entry when the application must not modify it.
The backend must also enforce the mount capability instead of relying only on
the container mount flag.

The matching application configuration is explicit:

```yaml
volumes:
  - id: documents
    name: Documents
    path: /mnt/volumes/documents
    readOnly: true
    showHiddenFiles: false
    thumbnails: false
  - id: external-drive
    name: External drive
    path: /mnt/volumes/external-drive
    readOnly: false
    showHiddenFiles: false
    thumbnails: true
```

Set `thumbnails: true` only when the operator accepts bounded image decoding for
that volume (see [SECURITY.md](SECURITY.md)). The example fixture stack enables
thumbnails on both disposable roots.

The backend must reject unknown fields, duplicate IDs, missing roots, roots
outside `/mnt/volumes`, and overlapping roots. The browser receives only IDs,
display names, capabilities, availability, and relative paths. It never receives
the configured container path or a host path.

If a bind mount becomes unreachable after startup, volume discovery reports
`available: false` and filesystem endpoints return `503`. Restore the host mount
and refresh; restart if the path was missing when the process started. See
[OPERATIONS.md](OPERATIONS.md).

## Read-only-first activation

For each real disk or share:

1. Confirm the host path and intended scope. Do not mount the host root for
   convenience.
2. Add the bind to `compose.override.yml` with `read_only: true`.
3. Add the matching path to `config/volumes.yaml` with `readOnly: true`.
4. Run `docker compose config` and inspect the resolved mount.
5. Run `docker compose up -d` and confirm the volume is available in the UI.
6. Test browse, preview, editor read-only mode, hidden files, and reconnect.
7. If writes are required, first create a dedicated disposable test directory.
8. Change both Docker `read_only` and registry `readOnly` only for the approved
   volume, restart, then test copy, move, rename, edit, and delete on disposable
   files.
9. Back up important storage before enabling permanent deletion.

Changing only one layer does not grant write access: both Docker and the
application registry must allow it.

## Platform examples

### macOS

External disks normally appear below `/Volumes`. Local user folders appear below
`/Users`.

Prefer mounting the specific folders or drives required by the application:

```yaml
volumes:
  - type: bind
    source: /Users/example/Documents
    target: /mnt/volumes/documents
  - type: bind
    source: /Volumes/Media
    target: /mnt/volumes/media
```

Docker Desktop must be allowed to share the selected paths.

### Linux

Mount internal disks, USB disks, NFS shares, or SMB shares on the host first,
then bind their mount points:

```yaml
volumes:
  - type: bind
    source: /home/example/Documents
    target: /mnt/volumes/documents
  - type: bind
    source: /mnt/media
    target: /mnt/volumes/media
```

The container user needs suitable permissions on each host path. SELinux-based
hosts may also need an explicit, carefully reviewed labeling policy.

### Windows

Docker Desktop can bind a Windows folder or drive into a Linux container after
that location is shared with Docker Desktop. Keep the Windows-specific source
path in an installation override or environment-specific Compose file. The
application itself still sees a normal path such as
`/mnt/volumes/external-drive`.

### Proxmox

Attach the physical disk or storage mount to a suitable Linux VM or container
environment first. Run Docker there, then bind-mount the resulting Linux path
into this application. Do not install the application directly on the Proxmox
host merely to gain filesystem access.

## Required copy behavior (Phase 1)

Implemented as durable SQLite jobs — see `TRANSFERS.md` and `API.md`.

- Copy files and directories within one mount or across mounts.
- Stream data with bounded memory use.
- Write to a temporary destination name first.
- Expose progress, speed, remaining bytes, and cancellation via REST + SSE.
- Report destination free space on a best-effort basis before/during transfer.
- Offer explicit conflict choices: skip, replace, rename, or apply to all.
- Never expose a partial file under its final destination name.
- Preserve timestamps and supported metadata on a best-effort basis.
- Clean up temporary files after cancellation or failure.
- Reject writes to volumes marked `readOnly: true` (including the RO fixture).

Each copy uses a destination-side staging name such as
`.lgfm-partial-<job-id>`. The final name appears only after the staged content is
closed, flushed where supported, verified, and atomically renamed. If a storage
backend cannot provide a reliable free-space value, report that the value is
unknown instead of rejecting the job or claiming sufficient space.

Disposable fixture mounts under `fixtures/` are for development and demos.
Automated tests must create their own temporary roots and must never point at
real user storage.

## Required move behavior (Phase 2)

Implemented as durable SQLite jobs — see `TRANSFERS.md` and `API.md`.

When source and destination share a filesystem, use an atomic rename when the
platform and filesystem support it.

Do not decide this from path strings alone. Attempt the safe rename and fall
back to the verified copy workflow only for an expected cross-device or
unsupported-operation result.

When moving across filesystems:

1. Copy to a temporary destination.
2. Flush and close the destination.
3. Verify at least size and expected transfer completion.
4. Rename the temporary destination to the final name.
5. Delete the source only after every prior step succeeds.

The current release verifies planned completion and size. A user-selectable
full-file checksum mode is not implemented.

If verification or source deletion fails, report the exact state. Never imply
that a move completed when both copies remain or when the destination is
incomplete. Read-only mounts cannot be move sources or destinations.

## Permissions

Every configured storage root has its own read-only or read-write capability.
The API enforces that capability for every operation.

Linux deployments may use configurable UID and GID values. Docker Desktop
deployments must not assume Linux ownership semantics are fully available.
Permission errors should be shown clearly without suggesting unsafe
`chmod 777` workarounds.

The example Compose deployment should support `PUID`, `PGID`, and `UMASK`
configuration or an equivalent explicit container `user`. The final process
must not run as root.
