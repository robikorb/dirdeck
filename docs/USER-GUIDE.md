# User guide

## Dual-pane layout

Each pane has its own volume, folder, view mode, selection, and breadcrumb.
Click inside a pane to make it active. Center buttons send the active selection
to the opposite pane.

Click a folder once to open it. Supported text and code files have a pencil
button that opens the editor. The dedicated selector beside each list row or in
the corner of each grid card adds or removes that item without opening it. The
Inspector shows metadata and previews for the focused item.

## Multiple selection

| Action | Result |
|--------|--------|
| Item selector | Add or remove one item without opening it |
| Shift+item selector | Add a contiguous range from the anchor |
| Click a file row or card | Replace the selection with that file |
| Click a folder row or card | Open that folder |
| Cmd/Ctrl+click | Add or remove one item |
| Shift+click | Select a contiguous range from the anchor |
| Cmd/Ctrl+Shift+click | Add a contiguous range |
| Cmd/Ctrl+A | Select up to 500 visible items |
| Select-all toolbar button | Select or clear up to 500 visible items |
| Escape | Clear the active selection |
| Arrow Up/Down | Move to a single focused item |

The pane footer displays selected item count and the combined size of selected
regular files. Directory content is included later when a transfer job plans
its exact totals.

Batch operations are intentionally limited to 500 top-level selected items.
Directories may contain any number of nested files subject to listing and
storage limits.

## Copy

1. Select one or more source items.
2. Open the destination directory in the other pane.
3. Press F5 or click the appropriate copy arrow.
4. Follow the combined job in Transfers.

One batch job processes the selection serially. It reports total bytes, files,
current source path, speed, and destination free space observed at job start.
The source is never changed by copy.

Copying from a read-only volume is allowed. Copying into a read-only volume is
rejected by the server.

## Move

Move uses F6 or the center move buttons. Both source and destination volumes
must be writable because a successful move removes the source.

The backend first attempts a same-filesystem atomic rename. If the filesystems
differ, it performs a verified copy and removes the source only after the
destination is finalized.

If destination verification succeeds but source deletion fails, the job is
reported as failed and both copies may remain. The completed destination is not
silently deleted.

## Conflicts

The default policy is Prompt. When a destination name exists:

- **Skip** leaves the existing destination unchanged.
- **Replace** removes the destination and writes the selected source.
- **Rename** creates a unique destination name.
- **Replace all in batch** applies Replace to the current and all later
  conflicts in the same batch job.

Apply-to-all state is persisted with the transfer job, so the rest of the batch
does not pause at every conflict.

## Cancel

Cancel changes the job to Cancelling and stops at a safe checkpoint. Partial
files keep `.lgfm-partial-*` names while cleanup runs and are never presented as
completed files. Completed items from an earlier part of a batch remain
completed.

## Delete

Select one or more items and press Delete or click the red trash button.

The confirmation dialog shows the item count, several names, and the number of
selected directories. Deletion is permanent. There is no recycle bin in this
release.

Server protections:

- read-only volumes cannot be deleted from;
- a configured volume root cannot be deleted;
- all paths are validated before the first batch item is removed;
- duplicate paths and descendants covered by a selected parent are collapsed;
- symlinks are unlinked without following their target;
- recursive deletion refuses to cross into a nested filesystem mount.

## Rename

Rename is available only when exactly one non-symlink item is selected. Press
F2 or use the pencil button. Rename stays inside the same parent directory and
never overwrites an existing name.

## Uploading files

Drag files onto a pane, or use the upload button in the pane toolbar. Files go
into the folder that pane is showing. Both controls are disabled on read-only
volumes.

Uploads run one at a time with per-file progress, and can be cancelled mid-file.
If a name already exists you are asked what to do: skip it, keep both, replace
it, or keep both for the rest of the batch.

Folder upload is not supported yet; drop individual files.

## Editor

Click the pencil action on a supported text or code file to open the Monaco
editor.

- maximum editable size is 2 MiB;
- content must be valid UTF-8;
- read-only volumes open in preview-only mode;
- Cmd/Ctrl+S saves;
- stale writes return a conflict instead of replacing a newer file;
- the save is staged, flushed, and atomically renamed over the old file.

The editor does not send content to a CDN or external language service.

## Preview

The Inspector supports image and media preview, bounded text preview, sanitized
Markdown, pretty-printed JSON, and bounded DOCX text extraction. Preview
availability depends on the volume policy and file type.

## Locations sidebar

The sidebar on the left stacks three sections — Volumes, Favorites, and Recent.
Click a section heading to collapse or expand it; all three can be open at once.
Each section header carries its own action: refresh volume availability, add or
remove the current folder as a favorite, and clear recent locations.

Clicking a volume opens it in the active pane. Clicking a favorite or a recent
entry navigates the active pane to that folder.

## Favorites and recent folders

Cmd/Ctrl+D favorites the current folder. Favorites, recent locations, pane
state, and transfer history are stored in the persistent application database.
