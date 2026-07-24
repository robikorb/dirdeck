import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  ArrowLeftRight,
  CheckSquare2,
  ChevronRight,
  Clock,
  File,
  FileText,
  Folder,
  HardDrive,
  Image as ImageIcon,
  LayoutGrid,
  Link2,
  List,
  LogOut,
  PanelRightClose,
  PanelRightOpen,
  Pencil,
  RefreshCw,
  Star,
  StarOff,
  Square,
  Trash2,
  X,
} from 'lucide-react'
import {
  addFavorite,
  cancelTransfer,
  clearRecent,
  createCopy,
  createMove,
  deleteEntries,
  fetchEditableFile,
  fetchTextPreview,
  formatDate,
  formatSize,
  formatSpeed,
  getPaneState,
  getSession,
  isImagePath,
  isEditablePath,
  isTextPreviewablePath,
  listDir,
  listFavorites,
  listRecent,
  listTransfers,
  listVolumes,
  login,
  logout,
  previewURL,
  recordRecent,
  renameEntry,
  removeFavorite,
  resolveConflict,
  savePaneState,
  saveEditableFile,
  setCsrfToken,
  statPath,
  subscribeTransfers,
  thumbnailURL,
  type TextPreview,
} from './api'
import { renderSafeMarkdown } from './markdown'
import DeleteModal from './DeleteModal'
import RenameModal from './RenameModal'
import type { EditorDocument } from './EditorModal'
import type {
  ConflictAction,
  DirEntry,
  Favorite,
  FileMeta,
  RecentLocation,
  TransferJob,
  Volume,
} from './api'

const EditorModal = lazy(() => import('./EditorModal'))

type ViewMode = 'list' | 'grid'
type ActivePane = 'left' | 'right'
type RailMode = 'volumes' | 'favorites' | 'recent'

type PaneState = {
  volumeId: string
  path: string
  view: ViewMode
  entries: DirEntry[]
  truncated: boolean
  selected: string | null
  selectedPaths: string[]
  selectionAnchor: string | null
  loading: boolean
  error: string | null
  reloadToken: number
}

const emptyPane = (): PaneState => ({
  volumeId: '',
  path: '',
  view: 'list',
  entries: [],
  truncated: false,
  selected: null,
  selectedPaths: [],
  selectionAnchor: null,
  loading: false,
  error: null,
  reloadToken: 0,
})

function LoginScreen({ onSuccess }: { onSuccess: () => void }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await login(username, password)
      onSuccess()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Login failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="login-page">
      <form className="glass login-card" onSubmit={onSubmit}>
        <div className="login-brand">
          <img src="/app-icon.svg" alt="" />
          <div>
            <h1>Liquid Glass</h1>
            <span>File Manager</span>
          </div>
        </div>
        <p>Sign in to browse configured storage volumes.</p>
        <label>
          Username
          <input
            autoComplete="username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            required
          />
        </label>
        <label>
          Password
          <input
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
        </label>
        {error ? <p className="login-error">{error}</p> : null}
        <button type="submit" disabled={busy}>
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </div>
  )
}

function entryIcon(entry: DirEntry) {
  if (entry.isSymlink) return <Link2 size={16} aria-hidden />
  if (entry.isDir) return <Folder size={16} aria-hidden />
  if (isImagePath(entry.name)) return <ImageIcon size={16} aria-hidden />
  if (entry.name.match(/\.(txt|md|json|ya?ml|csv)$/i)) return <FileText size={16} aria-hidden />
  return <File size={16} aria-hidden />
}

function Thumb({
  volumeId,
  entry,
  enabled,
}: {
  volumeId: string
  entry: DirEntry
  enabled: boolean
}) {
  const [failed, setFailed] = useState(false)
  if (!enabled || entry.isDir || entry.isSymlink || !isImagePath(entry.name) || failed) {
    return <div className="grid-icon">{entryIcon(entry)}</div>
  }
  return (
    <div className="grid-icon thumb">
      <img
        src={thumbnailURL(volumeId, entry.path)}
        alt=""
        loading="lazy"
        onError={() => setFailed(true)}
      />
    </div>
  )
}

function Pane({
  label,
  paneId,
  active,
  volumes,
  state,
  onChange,
  onSelectMeta,
  onOpenFile,
  onActivate,
}: {
  label: string
  paneId: ActivePane
  active: boolean
  volumes: Volume[]
  state: PaneState
  onChange: (next: PaneState) => void
  onSelectMeta: (volumeId: string, path: string, entry: DirEntry | null) => void
  onOpenFile: (volumeId: string, path: string, entry: DirEntry) => void
  onActivate: () => void
}) {
  const volume = volumes.find((v) => v.id === state.volumeId)
  const thumbsOn = Boolean(volume?.thumbnails && volume.available)
  const crumbs = useMemo(() => {
    const parts = state.path ? state.path.split('/').filter(Boolean) : []
    const items = [{ label: volume?.name ?? 'Volume', path: '' }]
    let acc = ''
    for (const part of parts) {
      acc = acc ? `${acc}/${part}` : part
      items.push({ label: part, path: acc })
    }
    return items
  }, [state.path, volume?.name])

  useEffect(() => {
    if (!state.volumeId) return
    let cancelled = false
    onChange({ ...state, loading: state.entries.length === 0, error: null })
    void listDir(state.volumeId, state.path)
      .then((result) => {
        if (cancelled) return
        const entries = [...result.entries].sort((a, b) => {
          if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
          return a.name.localeCompare(b.name)
        })
        const availablePaths = new Set(entries.map((entry) => entry.path))
        const selectedPaths = state.selectedPaths.filter((item) => availablePaths.has(item))
        onChange({
          ...state,
          entries,
          selected: state.selected && availablePaths.has(state.selected)
            ? state.selected
            : (selectedPaths.at(-1) ?? null),
          selectedPaths,
          selectionAnchor: state.selectionAnchor && availablePaths.has(state.selectionAnchor)
            ? state.selectionAnchor
            : (selectedPaths[0] ?? null),
          truncated: result.truncated,
          loading: false,
          error: null,
        })
      })
      .catch((err: unknown) => {
        if (cancelled) return
        onChange({
          ...state,
          entries: [],
          truncated: false,
          loading: false,
          error: err instanceof Error ? err.message : 'Failed to list',
        })
      })
    return () => {
      cancelled = true
    }
    // Intentionally only reload when volume/path/token change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state.volumeId, state.path, state.reloadToken])

  function selectEntry(entry: DirEntry, event?: React.MouseEvent) {
    const toggle = Boolean(event?.metaKey || event?.ctrlKey)
    const extend = Boolean(event?.shiftKey)
    let nextPaths: string[]

    if (extend && state.entries.length > 0) {
      const anchorPath = state.selectionAnchor ?? state.selected ?? entry.path
      const anchorIndex = Math.max(0, state.entries.findIndex((item) => item.path === anchorPath))
      const entryIndex = state.entries.findIndex((item) => item.path === entry.path)
      const from = Math.min(anchorIndex, entryIndex)
      const to = Math.max(anchorIndex, entryIndex)
      const range = state.entries.slice(from, to + 1).map((item) => item.path)
      nextPaths = toggle ? [...new Set([...state.selectedPaths, ...range])] : range
    } else if (toggle) {
      nextPaths = state.selectedPaths.includes(entry.path)
        ? state.selectedPaths.filter((item) => item !== entry.path)
        : [...state.selectedPaths, entry.path]
    } else {
      nextPaths = [entry.path]
    }
    nextPaths = nextPaths.slice(0, 500)
    onChange({
      ...state,
      selected: nextPaths.includes(entry.path) ? entry.path : (nextPaths.at(-1) ?? null),
      selectedPaths: nextPaths,
      selectionAnchor: extend ? (state.selectionAnchor ?? state.selected ?? entry.path) : entry.path,
    })
    onSelectMeta(state.volumeId, entry.path, entry)
  }

  function activateEntry(entry: DirEntry) {
    if (!entry.isDir) return
    onChange({
      ...state,
      path: entry.path,
      selected: null,
      selectedPaths: [],
      selectionAnchor: null,
    })
    onSelectMeta(state.volumeId, entry.path, null)
  }

  function handleEntryClick(entry: DirEntry, event: React.MouseEvent) {
    const modifiedSelection = event.metaKey || event.ctrlKey || event.shiftKey
    if (entry.isDir && !modifiedSelection) {
      activateEntry(entry)
      return
    }
    selectEntry(entry, event)
  }

  function editEntry(entry: DirEntry, event: React.MouseEvent | React.KeyboardEvent) {
    event.preventDefault()
    event.stopPropagation()
    onOpenFile(state.volumeId, entry.path, entry)
  }

  const selectedEntries = state.entries.filter((entry) => state.selectedPaths.includes(entry.path))
  const selectedBytes = selectedEntries.reduce(
    (total, entry) => total + (entry.isDir ? 0 : entry.size),
    0,
  )
  const selectableCount = Math.min(500, state.entries.length)

  return (
    <section
      className={`glass pane ${active ? 'pane-active' : ''}`}
      aria-label={label}
      data-pane={paneId}
      tabIndex={0}
      onMouseDown={onActivate}
      onFocus={onActivate}
    >
      <div className="pane-header">
        <h2>
          {volume ? volume.name : 'Select a volume'}
          {volume && !volume.available ? <span className="badge-warn"> offline</span> : null}
        </h2>
        <div className="topbar-actions">
          <button
            type="button"
            className="icon-btn"
            aria-label="Refresh folder"
            title="Refresh folder"
            onClick={() => onChange({ ...state, reloadToken: state.reloadToken + 1 })}
          >
            <RefreshCw size={16} />
          </button>
          <button
            type="button"
            className="icon-btn"
            aria-label={state.selectedPaths.length === selectableCount && selectableCount > 0
              ? 'Clear selection'
              : 'Select all visible items'}
            title={state.selectedPaths.length === selectableCount && selectableCount > 0
              ? 'Clear selection (Esc)'
              : 'Select all (Ctrl/Cmd+A)'}
            onClick={() => {
              const allSelected =
                selectableCount > 0 && state.selectedPaths.length === selectableCount
              const paths = allSelected
                ? []
                : state.entries.slice(0, 500).map((entry) => entry.path)
              onChange({
                ...state,
                selected: paths.at(-1) ?? null,
                selectedPaths: paths,
                selectionAnchor: paths[0] ?? null,
              })
            }}
          >
            {selectableCount > 0 && state.selectedPaths.length === selectableCount
              ? <CheckSquare2 size={16} />
              : <Square size={16} />}
          </button>
          <button
            type="button"
            className={`icon-btn ${state.view === 'list' ? 'active' : ''}`}
            aria-label="List view"
            onClick={() => onChange({ ...state, view: 'list' })}
          >
            <List size={16} />
          </button>
          <button
            type="button"
            className={`icon-btn ${state.view === 'grid' ? 'active' : ''}`}
            aria-label="Grid view"
            onClick={() => onChange({ ...state, view: 'grid' })}
          >
            <LayoutGrid size={16} />
          </button>
        </div>
      </div>
      <div className="breadcrumb" aria-label="Breadcrumb">
        {crumbs.map((c, i) => (
          <span key={c.path + i} style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
            {i > 0 ? <ChevronRight size={12} aria-hidden /> : null}
            <button
              type="button"
              onClick={() => onChange({
                ...state,
                path: c.path,
                selected: null,
                selectedPaths: [],
                selectionAnchor: null,
              })}
            >
              {c.label}
            </button>
          </span>
        ))}
        {volume?.readOnly ? <span className="muted"> · read-only</span> : null}
      </div>
      <div className="pane-body">
        {state.loading ? <p className="muted">Loading…</p> : null}
        {state.error ? (
          <div className="pane-error">
            <p className="login-error">{state.error}</p>
            <button
              type="button"
              onClick={() => onChange({ ...state, reloadToken: state.reloadToken + 1 })}
            >
              Retry
            </button>
          </div>
        ) : null}
        {state.truncated ? (
          <p className="muted truncate-note">
            Listing truncated at 10,000 entries. Narrow the folder or use search on the host.
          </p>
        ) : null}
        {!state.loading && !state.error && state.view === 'list' ? (
          <table className="list-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Date modified</th>
                <th>Size</th>
                <th className="entry-action-heading" aria-label="Actions" />
              </tr>
            </thead>
            <tbody>
              {state.entries.map((entry) => (
                <tr
                  key={entry.path}
                  className={state.selectedPaths.includes(entry.path) ? 'selected' : ''}
                  tabIndex={0}
                  aria-selected={state.selectedPaths.includes(entry.path)}
                  onClick={(event) => handleEntryClick(entry, event)}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter' && entry.isDir) {
                      event.preventDefault()
                      activateEntry(entry)
                    }
                  }}
                >
                  <td>
                    <span className="row-name">
                      {entryIcon(entry)}
                      {entry.name}
                    </span>
                  </td>
                  <td>{formatDate(entry.modTime)}</td>
                  <td>{entry.isDir ? '—' : formatSize(entry.size)}</td>
                  <td className="entry-action-cell">
                    {!entry.isDir && isEditablePath(entry.path) ? (
                      <button
                        type="button"
                        className="entry-edit-button"
                        aria-label={`Edit ${entry.name}`}
                        title="Open in editor"
                        onClick={(event) => editEntry(entry, event)}
                        onKeyDown={(event) => event.stopPropagation()}
                      >
                        <Pencil size={14} />
                      </button>
                    ) : null}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : null}
        {!state.loading && !state.error && state.view === 'grid' ? (
          <div className="grid">
            {state.entries.map((entry) => (
              <div
                key={entry.path}
                className={`grid-item ${state.selectedPaths.includes(entry.path) ? 'selected' : ''}`}
                role="button"
                tabIndex={0}
                aria-pressed={state.selectedPaths.includes(entry.path)}
                onClick={(event) => handleEntryClick(entry, event)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' && entry.isDir) {
                    event.preventDefault()
                    activateEntry(entry)
                  } else if (event.key === ' ' && !entry.isDir) {
                    event.preventDefault()
                    selectEntry(entry)
                  }
                }}
              >
                <Thumb volumeId={state.volumeId} entry={entry} enabled={thumbsOn} />
                <div className="grid-name" title={entry.name}>{entry.name}</div>
                {!entry.isDir && isEditablePath(entry.path) ? (
                  <button
                    type="button"
                    className="entry-edit-button grid-edit-button"
                    aria-label={`Edit ${entry.name}`}
                    title="Open in editor"
                    onClick={(event) => editEntry(entry, event)}
                    onKeyDown={(event) => event.stopPropagation()}
                  >
                    <Pencil size={14} />
                  </button>
                ) : null}
              </div>
            ))}
          </div>
        ) : null}
      </div>
      <div className="pane-footer">
        <span>
          {selectedEntries.length > 0
            ? `${selectedEntries.length} of ${state.entries.length} selected`
            : `${state.entries.length} items`}
        </span>
        <span>{selectedEntries.length > 0 && selectedBytes > 0 ? formatSize(selectedBytes) : ''}</span>
      </div>
    </section>
  )
}

function TransferPanel({
  jobs,
  onCancel,
  onResolve,
}: {
  jobs: TransferJob[]
  onCancel: (id: string) => void
  onResolve: (id: string, action: ConflictAction, applyToAll: boolean) => void
}) {
  const active = jobs.filter((j) =>
    ['queued', 'running', 'cancelling', 'conflict'].includes(j.status),
  )
  const recent = jobs.slice(0, 5)
  if (recent.length === 0) return null

  return (
    <div className="glass transfer-panel" aria-label="Transfers">
      <div className="transfer-panel-header">
        <strong>Transfers</strong>
        <span className="muted">{active.length ? `${active.length} active` : 'Idle'}</span>
      </div>
      <ul className="transfer-list">
        {recent.map((job) => {
          const pct =
            job.bytesTotal > 0 ? Math.min(100, Math.round((100 * job.bytesDone) / job.bytesTotal)) : 0
          const itemCount = job.sourcePaths?.length || 1
          const label = itemCount > 1
            ? `${itemCount} selected items`
            : (job.destName || job.sourcePath || job.id)
          return (
            <li key={job.id}>
              <div className="transfer-row">
                <span className="transfer-name" title={job.sourcePaths?.join('\n') || job.sourcePath}>
                  [{job.kind}] {label}
                </span>
                <span className="muted">{job.status}</span>
                {['queued', 'running', 'conflict', 'cancelling'].includes(job.status) ? (
                  <button
                    type="button"
                    className="icon-btn"
                    aria-label="Cancel transfer"
                    onClick={() => onCancel(job.id)}
                  >
                    <X size={14} />
                  </button>
                ) : null}
              </div>
              {job.status === 'running' || job.status === 'queued' ? (
                <div className="transfer-progress" role="progressbar" aria-valuenow={pct}>
                  <div style={{ width: `${pct}%` }} />
                </div>
              ) : null}
              <div className="transfer-meta muted">
                {formatSize(job.bytesDone)} / {formatSize(job.bytesTotal)} · {formatSpeed(job.bytesPerSecond)}
                {` · ${job.filesDone}/${job.filesTotal} files`}
                {job.currentPath ? ` · ${job.currentPath}` : ''}
                {job.freeSpaceKnown && job.freeSpaceBytes != null
                  ? ` · ${formatSize(job.freeSpaceBytes)} free at start`
                  : ' · free space unknown'}
                {job.errorMessage ? ` · ${job.errorMessage}` : ''}
              </div>
              {job.status === 'conflict' ? (
                <div className="conflict-actions">
                  <span>Conflict: {job.conflictName ?? 'file exists'}</span>
                  <button type="button" onClick={() => onResolve(job.id, 'skip', false)}>
                    Skip
                  </button>
                  <button type="button" onClick={() => onResolve(job.id, 'replace', false)}>
                    Replace
                  </button>
                  <button type="button" onClick={() => onResolve(job.id, 'rename', false)}>
                    Rename
                  </button>
                  <button type="button" onClick={() => onResolve(job.id, 'replace', true)}>
                    Replace all in batch
                  </button>
                </div>
              ) : null}
            </li>
          )
        })}
      </ul>
    </div>
  )
}

export default function App() {
  const [authed, setAuthed] = useState<boolean | null>(null)
  const [username, setUsername] = useState('')
  const [volumes, setVolumes] = useState<Volume[]>([])
  const [left, setLeft] = useState<PaneState>(() => ({ ...emptyPane(), view: 'grid' }))
  const [right, setRight] = useState<PaneState>(() => ({ ...emptyPane(), view: 'list' }))
  const [inspectorOpen, setInspectorOpen] = useState(true)
  const [meta, setMeta] = useState<FileMeta | null>(null)
  const [metaVolumeId, setMetaVolumeId] = useState('')
  const [activePane, setActivePane] = useState<ActivePane>('left')
  const [railMode, setRailMode] = useState<RailMode>('volumes')
  const [favorites, setFavorites] = useState<Favorite[]>([])
  const [recents, setRecents] = useState<RecentLocation[]>([])
  const [jobs, setJobs] = useState<TransferJob[]>([])
  const [copyError, setCopyError] = useState<string | null>(null)
  const [busyTransfer, setBusyTransfer] = useState(false)
  const [previewBroken, setPreviewBroken] = useState(false)
  const [textPreview, setTextPreview] = useState<TextPreview | null>(null)
  const [textPreviewError, setTextPreviewError] = useState<string | null>(null)
  const [textPreviewLoading, setTextPreviewLoading] = useState(false)
  const [editorDocument, setEditorDocument] = useState<EditorDocument | null>(null)
  const [editorVolumeId, setEditorVolumeId] = useState('')
  const [editorModTime, setEditorModTime] = useState('')
  const [editorSaving, setEditorSaving] = useState(false)
  const [editorError, setEditorError] = useState<string | null>(null)
  const [renameTarget, setRenameTarget] = useState<{
    volumeId: string
    path: string
    name: string
  } | null>(null)
  const [renameBusy, setRenameBusy] = useState(false)
  const [renameError, setRenameError] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<{
    volumeId: string
    paths: string[]
    names: string[]
    directoryCount: number
  } | null>(null)
  const [deleteBusy, setDeleteBusy] = useState(false)
  const [deleteError, setDeleteError] = useState<string | null>(null)
  const persistReady = useRef(false)
  const persistTimer = useRef<number | undefined>(undefined)

  const upsertJob = useCallback((job: TransferJob) => {
    setJobs((prev) => {
      const idx = prev.findIndex((j) => j.id === job.id)
      if (idx === -1) return [job, ...prev].slice(0, 50)
      const next = [...prev]
      next[idx] = job
      return next
    })
  }, [])

  const refreshPanes = useCallback(() => {
    setLeft((p) => ({ ...p, reloadToken: p.reloadToken + 1 }))
    setRight((p) => ({ ...p, reloadToken: p.reloadToken + 1 }))
  }, [])

  const refreshVolumes = useCallback(async () => {
    const vols = await listVolumes()
    setVolumes(vols)
    return vols
  }, [])

  const refreshPrefs = useCallback(async () => {
    const [favs, recent] = await Promise.all([listFavorites(), listRecent()])
    setFavorites(favs)
    setRecents(recent)
  }, [])

  const navigatePane = useCallback((pane: ActivePane, volumeId: string, path: string) => {
    const apply = (p: PaneState): PaneState => ({
      ...p,
      volumeId,
      path,
      selected: null,
      selectedPaths: [],
      selectionAnchor: null,
    })
    if (pane === 'left') setLeft(apply)
    else setRight(apply)
    setActivePane(pane)
    setRailMode('volumes')
    void recordRecent(volumeId, path)
      .then(() => listRecent())
      .then(setRecents)
      .catch(() => undefined)
  }, [])

  const selectVolume = useCallback((volumeId: string) => {
    const current = activePane === 'left' ? left : right
    if (current.volumeId === volumeId) {
      if (activePane === 'left') {
        setLeft((pane) => ({ ...pane, reloadToken: pane.reloadToken + 1 }))
      } else {
        setRight((pane) => ({ ...pane, reloadToken: pane.reloadToken + 1 }))
      }
      void refreshVolumes().catch(() => undefined)
      return
    }
    navigatePane(activePane, volumeId, '')
  }, [activePane, left, navigatePane, refreshVolumes, right])

  const bootstrap = useCallback(async () => {
    const session = await getSession()
    if (!session.authenticated) {
      setAuthed(false)
      return
    }
    if (session.csrfToken) setCsrfToken(session.csrfToken)
    setUsername(session.username ?? '')
    const vols = await listVolumes()
    setVolumes(vols)

    let restored = false
    try {
      const paneState = await getPaneState()
      if (paneState) {
        const leftVol = vols.find((v) => v.id === paneState.left.volumeId)?.id ?? vols[0]?.id ?? ''
        const rightVol =
          vols.find((v) => v.id === paneState.right.volumeId)?.id ?? vols[1]?.id ?? vols[0]?.id ?? ''
        setLeft((p) => ({
          ...p,
          volumeId: leftVol,
          path: paneState.left.path || '',
          view: paneState.left.view === 'list' ? 'list' : 'grid',
        }))
        setRight((p) => ({
          ...p,
          volumeId: rightVol,
          path: paneState.right.path || '',
          view: paneState.right.view === 'grid' ? 'grid' : 'list',
        }))
        if (typeof paneState.inspectorOpen === 'boolean') {
          setInspectorOpen(paneState.inspectorOpen)
        }
        if (paneState.activePane === 'left' || paneState.activePane === 'right') {
          setActivePane(paneState.activePane)
        }
        restored = true
      }
    } catch {
      /* ignore corrupt prefs */
    }
    if (!restored) {
      setLeft((p) => ({ ...p, volumeId: vols[0]?.id ?? '', path: '' }))
      setRight((p) => ({ ...p, volumeId: vols[1]?.id ?? vols[0]?.id ?? '', path: '' }))
    }

    try {
      await refreshPrefs()
    } catch {
      setFavorites([])
      setRecents([])
    }
    try {
      setJobs(await listTransfers())
    } catch {
      setJobs([])
    }
    setAuthed(true)
    persistReady.current = true
  }, [refreshPrefs])

  useEffect(() => {
    void bootstrap().catch(() => setAuthed(false))
  }, [bootstrap])

  useEffect(() => {
    const narrow = window.matchMedia('(max-width: 1439px)')
    const apply = () => {
      if (!persistReady.current) setInspectorOpen(!narrow.matches)
    }
    apply()
    narrow.addEventListener('change', apply)
    return () => narrow.removeEventListener('change', apply)
  }, [])

  // Persist pane state (debounced).
  useEffect(() => {
    if (!authed || !persistReady.current) return
    window.clearTimeout(persistTimer.current)
    persistTimer.current = window.setTimeout(() => {
      void savePaneState({
        left: { volumeId: left.volumeId, path: left.path, view: left.view },
        right: { volumeId: right.volumeId, path: right.path, view: right.view },
        inspectorOpen,
        activePane,
      }).catch(() => undefined)
    }, 400)
    return () => window.clearTimeout(persistTimer.current)
  }, [authed, left.volumeId, left.path, left.view, right.volumeId, right.path, right.view, inspectorOpen, activePane])

  // Record recent when path changes on either pane.
  useEffect(() => {
    if (!authed || !left.volumeId || !persistReady.current) return
    void recordRecent(left.volumeId, left.path)
      .then(() => listRecent())
      .then(setRecents)
      .catch(() => undefined)
  }, [authed, left.volumeId, left.path])

  useEffect(() => {
    if (!authed || !right.volumeId || !persistReady.current) return
    void recordRecent(right.volumeId, right.path)
      .then(() => listRecent())
      .then(setRecents)
      .catch(() => undefined)
  }, [authed, right.volumeId, right.path])

  // SSE with reconnect + REST snapshot on open/error recovery.
  useEffect(() => {
    if (!authed) return
    let closed = false
    let stop: (() => void) | null = null
    let retryTimer: number | undefined

    const connect = () => {
      if (closed) return
      stop?.()
      void listTransfers()
        .then((list) => {
          if (!closed) setJobs(list)
        })
        .catch(() => undefined)
      stop = subscribeTransfers((job) => {
        upsertJob(job)
        if (job.status === 'completed' || job.status === 'failed') refreshPanes()
      })
    }

    connect()
    const onVis = () => {
      if (document.visibilityState === 'visible') {
        window.clearTimeout(retryTimer)
        connect()
        void refreshVolumes().catch(() => undefined)
        refreshPanes()
      }
    }
    const onFocus = () => {
      void refreshVolumes().catch(() => undefined)
      refreshPanes()
    }
    document.addEventListener('visibilitychange', onVis)
    window.addEventListener('focus', onFocus)
    retryTimer = window.setInterval(() => {
      void listTransfers()
        .then((list) => {
          if (!closed) setJobs(list)
        })
        .catch(() => undefined)
      void refreshVolumes().catch(() => undefined)
      if (document.visibilityState === 'visible') refreshPanes()
    }, 30000)

    return () => {
      closed = true
      document.removeEventListener('visibilitychange', onVis)
      window.removeEventListener('focus', onFocus)
      window.clearTimeout(retryTimer)
      stop?.()
    }
  }, [authed, upsertJob, refreshPanes, refreshVolumes])

  async function onSelectMeta(volumeId: string, path: string, entry: DirEntry | null) {
    setPreviewBroken(false)
    if (!entry || entry.isDir) {
      setTextPreview(null)
      setTextPreviewError(null)
      setTextPreviewLoading(false)
      setMeta(null)
      setMetaVolumeId('')
      return
    }
    const wantText = isTextPreviewablePath(entry.name)
    setTextPreview(null)
    setTextPreviewError(null)
    // Show the text-preview shell immediately so selection never sticks on the generic icon
    // while stat/preview requests are in flight (or if meta identity has not changed yet).
    setTextPreviewLoading(wantText)
    try {
      const m = await statPath(volumeId, path)
      setMeta(m)
      setMetaVolumeId(volumeId)
      if (!isTextPreviewablePath(m.name)) {
        setTextPreviewLoading(false)
      }
    } catch {
      setMeta(null)
      setMetaVolumeId('')
      setTextPreviewLoading(false)
    }
  }

  const openEditor = useCallback(
    async (volumeId: string, path: string, entry: DirEntry) => {
      if (entry.isDir || entry.isSymlink || !isEditablePath(entry.name)) return
      setEditorError(null)
      try {
        const editable = await fetchEditableFile(volumeId, path)
        const readOnly = Boolean(volumes.find((volume) => volume.id === volumeId)?.readOnly)
        setEditorVolumeId(volumeId)
        setEditorModTime(editable.modTime)
        setEditorDocument({
          name: entry.name,
          path,
          language: editable.language,
          content: editable.content,
          originalContent: editable.content,
          readOnly,
        })
      } catch (error) {
        setCopyError(error instanceof Error ? error.message : 'Could not open editor')
      }
    },
    [volumes],
  )

  const saveEditor = useCallback(async () => {
    if (!editorDocument || !editorVolumeId || editorDocument.readOnly) return
    setEditorSaving(true)
    setEditorError(null)
    try {
      const meta = await saveEditableFile(
        editorVolumeId,
        editorDocument.path,
        editorDocument.content,
        editorModTime,
      )
      setEditorModTime(meta.modTime)
      setEditorDocument((current) =>
        current ? { ...current, originalContent: current.content } : current,
      )
      refreshPanes()
    } catch (error) {
      setEditorError(error instanceof Error ? error.message : 'Save failed')
    } finally {
      setEditorSaving(false)
    }
  }, [editorDocument, editorModTime, editorVolumeId, refreshPanes])

  const openRename = useCallback(() => {
    const pane = activePane === 'left' ? left : right
    const volume = volumes.find((item) => item.id === pane.volumeId)
    if (pane.selectedPaths.length !== 1 || !pane.selected || !pane.volumeId || volume?.readOnly) return
    const entry = pane.entries.find((item) => item.path === pane.selected)
    if (!entry || entry.isSymlink) return
    setRenameError(null)
    setRenameTarget({
      volumeId: pane.volumeId,
      path: entry.path,
      name: entry.name,
    })
  }, [activePane, left, right, volumes])

  const performRename = useCallback(
    async (newName: string) => {
      if (!renameTarget) return
      setRenameBusy(true)
      setRenameError(null)
      try {
        await renameEntry(renameTarget.volumeId, renameTarget.path, newName)
        setRenameTarget(null)
        setMeta(null)
        setMetaVolumeId('')
        refreshPanes()
      } catch (error) {
        setRenameError(error instanceof Error ? error.message : 'Rename failed')
      } finally {
        setRenameBusy(false)
      }
    },
    [refreshPanes, renameTarget],
  )

  const openDelete = useCallback(() => {
    const pane = activePane === 'left' ? left : right
    const volume = volumes.find((item) => item.id === pane.volumeId)
    if (pane.selectedPaths.length === 0 || !pane.volumeId || volume?.readOnly) return
    const entries = pane.entries.filter((item) => pane.selectedPaths.includes(item.path))
    if (entries.length === 0) return
    setDeleteError(null)
    setDeleteTarget({
      volumeId: pane.volumeId,
      paths: entries.map((entry) => entry.path),
      names: entries.map((entry) => entry.name),
      directoryCount: entries.filter((entry) => entry.isDir).length,
    })
  }, [activePane, left, right, volumes])

  const performDelete = useCallback(async () => {
    if (!deleteTarget) return
    setDeleteBusy(true)
    setDeleteError(null)
    try {
      await deleteEntries(deleteTarget.volumeId, deleteTarget.paths)
      setLeft((pane) =>
        pane.volumeId === deleteTarget.volumeId
          ? {
              ...pane,
              selected: null,
              selectedPaths: [],
              selectionAnchor: null,
              reloadToken: pane.reloadToken + 1,
            }
          : { ...pane, reloadToken: pane.reloadToken + 1 },
      )
      setRight((pane) =>
        pane.volumeId === deleteTarget.volumeId
          ? {
              ...pane,
              selected: null,
              selectedPaths: [],
              selectionAnchor: null,
              reloadToken: pane.reloadToken + 1,
            }
          : { ...pane, reloadToken: pane.reloadToken + 1 },
      )
      setMeta(null)
      setMetaVolumeId('')
      setTextPreview(null)
      setDeleteTarget(null)
    } catch (error) {
      setDeleteError(error instanceof Error ? error.message : 'Delete failed')
    } finally {
      setDeleteBusy(false)
    }
  }, [deleteTarget])

  useEffect(() => {
    if (!meta || !metaVolumeId || meta.isDir || meta.isSymlink) {
      setTextPreview(null)
      setTextPreviewError(null)
      setTextPreviewLoading(false)
      return
    }
    if (!isTextPreviewablePath(meta.name)) {
      setTextPreview(null)
      setTextPreviewError(null)
      setTextPreviewLoading(false)
      return
    }
    let cancelled = false
    setTextPreviewLoading(true)
    setTextPreviewError(null)
    void fetchTextPreview(metaVolumeId, meta.path)
      .then((preview) => {
        if (!cancelled) {
          setTextPreview(preview)
          setTextPreviewLoading(false)
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setTextPreview(null)
          setTextPreviewError(err instanceof Error ? err.message : 'Preview failed')
          setTextPreviewLoading(false)
        }
      })
    return () => {
      cancelled = true
    }
  }, [meta, metaVolumeId])

  async function transferSelection(direction: 'ltr' | 'rtl', kind: 'copy' | 'move') {
    setCopyError(null)
    const src = direction === 'ltr' ? left : right
    const dst = direction === 'ltr' ? right : left
    const srcVol = volumes.find((v) => v.id === src.volumeId)
    const dstVol = volumes.find((v) => v.id === dst.volumeId)
    if (src.selectedPaths.length === 0 || !src.volumeId || !dst.volumeId) {
      setCopyError(`Select one or more items to ${kind}`)
      return
    }
    if (dstVol?.readOnly) {
      setCopyError('Destination volume is read-only')
      return
    }
    if (kind === 'move' && srcVol?.readOnly) {
      setCopyError('Source volume is read-only; cannot move')
      return
    }
    const selectedEntries = src.entries.filter((entry) => src.selectedPaths.includes(entry.path))
    if (selectedEntries.length !== src.selectedPaths.length || selectedEntries.some((entry) => entry.isSymlink)) {
      setCopyError(`Cannot ${kind} a selection containing symbolic links`)
      return
    }
    setBusyTransfer(true)
    try {
      const body = {
        sourceVolumeId: src.volumeId,
        sourcePaths: selectedEntries.map((entry) => entry.path),
        destVolumeId: dst.volumeId,
        destDir: dst.path,
        conflictPolicy: 'prompt',
      }
      const job = kind === 'move' ? await createMove(body) : await createCopy(body)
      upsertJob(job)
      const clearSelection = (pane: PaneState): PaneState => ({
        ...pane,
        selected: null,
        selectedPaths: [],
        selectionAnchor: null,
      })
      if (direction === 'ltr') setLeft(clearSelection)
      else setRight(clearSelection)
    } catch (err) {
      setCopyError(err instanceof Error ? err.message : `${kind} failed`)
    } finally {
      setBusyTransfer(false)
    }
  }

  async function copySelection(direction: 'ltr' | 'rtl') {
    await transferSelection(direction, 'copy')
  }

  async function moveSelection(direction: 'ltr' | 'rtl') {
    await transferSelection(direction, 'move')
  }

  const activeState = activePane === 'left' ? left : right

  const favoriteCurrent = useCallback(async () => {
    if (!activeState.volumeId) return
    try {
      await addFavorite(activeState.volumeId, activeState.path)
      await refreshPrefs()
    } catch (err) {
      setCopyError(err instanceof Error ? err.message : 'Could not add favorite')
    }
  }, [activeState.volumeId, activeState.path, refreshPrefs])

  // Keyboard shortcuts
  useEffect(() => {
    if (!authed) return
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null
      if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)) {
        return
      }
      if (editorDocument || renameTarget || deleteTarget) return
      const pane = activePane === 'left' ? left : right
      const setPane = activePane === 'left' ? setLeft : setRight

      if (e.key === 'F5') {
        e.preventDefault()
        void copySelection(activePane === 'left' ? 'ltr' : 'rtl')
        return
      }
      if (e.key === 'F6') {
        e.preventDefault()
        void moveSelection(activePane === 'left' ? 'ltr' : 'rtl')
        return
      }
      if (e.key === 'F2') {
        e.preventDefault()
        openRename()
        return
      }
      if (e.key === 'Delete') {
        e.preventDefault()
        openDelete()
        return
      }
      if ((e.key === 'i' || e.key === 'I') && (e.ctrlKey || e.metaKey)) {
        e.preventDefault()
        setInspectorOpen((v) => !v)
        return
      }
      if ((e.key === 'd' || e.key === 'D') && (e.ctrlKey || e.metaKey)) {
        e.preventDefault()
        void favoriteCurrent()
        return
      }
      if ((e.key === 'a' || e.key === 'A') && (e.ctrlKey || e.metaKey)) {
        e.preventDefault()
        const paths = pane.entries.slice(0, 500).map((entry) => entry.path)
        setPane({
          ...pane,
          selected: paths.at(-1) ?? null,
          selectedPaths: paths,
          selectionAnchor: paths[0] ?? null,
        })
        if (pane.entries.length > 500) {
          setCopyError('The first 500 items were selected; batch operations are limited to 500 items.')
        }
        return
      }
      if (e.key === 'Escape' && pane.selectedPaths.length > 0) {
        e.preventDefault()
        setPane({ ...pane, selected: null, selectedPaths: [], selectionAnchor: null })
        setMeta(null)
        setMetaVolumeId('')
        return
      }
      if (e.key === 'Backspace') {
        e.preventDefault()
        if (!pane.path) return
        const parts = pane.path.split('/').filter(Boolean)
        parts.pop()
        setPane({
          ...pane,
          path: parts.join('/'),
          selected: null,
          selectedPaths: [],
          selectionAnchor: null,
        })
        return
      }
      if (e.key === 'Enter' && pane.selected && pane.selectedPaths.length === 1) {
        e.preventDefault()
        const entry = pane.entries.find((en) => en.path === pane.selected)
        if (entry?.isDir) {
          setPane({
            ...pane,
            path: entry.path,
            selected: null,
            selectedPaths: [],
            selectionAnchor: null,
          })
        }
        return
      }
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault()
        if (pane.entries.length === 0) return
        const idx = pane.selected
          ? pane.entries.findIndex((en) => en.path === pane.selected)
          : -1
        const nextIdx =
          e.key === 'ArrowDown'
            ? Math.min(pane.entries.length - 1, idx + 1)
            : Math.max(0, idx <= 0 ? 0 : idx - 1)
        const entry = pane.entries[nextIdx]
        setPane({
          ...pane,
          selected: entry.path,
          selectedPaths: [entry.path],
          selectionAnchor: entry.path,
        })
        void onSelectMeta(pane.volumeId, entry.path, entry.isDir ? null : entry)
        return
      }
      if ((e.key === 'g' || e.key === 'G') && !e.ctrlKey && !e.metaKey) {
        setPane({ ...pane, view: 'grid' })
        return
      }
      if ((e.key === 'l' || e.key === 'L') && !e.ctrlKey && !e.metaKey) {
        setPane({ ...pane, view: 'list' })
        return
      }
      if ((e.key === 'r' || e.key === 'R') && (e.ctrlKey || e.metaKey)) {
        e.preventDefault()
        void refreshVolumes().then(() => refreshPanes())
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  })

  if (authed === null) {
    return (
      <div className="login-page">
        <p className="muted">Loading…</p>
      </div>
    )
  }

  if (!authed) {
    return (
      <LoginScreen
        onSuccess={() => {
          void bootstrap()
        }}
      />
    )
  }

  const leftVol = volumes.find((v) => v.id === left.volumeId)
  const rightVol = volumes.find((v) => v.id === right.volumeId)
  const canCopyRight = Boolean(left.selectedPaths.length && right.volumeId && !rightVol?.readOnly && !busyTransfer)
  const canCopyLeft = Boolean(right.selectedPaths.length && left.volumeId && !leftVol?.readOnly && !busyTransfer)
  const canMoveRight = Boolean(
    left.selectedPaths.length && right.volumeId && !rightVol?.readOnly && !leftVol?.readOnly && !busyTransfer,
  )
  const canMoveLeft = Boolean(
    right.selectedPaths.length && left.volumeId && !leftVol?.readOnly && !rightVol?.readOnly && !busyTransfer,
  )
  const activeSelected = activePane === 'left' ? left.selected : right.selected
  const activeVolume = activePane === 'left' ? leftVol : rightVol
  const activeEntries = activePane === 'left' ? left.entries : right.entries
  const activeEntry = activeEntries.find((entry) => entry.path === activeSelected)
  const activeSelectionCount = activePane === 'left' ? left.selectedPaths.length : right.selectedPaths.length
  const canRename = Boolean(
    activeSelectionCount === 1 && activeEntry && !activeEntry.isSymlink && !activeVolume?.readOnly,
  )
  const canDelete = Boolean(activeSelectionCount > 0 && !activeVolume?.readOnly)
  const activeCount = jobs.filter((j) =>
    ['queued', 'running', 'cancelling', 'conflict'].includes(j.status),
  ).length

  const showImagePreview =
    meta &&
    metaVolumeId &&
    !meta.isDir &&
    !meta.isSymlink &&
    isImagePath(meta.name) &&
    !previewBroken

  // Gate only on path kind — not on fetch state — so a brief loading/error gap never
  // falls back to the generic file icon after a successful text-previewable selection.
  const showTextPreview =
    meta &&
    metaVolumeId &&
    !meta.isDir &&
    !meta.isSymlink &&
    isTextPreviewablePath(meta.name)

  const currentFav = favorites.find(
    (f) => f.volumeId === activeState.volumeId && f.path === activeState.path,
  )

  return (
    <>
    <div className="app-shell">
      <nav className="glass rail" aria-label="Locations">
        <div className="rail-brand" aria-hidden>
          <img src="/app-icon.svg" alt="" />
        </div>
        <button
          type="button"
          className={railMode === 'volumes' ? 'active' : ''}
          aria-label="Volumes"
          title="Volumes"
          onClick={() => setRailMode('volumes')}
        >
          <HardDrive size={18} />
          Vols
        </button>
        <button
          type="button"
          className={railMode === 'favorites' ? 'active' : ''}
          aria-label="Favorites"
          title="Favorites (Ctrl/Cmd+D to bookmark current folder)"
          onClick={() => setRailMode('favorites')}
        >
          <Star size={18} />
          Fav
        </button>
        <button
          type="button"
          className={railMode === 'recent' ? 'active' : ''}
          aria-label="Recent locations"
          title="Recent"
          onClick={() => setRailMode('recent')}
        >
          <Clock size={18} />
          Recent
        </button>
        <div className="rail-spacer" />
        <div className="rail-status">
          {activeCount ? `${activeCount} xfer` : 'Idle'}
          <br />
          {username}
        </div>
        <button
          type="button"
          aria-label="Sign out"
          onClick={() => {
            void logout().then(() => {
              setAuthed(false)
              setVolumes([])
              setJobs([])
              persistReady.current = false
            })
          }}
        >
          <LogOut size={18} />
          Out
        </button>
      </nav>

      <aside className="glass side-panel" aria-label="Location browser">
        <div className="side-panel-header">
          <strong>
            {railMode === 'volumes' ? 'Volumes' : railMode === 'favorites' ? 'Favorites' : 'Recent'}
          </strong>
          {railMode === 'favorites' ? (
            <button
              type="button"
              className="icon-btn"
              aria-label={currentFav ? 'Remove favorite' : 'Add favorite'}
              title={currentFav ? 'Remove favorite' : 'Favorite current folder'}
              onClick={() => {
                if (currentFav) {
                  void removeFavorite(currentFav.id).then(refreshPrefs).catch(() => undefined)
                } else {
                  void favoriteCurrent()
                }
              }}
            >
              {currentFav ? <StarOff size={14} /> : <Star size={14} />}
            </button>
          ) : null}
          {railMode === 'recent' ? (
            <button
              type="button"
              className="text-btn"
              onClick={() => void clearRecent().then(() => setRecents([]))}
            >
              Clear
            </button>
          ) : null}
          {railMode === 'volumes' ? (
            <button
              type="button"
              className="text-btn"
              title="Refresh availability (Ctrl/Cmd+R)"
              onClick={() => void refreshVolumes().then(() => refreshPanes())}
            >
              Refresh
            </button>
          ) : null}
        </div>
        <ul className="side-list">
          {railMode === 'volumes'
            ? volumes.map((v) => (
                <li key={v.id}>
                  <button
                    type="button"
                    className={left.volumeId === v.id || right.volumeId === v.id ? 'active' : ''}
                    onClick={() => selectVolume(v.id)}
                  >
                    <HardDrive size={14} />
                    <span className="side-label">
                      {v.name}
                      {!v.available ? <em> · offline</em> : null}
                      {v.readOnly ? <em> · RO</em> : null}
                    </span>
                  </button>
                </li>
              ))
            : null}
          {railMode === 'favorites'
            ? favorites.map((f) => (
                <li key={f.id}>
                  <button type="button" onClick={() => navigatePane(activePane, f.volumeId, f.path)}>
                    <Star size={14} />
                    <span className="side-label">{f.label || f.path || f.volumeId}</span>
                  </button>
                </li>
              ))
            : null}
          {railMode === 'recent'
            ? recents.map((r) => (
                <li key={`${r.volumeId}:${r.path}`}>
                  <button type="button" onClick={() => navigatePane(activePane, r.volumeId, r.path)}>
                    <Clock size={14} />
                    <span className="side-label">
                      {volumes.find((v) => v.id === r.volumeId)?.name ?? r.volumeId}
                      {r.path ? ` / ${r.path}` : ' /'}
                    </span>
                  </button>
                </li>
              ))
            : null}
          {railMode === 'favorites' && favorites.length === 0 ? (
            <li className="muted side-empty">No favorites yet. Press Ctrl/Cmd+D.</li>
          ) : null}
          {railMode === 'recent' && recents.length === 0 ? (
            <li className="muted side-empty">No recent folders yet.</li>
          ) : null}
        </ul>
      </aside>

      <Pane
        label="Left pane"
        paneId="left"
        active={activePane === 'left'}
        volumes={volumes}
        state={left}
        onChange={setLeft}
        onSelectMeta={onSelectMeta}
        onOpenFile={(volumeId, path, entry) => void openEditor(volumeId, path, entry)}
        onActivate={() => setActivePane('left')}
      />

      <div className="glass transfer-strip" aria-label="Transfer controls">
        <button
          type="button"
          disabled={!canCopyRight}
          aria-label="Copy selection to right pane"
          title="Copy → (F5)"
          onClick={() => void copySelection('ltr')}
        >
          <ChevronRight size={16} />
        </button>
        <button
          type="button"
          disabled={!canCopyLeft}
          aria-label="Copy selection to left pane"
          title="← Copy (F5)"
          onClick={() => void copySelection('rtl')}
        >
          <ChevronRight size={16} style={{ transform: 'rotate(180deg)' }} />
        </button>
        <button
          type="button"
          disabled={!canMoveRight}
          aria-label="Move selection to right pane"
          title="Move → (F6)"
          onClick={() => void moveSelection('ltr')}
        >
          <ArrowLeftRight size={14} />
        </button>
        <button
          type="button"
          disabled={!canMoveLeft}
          aria-label="Move selection to left pane"
          title="← Move (F6)"
          onClick={() => void moveSelection('rtl')}
        >
          <ArrowLeftRight size={14} style={{ transform: 'scaleX(-1)' }} />
        </button>
        <button
          type="button"
          disabled={!canRename}
          aria-label="Rename selection"
          title="Rename (F2)"
          onClick={openRename}
        >
          <Pencil size={14} />
        </button>
        <button
          type="button"
          className="delete-tool"
          disabled={!canDelete}
          aria-label="Delete selection"
          title="Delete permanently (Delete)"
          onClick={openDelete}
        >
          <Trash2 size={14} />
        </button>
        <button
          type="button"
          className="icon-btn"
          style={{ opacity: 1, cursor: 'pointer', width: 28, height: 28 }}
          aria-label={inspectorOpen ? 'Hide inspector' : 'Show inspector'}
          title="Toggle inspector (Ctrl/Cmd+I)"
          onClick={() => setInspectorOpen((v) => !v)}
        >
          {inspectorOpen ? <PanelRightClose size={14} /> : <PanelRightOpen size={14} />}
        </button>
      </div>

      <Pane
        label="Right pane"
        paneId="right"
        active={activePane === 'right'}
        volumes={volumes}
        state={right}
        onChange={setRight}
        onSelectMeta={onSelectMeta}
        onOpenFile={(volumeId, path, entry) => void openEditor(volumeId, path, entry)}
        onActivate={() => setActivePane('right')}
      />

      {inspectorOpen ? (
        <aside className="glass inspector" aria-label="Inspector">
          <div className="inspector-header">
            <h3>Inspector</h3>
            <button
              type="button"
              className="icon-btn"
              aria-label="Close inspector"
              onClick={() => setInspectorOpen(false)}
            >
              <PanelRightClose size={16} />
            </button>
          </div>
          <div className="inspector-body">
            {meta ? (
              <>
                {showImagePreview ? (
                  <div className="inspector-preview">
                    <img
                      src={previewURL(metaVolumeId, meta.path)}
                      alt={meta.name}
                      onError={() => setPreviewBroken(true)}
                    />
                  </div>
                ) : showTextPreview ? (
                  <div className="inspector-text-preview" aria-label="File preview">
                    {textPreviewLoading ? (
                      <p className="muted">Loading preview…</p>
                    ) : textPreviewError ? (
                      <p className="login-error">{textPreviewError}</p>
                    ) : textPreview ? (
                      <>
                        {textPreview.truncated ? (
                          <p className="preview-truncated" role="status">
                            Preview truncated to first {formatSize(textPreview.bytesRead)} of{' '}
                            {formatSize(textPreview.fileSize)}.
                          </p>
                        ) : null}
                        {textPreview.kind === 'markdown' ? (
                          <div
                            className="preview-markdown"
                            dangerouslySetInnerHTML={{
                              __html: renderSafeMarkdown(textPreview.text),
                            }}
                          />
                        ) : (
                          <pre
                            className={
                              textPreview.kind === 'text' || textPreview.kind === 'docx'
                                ? 'preview-prose'
                                : 'preview-code'
                            }
                          >
                            <code>{textPreview.text}</code>
                          </pre>
                        )}
                      </>
                    ) : (
                      <p className="muted">Loading preview…</p>
                    )}
                  </div>
                ) : (
                  <div className="grid-icon" style={{ width: 72, height: 72 }}>
                    {isImagePath(meta.name) ? (
                      <ImageIcon size={28} />
                    ) : isTextPreviewablePath(meta.name) ? (
                      <FileText size={28} />
                    ) : (
                      <File size={28} />
                    )}
                  </div>
                )}
                <strong>{meta.name}</strong>
                <dl>
                  <dt>Type</dt>
                  <dd>{meta.isDir ? 'Folder' : meta.isSymlink ? 'Symbolic link' : 'File'}</dd>
                  <dt>Size</dt>
                  <dd>{meta.isDir ? '—' : formatSize(meta.size)}</dd>
                  <dt>Modified</dt>
                  <dd>{formatDate(meta.modTime)}</dd>
                  <dt>Mode</dt>
                  <dd>{meta.mode}</dd>
                  <dt>Path</dt>
                  <dd>{meta.path || '/'}</dd>
                </dl>
              </>
            ) : (
              <p className="empty-inspector">Select a file to inspect details.</p>
            )}
            <TransferPanel
              jobs={jobs}
              onCancel={(id) => {
                void cancelTransfer(id).then(upsertJob).catch(() => undefined)
              }}
              onResolve={(id, action, applyToAll) => {
                void resolveConflict(id, action, applyToAll)
                  .then(upsertJob)
                  .catch(() => undefined)
              }}
            />
            {copyError ? <p className="login-error">{copyError}</p> : null}
            <p className="muted shortcuts-hint">
              Cmd/Ctrl+click toggle · Shift+click range · Cmd/Ctrl+A select all · Esc clear ·
              F2 rename · Delete remove · F5 copy · F6 move · Ctrl/Cmd+D favorite
            </p>
          </div>
        </aside>
      ) : (
        <div className="inspector collapsed-auto" hidden />
      )}
    </div>
      {renameTarget ? (
        <RenameModal
          currentName={renameTarget.name}
          busy={renameBusy}
          error={renameError}
          onRename={(newName) => void performRename(newName)}
          onClose={() => {
            if (!renameBusy) setRenameTarget(null)
          }}
        />
      ) : null}
      {deleteTarget ? (
        <DeleteModal
          names={deleteTarget.names}
          directoryCount={deleteTarget.directoryCount}
          busy={deleteBusy}
          error={deleteError}
          onDelete={() => void performDelete()}
          onClose={() => {
            if (!deleteBusy) setDeleteTarget(null)
          }}
        />
      ) : null}
      {editorDocument ? (
        <Suspense fallback={<div className="modal-backdrop"><div className="editor-loading">Starting editor…</div></div>}>
          <EditorModal
            document={editorDocument}
            saving={editorSaving}
            error={editorError}
            onChange={(content) =>
              setEditorDocument((current) => (current ? { ...current, content } : current))
            }
            onSave={() => void saveEditor()}
            onClose={() => {
              setEditorDocument(null)
              setEditorError(null)
            }}
          />
        </Suspense>
      ) : null}
    </>
  )
}
