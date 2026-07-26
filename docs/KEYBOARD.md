# Keyboard shortcuts

Primary dual-pane shortcuts. File-manager shortcuts are disabled while typing
in inputs or using the editor.

| Shortcut | Action |
|----------|--------|
| `Tab` | Normal browser focus navigation |
| Item selector | Add or remove an item without opening it |
| `Shift`+item selector | Add a contiguous range |
| Click a file | Select that file |
| Click a folder | Open that folder |
| `Cmd/Ctrl+click` | Toggle one item in the active selection |
| `Shift+click` | Select a contiguous range |
| `Cmd/Ctrl+Shift+click` | Add a contiguous range |
| `Cmd/Ctrl+A` | Select up to 500 visible items |
| `Escape` | Clear selection |
| `↑` / `↓` | Replace selection with the previous or next item |
| `Enter` | Open the single selected folder |
| `Backspace` | Go to parent folder |
| Click a column header | Sort by that column; click again to reverse |
| `F7` | New folder in the active pane |
| `F2` | Rename selected file or folder |
| `Delete` | Open permanent-delete confirmation for the selected item |
| `F5` | Copy selection to the other pane |
| `F6` | Move selection to the other pane |
| `G` | Grid view (active pane) |
| `L` | List view (active pane) |
| `Ctrl/Cmd+D` | Favorite / bookmark the current folder |
| `Ctrl/Cmd+I` | Toggle inspector |
| `Ctrl/Cmd+R` | Refresh volume availability and reload both panes |

F5, F6, and Delete operate on the complete active selection. Rename stays
disabled unless exactly one compatible item is selected. The pane toolbar also
contains a select-all/clear-selection button.

Arrow keys and `Ctrl/Cmd+A` follow the order the pane is currently sorted by,
not the order the directory loaded in, and the selected row is scrolled into
view as you move.

## Known gaps

These are tracked for a future release and are listed here so the table above
stays honest:

- **The editor cannot be opened from the keyboard.** Use the pencil button on
  the row, which appears on hover or keyboard focus.
- **There is no type-ahead.** Pressing an unmodified letter does not jump to a
  filename; `g` and `l` switch the pane between grid and list view. Filename
  search is planned.
- **Every row is a tab stop.** In a large folder, reaching the pane toolbar with
  Tab alone is impractical.
