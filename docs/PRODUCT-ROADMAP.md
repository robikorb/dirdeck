# Product roadmap and rebrand plan

Status: proposal for review  
Current product name: DirDeck  
Recommended working name: **DirDeck**

## Product position

DirDeck should be presented as:

> A fast, self-hosted dual-pane file manager for homelabs.

The primary distinction is not the visual theme. It is the orthodox dual-pane
workflow in a modern browser, packaged as one self-hosted Docker application.
The design supports that promise, but should not be the product name.

The target operator:

- runs Docker on a NAS, home server, or small Linux/macOS host;
- wants Total Commander, ForkLift, or Midnight Commander-style workflows;
- prefers local storage, no telemetry, and no cloud account;
- expects safe bulk copy and move operations across mounted volumes;
- may access the service through a LAN, VPN, or HTTPS reverse proxy.

## What is already resolved

The following findings from the external review have already been addressed and
must not be repeated as open roadmap work:

- complete copy and move traversal, including hidden entries and directories
  larger than the browser listing limit;
- safe cross-filesystem move cleanup without following symlinks;
- localhost-only default bind with explicit LAN opt-in;
- corrected SQLite configuration, WAL mode, and batched progress writes;
- bounded thumbnail retries and virtualized list and grid rendering;
- background transfer planning, free-space checks, and cancellation;
- graceful shutdown that releases open event streams first and gives transfer
  cleanup an independent time budget;
- fixed list row height so long filenames cannot desynchronise row
  virtualization;
- documented network exposure model, reverse-proxy limitations, and upgrade
  notes;
- restrictive Content Security Policy;
- hashed session tokens, expired-session pruning, and safer login timing;
- configurable credentials, sessions, throttling, bind address, and backups;
- backup retention and private archive permissions;
- race-detector CI and a live container readiness test;
- explicit refresh, focus refresh, periodic refresh, and active-volume refresh;
- single-click folder navigation, explicit editor action, text-selection-safe
  range selection, improved grid sizing, and a complete context menu.

## Public launch gate

These items should be complete before promoting the project on r/selfhosted or
similar communities.

### 1. Rebrand without breaking existing installations

- Adopt a final product name after a lightweight package, repository, domain,
  and trademark check.
- Change visible copy, icons, README text, repository description, container
  labels, and documentation together.
- Keep the pre-rename `LGFM_*` environment variables and current Docker state
  volume compatible for at least two releases.
- If new environment names are introduced, treat the old names as documented
  aliases and emit a non-fatal deprecation notice.
- Do not silently rename the Docker state volume. Provide an explicit migration
  command and rollback procedure if that is ever needed.
- Preserve the existing API paths during the rebrand.

### 2. Show the real product

- Add three or four screenshots made from the real running application:
  dual-pane list view, grid/preview, transfer progress, and editor/context menu.
- Add a short optimized GIF or WebM showing a cross-pane copy.
- Put the strongest screenshot directly below the opening pitch.
- Replace design-concept artwork with runtime evidence where product behavior is
  being demonstrated.
- Add a short comparison section explaining the dual-pane focus without
  attacking FileBrowser, Filestash, or other projects.

### 3. Publish prebuilt multi-architecture images

- Add a release workflow using Docker Buildx for `linux/amd64` and
  `linux/arm64`.
- Publish versioned images to GHCR:
  `ghcr.io/robikorb/dirdeck:<version>`.
- Publish `latest` only for stable releases, never for release candidates.
- Make the normal Compose file use `image:` so installation does not compile Go,
  npm, and Monaco locally.
- Keep source builds in a separate development override.
- Generate an SBOM and attach image provenance to releases.
- Test clean install, upgrade, rollback, state persistence, and mounted-volume
  visibility using the published image rather than the local source tree.

### 4. Add browser upload

The first implementation should be intentionally narrow and safe:

- multi-file and drag-and-drop upload into the active pane;
- streaming request handling with no whole-file buffering;
- unmistakable temporary names followed by atomic final rename;
- skip, replace, rename, and apply-to-all conflict handling;
- progress and cancellation integrated into the existing transfer UI;
- server-side filename, path, free-space, read-only, and size checks;
- cleanup of abandoned partial uploads;
- tests for disconnects, duplicate names, zero-byte files, large files,
  read-only volumes, and hostile filenames.

Folder upload and resumable chunk upload can follow after ordinary upload is
stable.

### 5. Add scoped filename search

The first search release should search names, not file contents:

- search inside the active volume or current folder;
- asynchronous, cancellable traversal with bounded results;
- filters for file/folder, extension, size, and modified date;
- reveal a result in either pane;
- preserve the same symlink, hidden-file, and mount-boundary rules as browsing;
- avoid a mandatory background index in the first version.

Content indexing and OCR are separate future features and should not delay the
initial search release.

### 6. Make reverse-proxy behavior safe

- Add an explicit trusted-proxy CIDR configuration.
- Honor `Forwarded` or `X-Forwarded-For` only when the direct peer is trusted.
- Key login throttling on the verified client address, never an arbitrary
  request header.
- Test spoofed forwarding headers, multiple clients behind one proxy, IPv4,
  IPv6, and direct LAN access.
- Document working examples for Caddy, Traefik, and Nginx Proxy Manager.
- Keep direct public-internet exposure unsupported; recommend LAN, VPN, or
  Tailscale plus HTTPS.

## Post-launch roadmap

### Phase A: complete everyday file operations

- Stream a selected folder or selection as a ZIP without first creating a large
  temporary archive.
- Add keyboard-accessible upload, search, and archive actions.
- Add optional checksum calculation and copy verification for advanced users.
- Add an operations log that clearly distinguishes user actions, completed
  jobs, conflicts, cancellation, and failures.

### Phase B: deployment flexibility

- Support a configurable URL base path such as `/files/`.
- Apply the base path consistently to SPA routing, API calls, web workers,
  assets, cookies, CSP, and redirects.
- Add automated root-path and subpath reverse-proxy tests.
- Provide a configuration validator or `doctor` command.
- Supply commented Compose examples for one root, several named volumes,
  read-only mounts, SMB/NFS host mounts, and macOS Docker Desktop.

Docker bind mounts must remain operator-controlled. The web application should
not receive the Docker socket or gain the ability to mount arbitrary host
paths. A friendlier configuration flow can validate and explain mounts, but it
cannot safely bypass Docker's mount boundary.

### Phase C: controlled sharing

- Add revocable, time-limited, read-only file download links.
- Support optional link password, expiry, download limit, and audit entry.
- Do not allow shared links to browse neighboring paths.
- Add folder sharing only after streaming ZIP behavior is proven.

### Phase D: multiple users and external authentication

Single-user mode should remain the v1 security model. Multi-user support is a
larger authorization project, not a small login enhancement.

- Define users, groups, volume grants, read/write capabilities, and per-path
  policy semantics.
- Enforce every permission in the Go API, not only in the React interface.
- Add privileged, own-scope, and foreign-scope authorization tests.
- Add OIDC or trusted forward-auth only after the internal authorization model
  is complete.
- Keep local authentication available for recovery.

## Recommended name

### DirDeck

Why it fits:

- short enough for a repository, image, command, and browser title;
- suggests directories arranged as a working deck;
- does not tie the project to an Apple design trend;
- leaves room for themes and UI changes;
- supports a direct tagline: **Dual-pane file management for your homelab.**

Proposed identifiers after approval:

| Surface | Value |
|---|---|
| Product | DirDeck |
| Repository | `robikorb/dirdeck` |
| Container | `dirdeck` |
| GHCR image | `ghcr.io/robikorb/dirdeck` |
| Compose project example | `dirdeck` |
| State volume for new installs | `dirdeck-data` |
| New environment prefix | `DIRDECK_` |
| Legacy environment prefix | `DIRDECK_` remains supported |

The name is a recommendation, not a completed legal or trademark clearance.
Before the repository rename, recheck major package registries, container
registries, domains, GitHub, and relevant trademark databases.

## Alternative names

1. **DuoDir**: compact and technically descriptive, but less natural to say.
2. **PaneShift**: communicates movement between panes, but an existing small
   pagination project already uses the name.
3. **PaneDeck**: visually relevant, but weaker than DirDeck as a command and
   product name.

Rejected working names:

- **PaneForge**: already an established open-source pane-layout library.
- **FileDeck**: already used by an active document-management product.
- **TwinPane**: already used by a commercial remote file-access product.
- **DirDeck**: descriptive but derivative, visually narrow,
  and likely to date faster than the product.

## Suggested release sequence

### 0.2.0: launchable foundation

- final name and compatibility-safe rebrand;
- real screenshots and demo clip;
- GHCR multi-arch images and image-based Compose;
- trusted-proxy fix and proxy documentation;
- browser upload;
- scoped filename search;
- clean-install and upgrade test from the public repository.

### 0.3.0: complete personal file manager

- streaming ZIP download;
- subpath hosting;
- configuration doctor and improved mount examples;
- richer operations log and optional checksums.

### 0.4.0: safe sharing

- expiring, revocable file share links;
- audit and download limits;
- security review focused on unauthenticated download paths.

### 1.0.0: stable single-user contract

- stable API and configuration compatibility policy;
- documented backup, restore, rollback, and migration guarantees;
- accessibility and keyboard-navigation audit;
- threat-model review and an independent destructive-operation test pass;
- no known data-loss defects.

Multi-user authorization and OIDC should be a later milestone unless real user
demand justifies expanding the security model before 1.0.

## Definition of public-ready

The project is ready for a public launch when a new user can:

1. copy one Compose example and set credentials;
2. pull a signed multi-architecture image without compiling source;
3. mount a test directory read-only and see it in the browser;
4. upload, search, copy, move, rename, edit, download, and delete test data;
5. update to the next version without losing credentials, configuration, jobs,
   or state;
6. understand the security boundary and limitations from the first README
   screen;
7. see the real interface before installing it.
