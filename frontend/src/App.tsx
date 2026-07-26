import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import {
  ArrowLeftRight,
  CheckSquare2,
  ChevronDown,
  ChevronRight,
  Clock,
  File,
  FileText,
  Folder,
  FolderUp,
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
  Upload,
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
  uploadFile,
  collectDroppedEntry,
  relativeDirOf,
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
  PickedFile,
  TransferJob,
  UploadConflictAction,
  Volume,
} from './api'

const EditorModal = lazy(() => import('./EditorModal'))

type ViewMode = 'list' | 'grid'
const LIST_ROW_HEIGHT = 38
const GRID_ROW_HEIGHT = 146
const GRID_MIN_COLUMN_WIDTH = 160
const GRID_GAP = 14
const VIRTUAL_OVERSCAN_ROWS = 8
// Above this many files a drop is confirmed first; a dropped node_modules is 40k+.
const LARGE_UPLOAD_CONFIRM = 25
type ActivePane = 'left' | 'right'
type SectionKey = 'volumes' | 'favorites' | 'recent'

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
            <h1>DirDeck</h1>
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
  const [attempt, setAttempt] = useState(0)
  const retryTimer = useRef<number | null>(null)
  useEffect(() => {
    setFailed(false)
    setAttempt(0)
    return () => {
      if (retryTimer.current !== null) window.clearTimeout(retryTimer.current)
    }
  }, [volumeId, entry.path])
  if (!enabled || entry.isDir || entry.isSymlink || !isImagePath(entry.name) || failed) {
    return <div className="grid-icon">{entryIcon(entry)}</div>
  }
  return (
    <div className="grid-icon thumb">
      <img
        src={`${thumbnailURL(volumeId, entry.path)}&attempt=${attempt}`}
        alt=""
        loading="lazy"
        onError={() => {
          if (attempt >= 3) {
            setFailed(true)
            return
          }
          retryTimer.current = window.setTimeout(
            () => setAttempt((value) => value + 1),
            250 * 2 ** attempt,
          )
        }}
      />
    </div>
  )
}

type UploadItem = {
  id: string
  name: string
  subDir: string
  loaded: number
  total: number
  status: 'pending' | 'uploading' | 'done' | 'skipped' | 'cancelled' | 'error'
  error?: string
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
  otherPaneName,
  canCopySelection,
  canMoveSelection,
  canRenameSelection,
  canDeleteSelection,
  onCopySelection,
  onMoveSelection,
  onRenameSelection,
  onDeleteSelection,
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
  otherPaneName: string
  canCopySelection: boolean
  canMoveSelection: boolean
  canRenameSelection: boolean
  canDeleteSelection: boolean
  onCopySelection: () => void
  onMoveSelection: () => void
  onRenameSelection: () => void
  onDeleteSelection: () => void
}) {
  const [contextMenu, setContextMenu] = useState<{
    x: number
    y: number
    entry: DirEntry
  } | null>(null)
  const [uploads, setUploads] = useState<UploadItem[]>([])
  const [dragActive, setDragActive] = useState(false)
  const [dropNotice, setDropNotice] = useState<string | null>(null)
  const [uploadConflict, setUploadConflict] = useState<{ name: string; resolve: (a: UploadConflictAction | 'cancel', all: boolean) => void } | null>(null)
  const uploadAllPolicy = useRef<UploadConflictAction | null>(null)
  const uploadAbort = useRef<(() => void) | null>(null)
  const stopAllUploads = useRef(false)
  const [confirmBatch, setConfirmBatch] = useState<{
    count: number
    bytes: number
    resolve: (go: boolean) => void
  } | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const folderInputRef = useRef<HTMLInputElement>(null)
  const paneBodyRef = useRef<HTMLDivElement>(null)
  const scrollFrameRef = useRef<number | null>(null)
  const [viewport, setViewport] = useState({ scrollTop: 0, width: 0, height: 0 })
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
    const element = paneBodyRef.current
    if (!element) return
    const updateSize = () => setViewport((current) => ({
      ...current,
      width: element.clientWidth,
      height: element.clientHeight,
    }))
    updateSize()
    const observer = new ResizeObserver(updateSize)
    observer.observe(element)
    return () => observer.disconnect()
  }, [])

  useEffect(() => {
    const element = paneBodyRef.current
    if (element) element.scrollTop = 0
    setViewport((current) => ({ ...current, scrollTop: 0 }))
  }, [state.volumeId, state.path, state.view])

  useEffect(() => () => {
    if (scrollFrameRef.current !== null) cancelAnimationFrame(scrollFrameRef.current)
  }, [])

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

  useEffect(() => {
    if (!contextMenu) return
    const close = () => setContextMenu(null)
    const closeOnKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') close()
    }
    document.addEventListener('mousedown', close)
    window.addEventListener('blur', close)
    window.addEventListener('resize', close)
    window.addEventListener('scroll', close, true)
    document.addEventListener('keydown', closeOnKey)
    return () => {
      document.removeEventListener('mousedown', close)
      window.removeEventListener('blur', close)
      window.removeEventListener('resize', close)
      window.removeEventListener('scroll', close, true)
      document.removeEventListener('keydown', closeOnKey)
    }
  }, [contextMenu])

  function selectEntry(entry: DirEntry, event?: React.MouseEvent, forceToggle = false) {
    const toggle = forceToggle || Boolean(event?.metaKey || event?.ctrlKey)
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

  function toggleEntrySelection(entry: DirEntry, event: React.MouseEvent) {
    event.preventDefault()
    event.stopPropagation()
    onActivate()
    selectEntry(entry, event, true)
  }

  function editEntry(entry: DirEntry, event: React.MouseEvent | React.KeyboardEvent) {
    event.preventDefault()
    event.stopPropagation()
    onOpenFile(state.volumeId, entry.path, entry)
  }

  function openContextMenu(entry: DirEntry, event: React.MouseEvent) {
    event.preventDefault()
    onActivate()
    if (!state.selectedPaths.includes(entry.path)) selectEntry(entry)
    const menuWidth = 240
    const menuHeight = 300
    setContextMenu({
      x: Math.max(8, Math.min(event.clientX, window.innerWidth - menuWidth - 8)),
      y: Math.max(8, Math.min(event.clientY, window.innerHeight - menuHeight - 8)),
      entry,
    })
  }

  function runContextAction(action: () => void) {
    setContextMenu(null)
    action()
  }

  function copyRelativePath(entry: DirEntry) {
    const text = entry.path
    const fallback = () => {
      const field = document.createElement('textarea')
      field.value = text
      field.style.position = 'fixed'
      field.style.opacity = '0'
      document.body.appendChild(field)
      field.select()
      document.execCommand('copy')
      field.remove()
    }
    if (navigator.clipboard?.writeText) {
      void navigator.clipboard.writeText(text).catch(fallback)
    } else {
      fallback()
    }
  }

  const selectedEntries = state.entries.filter((entry) => state.selectedPaths.includes(entry.path))
  const selectedBytes = selectedEntries.reduce(
    (total, entry) => total + (entry.isDir ? 0 : entry.size),
    0,
  )
  const selectableCount = Math.min(500, state.entries.length)
  const listStart = Math.max(
    0,
    Math.floor(Math.max(0, viewport.scrollTop - 32) / LIST_ROW_HEIGHT) - VIRTUAL_OVERSCAN_ROWS,
  )
  const listEnd = Math.min(
    state.entries.length,
    listStart + Math.ceil(viewport.height / LIST_ROW_HEIGHT) + VIRTUAL_OVERSCAN_ROWS * 2,
  )
  const visibleListEntries = state.entries.slice(listStart, listEnd)
  const gridContentWidth = Math.max(GRID_MIN_COLUMN_WIDTH, viewport.width - 16)
  const gridColumns = Math.max(
    1,
    Math.floor((gridContentWidth + GRID_GAP) / (GRID_MIN_COLUMN_WIDTH + GRID_GAP)),
  )
  const gridRows = Math.ceil(state.entries.length / gridColumns)
  const gridStartRow = Math.max(
    0,
    Math.floor(viewport.scrollTop / GRID_ROW_HEIGHT) - VIRTUAL_OVERSCAN_ROWS,
  )
  const gridEndRow = Math.min(
    gridRows,
    gridStartRow + Math.ceil(viewport.height / GRID_ROW_HEIGHT) + VIRTUAL_OVERSCAN_ROWS * 2,
  )
  const visibleGridEntries = state.entries.slice(
    gridStartRow * gridColumns,
    Math.min(state.entries.length, gridEndRow * gridColumns),
  )

  const writable = Boolean(volume && !volume.readOnly && volume.available)

  // Uploads run one at a time. A parallel queue would saturate a NAS link and
  // makes per-file progress meaningless.
  const runUploads = useCallback(async (picked: PickedFile[]) => {
    if (!writable || picked.length === 0) return

    // A dropped node_modules is 40k+ files. Confirm before committing to a queue
    // that would take hours, and say how big it actually is.
    if (picked.length > LARGE_UPLOAD_CONFIRM) {
      const bytes = picked.reduce((sum, p) => sum + p.file.size, 0)
      const go = await new Promise<boolean>((resolve) => {
        setConfirmBatch({ count: picked.length, bytes, resolve })
      })
      setConfirmBatch(null)
      if (!go) return
    }

    uploadAllPolicy.current = null
    stopAllUploads.current = false
    const queued: UploadItem[] = picked.map((p, i) => ({
      id: `${Date.now()}-${i}-${p.subDir}/${p.file.name}`,
      name: p.file.name,
      subDir: p.subDir,
      loaded: 0,
      total: p.file.size,
      status: 'pending',
    }))
    setUploads(queued)

    for (let i = 0; i < picked.length; i += 1) {
      if (stopAllUploads.current) {
        // Mark everything still waiting, so the strip does not look stalled.
        setUploads((list) =>
          list.map((u) =>
            u.status === 'pending' || u.status === 'uploading'
              ? { ...u, status: 'cancelled', error: 'stopped' }
              : u,
          ),
        )
        break
      }
      const file = picked[i].file
      const subDir = picked[i].subDir
      const item = queued[i]
      const patch = (fields: Partial<UploadItem>) =>
        setUploads((list) => list.map((u) => (u.id === item.id ? { ...u, ...fields } : u)))

      let conflictAction: UploadConflictAction | undefined = uploadAllPolicy.current ?? undefined
      for (;;) {
        patch({ status: 'uploading' })
        try {
          const { promise, abort } = uploadFile(state.volumeId, state.path, file, {
            conflict: conflictAction,
            subDir,
            onProgress: (loaded, total) => patch({ loaded, total }),
          })
          uploadAbort.current = abort
          const result = await promise
          uploadAbort.current = null

          if (result.conflict) {
            const decision = await new Promise<{ action: UploadConflictAction | 'cancel'; all: boolean }>(
              (resolve) => {
                setUploadConflict({
                  name: file.name,
                  resolve: (action, all) => {
                    setUploadConflict(null)
                    resolve({ action, all })
                  },
                })
              },
            )
            if (decision.action === 'cancel') {
              patch({ status: 'error', error: 'cancelled' })
              break
            }
            if (decision.all) uploadAllPolicy.current = decision.action
            conflictAction = decision.action
            continue
          }
          patch({ status: result.skipped ? 'skipped' : 'done', loaded: file.size })
        } catch (err) {
          uploadAbort.current = null
          const message = err instanceof Error ? err.message : 'upload failed'
          patch({
            status: message === 'upload cancelled' ? 'cancelled' : 'error',
            error: message === 'upload cancelled' ? 'cancelled' : message,
          })
        }
        break
      }
    }

    onChange({ ...state, reloadToken: state.reloadToken + 1 })
    window.setTimeout(() => setUploads((list) => (list.every((u) => u.status !== 'uploading') ? [] : list)), 2500)
  }, [writable, state, onChange])

  function onDragOver(event: React.DragEvent) {
    if (!writable || !Array.from(event.dataTransfer.types).includes('Files')) return
    event.preventDefault()
    event.dataTransfer.dropEffect = 'copy'
    setDragActive(true)
  }

  function onDragLeave(event: React.DragEvent) {
    if (event.currentTarget.contains(event.relatedTarget as Node)) return
    setDragActive(false)
  }

  function onDrop(event: React.DragEvent) {
    if (!writable) return
    event.preventDefault()
    setDragActive(false)
    onActivate()

    // Entries must be captured synchronously: the DataTransfer is invalidated
    // once the event handler returns, so grab them before any await.
    const entries: FileSystemEntry[] = []
    const plainFiles: File[] = []
    const items = Array.from(event.dataTransfer.items ?? [])
    if (items.length > 0 && typeof items[0].webkitGetAsEntry === 'function') {
      for (const item of items) {
        if (item.kind !== 'file') continue
        const entry = item.webkitGetAsEntry()
        if (entry) entries.push(entry)
        else {
          const file = item.getAsFile()
          if (file) plainFiles.push(file)
        }
      }
    } else {
      plainFiles.push(...Array.from(event.dataTransfer.files))
    }

    void (async () => {
      const picked: PickedFile[] = plainFiles.map((file) => ({ file, subDir: '' }))
      try {
        setDropNotice(entries.some((e) => e.isDirectory) ? 'Reading dropped folders…' : null)
        for (const entry of entries) {
          await collectDroppedEntry(entry, '', picked)
        }
      } catch {
        setDropNotice('Could not read one of the dropped folders.')
        return
      }
      const folders = new Set(picked.filter((p) => p.subDir).map((p) => p.subDir.split('/')[0]))
      setDropNotice(
        folders.size > 0
          ? `${picked.length} file${picked.length > 1 ? 's' : ''} from ${folders.size} folder${folders.size > 1 ? 's' : ''}.`
          : null,
      )
      await runUploads(picked)
    })()
  }

  function handlePaneScroll(event: React.UIEvent<HTMLDivElement>) {
    const scrollTop = event.currentTarget.scrollTop
    if (scrollFrameRef.current !== null) cancelAnimationFrame(scrollFrameRef.current)
    scrollFrameRef.current = requestAnimationFrame(() => {
      setViewport((current) => ({ ...current, scrollTop }))
      scrollFrameRef.current = null
    })
  }

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
            className="upload-btn"
            aria-label="Upload files"
            title={writable ? 'Upload files into this folder' : 'Volume is read-only'}
            disabled={!writable}
            onClick={() => fileInputRef.current?.click()}
          >
            <Upload size={15} aria-hidden />
            <span>Upload</span>
          </button>
          <button
            type="button"
            className="upload-btn"
            aria-label="Upload a folder"
            title={writable ? 'Upload a folder and its contents' : 'Volume is read-only'}
            disabled={!writable}
            onClick={() => folderInputRef.current?.click()}
          >
            <FolderUp size={15} aria-hidden />
            <span>Folder</span>
          </button>
          <input
            ref={fileInputRef}
            type="file"
            multiple
            hidden
            onChange={(event) => {
              const files = Array.from(event.target.files ?? [])
              event.target.value = ''
              void runUploads(files.map((file) => ({ file, subDir: relativeDirOf(file) })))
            }}
          />
          <input
            ref={folderInputRef}
            type="file"
            multiple
            hidden
            // webkitdirectory has no typed JSX attribute; the picker needs it set.
            {...{ webkitdirectory: '', directory: '' }}
            onChange={(event) => {
              const files = Array.from(event.target.files ?? [])
              event.target.value = ''
              void runUploads(files.map((file) => ({ file, subDir: relativeDirOf(file) })))
            }}
          />
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
      <div
        ref={paneBodyRef}
        className={`pane-body${dragActive ? ' drop-active' : ''}`}
        onScroll={handlePaneScroll}
        onDragOver={onDragOver}
        onDragLeave={onDragLeave}
        onDrop={onDrop}
      >
        {dragActive ? (
          <div className="drop-hint" role="status">
            <Upload size={22} aria-hidden />
            <span>Drop files into {state.path || volume?.name}</span>
          </div>
        ) : null}
        {dropNotice ? (
          <div className="drop-notice" role="status">
            <span>{dropNotice}</span>
            <button type="button" className="text-btn" onClick={() => setDropNotice(null)}>
              Dismiss
            </button>
          </div>
        ) : null}
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
        {!state.loading && !state.error && state.entries.length === 0 && !dragActive ? (
          <div className={`empty-folder${writable ? ' empty-folder-droppable' : ''}`}>
            {writable ? (
              <>
                <Upload size={26} aria-hidden />
                <p>Drop files here to upload</p>
                <button type="button" className="text-btn" onClick={() => fileInputRef.current?.click()}>
                  or choose files
                </button>
              </>
            ) : (
              <p className="muted">This folder is empty.</p>
            )}
          </div>
        ) : null}
        {!state.loading && !state.error && state.view === 'list' ? (
          <table className="list-table">
            <thead>
              <tr>
                <th className="entry-select-heading" aria-label="Selection" />
                <th className="entry-name-heading">Name</th>
                <th className="entry-date-heading">Date modified</th>
                <th className="entry-size-heading">Size</th>
                <th className="entry-action-heading" aria-label="Actions" />
              </tr>
            </thead>
            <tbody>
              {listStart > 0 ? (
                <tr className="virtual-spacer" aria-hidden="true">
                  <td colSpan={5} style={{ height: listStart * LIST_ROW_HEIGHT }} />
                </tr>
              ) : null}
              {visibleListEntries.map((entry) => (
                <tr
                  key={entry.path}
                  className={state.selectedPaths.includes(entry.path) ? 'selected' : ''}
                  tabIndex={0}
                  aria-selected={state.selectedPaths.includes(entry.path)}
                  onClick={(event) => handleEntryClick(entry, event)}
                  onContextMenu={(event) => openContextMenu(entry, event)}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter' && entry.isDir) {
                      event.preventDefault()
                      activateEntry(entry)
                    }
                  }}
                >
                  <td className="entry-select-cell">
                    <button
                      type="button"
                      className="entry-select-button"
                      aria-label={`${state.selectedPaths.includes(entry.path) ? 'Deselect' : 'Select'} ${entry.name}`}
                      aria-pressed={state.selectedPaths.includes(entry.path)}
                      title="Select item (Shift-click for a range)"
                      onClick={(event) => toggleEntrySelection(entry, event)}
                      onKeyDown={(event) => event.stopPropagation()}
                    >
                      {state.selectedPaths.includes(entry.path)
                        ? <CheckSquare2 size={16} />
                        : <Square size={16} />}
                    </button>
                  </td>
                  <td>
                    <span className="row-name">
                      {entryIcon(entry)}
                      <span className="row-name-text" title={entry.name}>{entry.name}</span>
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
              {listEnd < state.entries.length ? (
                <tr className="virtual-spacer" aria-hidden="true">
                  <td
                    colSpan={5}
                    style={{ height: (state.entries.length - listEnd) * LIST_ROW_HEIGHT }}
                  />
                </tr>
              ) : null}
            </tbody>
          </table>
        ) : null}
        {!state.loading && !state.error && state.view === 'grid' ? (
          <div
            className="grid-virtual"
            style={{ height: Math.max(0, gridRows * GRID_ROW_HEIGHT - GRID_GAP) }}
          >
            <div
              className="grid grid-window"
              style={{ top: gridStartRow * GRID_ROW_HEIGHT }}
            >
              {visibleGridEntries.map((entry) => (
                <div
                  key={entry.path}
                  className={`grid-item ${state.selectedPaths.includes(entry.path) ? 'selected' : ''}`}
                  role="button"
                  tabIndex={0}
                  aria-pressed={state.selectedPaths.includes(entry.path)}
                  onClick={(event) => handleEntryClick(entry, event)}
                  onContextMenu={(event) => openContextMenu(entry, event)}
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
                  <button
                    type="button"
                    className="entry-select-button grid-select-button"
                    aria-label={`${state.selectedPaths.includes(entry.path) ? 'Deselect' : 'Select'} ${entry.name}`}
                    aria-pressed={state.selectedPaths.includes(entry.path)}
                    title="Select item (Shift-click for a range)"
                    onClick={(event) => toggleEntrySelection(entry, event)}
                    onKeyDown={(event) => event.stopPropagation()}
                  >
                    {state.selectedPaths.includes(entry.path)
                      ? <CheckSquare2 size={16} />
                      : <Square size={16} />}
                  </button>
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
          </div>
        ) : null}
      </div>
      {uploads.length > 0 ? (
        <div className="upload-strip" role="status" aria-label="Upload progress">
          {uploads.length <= 6 && uploads.some((u) => u.status === 'pending' || u.status === 'uploading') ? (
            <div className="upload-summary">
              <span>Uploading {uploads.length} file{uploads.length > 1 ? 's' : ''}</span>
              <button
                type="button"
                className="dialog-danger upload-stop"
                onClick={() => {
                  stopAllUploads.current = true
                  uploadAbort.current?.()
                }}
              >
                Stop all
              </button>
            </div>
          ) : null}
          {uploads.length > 6 ? (
            <div className="upload-summary">
              <span>
                {uploads.filter((u) => u.status === 'done' || u.status === 'skipped').length} of{' '}
                {uploads.length} uploaded
              </span>
              {uploads.some((u) => u.status === 'error') ? (
                <span className="upload-failed">
                  {uploads.filter((u) => u.status === 'error').length} failed
                </span>
              ) : null}
              {uploads.some((u) => u.status === 'pending' || u.status === 'uploading') ? (
                <button
                  type="button"
                  className="dialog-danger upload-stop"
                  onClick={() => {
                    stopAllUploads.current = true
                    uploadAbort.current?.()
                  }}
                >
                  Stop all
                </button>
              ) : null}
            </div>
          ) : null}
          {(uploads.length > 6
            ? uploads
                .filter((u) => u.status === 'uploading' || u.status === 'error')
                .slice(0, 6)
            : uploads
          ).map((u) => (
            <div key={u.id} className={`upload-row upload-${u.status}`}>
              <span className="upload-name" title={u.subDir ? `${u.subDir}/${u.name}` : u.name}>
                {u.subDir ? `${u.subDir}/${u.name}` : u.name}
              </span>
              {u.status === 'uploading' ? (
                <span className="upload-bar">
                  <span
                    className="upload-bar-fill"
                    style={{ width: `${u.total ? Math.round((u.loaded / u.total) * 100) : 0}%` }}
                  />
                </span>
              ) : null}
              <span className="upload-status">
                {u.status === 'done'
                  ? 'done'
                  : u.status === 'skipped'
                    ? 'skipped'
                    : u.status === 'cancelled'
                    ? 'cancelled'
                    : u.status === 'error'
                      ? (u.error ?? 'failed')
                      : u.status === 'uploading'
                        ? `${formatSize(u.loaded)} / ${formatSize(u.total)}`
                        : 'queued'}
              </span>
              {u.status === 'uploading' ? (
                <button type="button" className="text-btn" onClick={() => uploadAbort.current?.()}>
                  Cancel
                </button>
              ) : null}
            </div>
          ))}
        </div>
      ) : null}
      {confirmBatch ? (
        <div className="upload-conflict" role="alertdialog" aria-label="Confirm large upload">
          <strong>
            Upload {confirmBatch.count.toLocaleString()} files ({formatSize(confirmBatch.bytes)})?
          </strong>
          <span className="muted">
            Files upload one at a time. A queue this size can take a long time; you can stop it
            at any point and keep whatever finished.
          </span>
          <div className="upload-conflict-actions">
            <button type="button" onClick={() => confirmBatch.resolve(true)}>Start upload</button>
            <button
              type="button"
              className="text-btn dialog-cancel"
              onClick={() => confirmBatch.resolve(false)}
            >
              Cancel
            </button>
          </div>
        </div>
      ) : null}
      {uploadConflict ? (
        <div className="upload-conflict" role="alertdialog" aria-label="Upload conflict">
          <strong>{uploadConflict.name} already exists</strong>
          <div className="upload-conflict-actions">
            <button type="button" onClick={() => uploadConflict.resolve('skip', false)}>Skip</button>
            <button type="button" onClick={() => uploadConflict.resolve('rename', false)}>Keep both</button>
            <button type="button" className="dialog-danger" onClick={() => uploadConflict.resolve('replace', false)}>Replace</button>
            <button type="button" className="text-btn" onClick={() => uploadConflict.resolve('rename', true)}>Keep both for all</button>
            <button type="button" className="text-btn dialog-cancel" onClick={() => uploadConflict.resolve('cancel', false)}>Cancel</button>
          </div>
        </div>
      ) : null}
      <div className="pane-footer">
        <span>
          {selectedEntries.length > 0
            ? `${selectedEntries.length} of ${state.entries.length} selected`
            : `${state.entries.length} items`}
        </span>
        <span>{selectedEntries.length > 0 && selectedBytes > 0 ? formatSize(selectedBytes) : ''}</span>
      </div>
      {contextMenu ? createPortal(
        <div
          className="entry-context-menu"
          role="menu"
          aria-label={`Actions for ${contextMenu.entry.name}`}
          style={{ left: contextMenu.x, top: contextMenu.y }}
          onMouseDown={(event) => event.stopPropagation()}
          onContextMenu={(event) => event.preventDefault()}
        >
          <div className="context-menu-title" title={contextMenu.entry.name}>
            {contextMenu.entry.name}
          </div>
          {contextMenu.entry.isDir ? (
            <button
              type="button"
              role="menuitem"
              onClick={() => runContextAction(() => activateEntry(contextMenu.entry))}
            >
              <Folder size={15} />
              Open folder
            </button>
          ) : null}
          {!contextMenu.entry.isDir && isEditablePath(contextMenu.entry.path) ? (
            <button
              type="button"
              role="menuitem"
              onClick={() => runContextAction(() =>
                onOpenFile(state.volumeId, contextMenu.entry.path, contextMenu.entry))}
            >
              <Pencil size={15} />
              Open in editor
            </button>
          ) : null}
          <button
            type="button"
            role="menuitem"
            disabled={!canCopySelection}
            onClick={() => runContextAction(onCopySelection)}
          >
            <ChevronRight size={15} />
            Copy to {otherPaneName}
          </button>
          <button
            type="button"
            role="menuitem"
            disabled={!canMoveSelection}
            onClick={() => runContextAction(onMoveSelection)}
          >
            <ArrowLeftRight size={15} />
            Move to {otherPaneName}
          </button>
          <div className="context-menu-separator" role="separator" />
          <button
            type="button"
            role="menuitem"
            onClick={() => runContextAction(() => copyRelativePath(contextMenu.entry))}
          >
            <Link2 size={15} />
            Copy relative path
          </button>
          <button
            type="button"
            role="menuitem"
            disabled={!canRenameSelection}
            onClick={() => runContextAction(onRenameSelection)}
          >
            <Pencil size={15} />
            Rename
          </button>
          <button
            type="button"
            role="menuitem"
            className="context-menu-danger"
            disabled={!canDeleteSelection}
            onClick={() => runContextAction(onDeleteSelection)}
          >
            <Trash2 size={15} />
            Delete permanently
          </button>
        </div>,
        document.body,
      ) : null}
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
  const [openSections, setOpenSections] = useState<Record<SectionKey, boolean>>({
    volumes: true,
    favorites: true,
    recent: false,
  })
  const toggleSection = useCallback((key: SectionKey) => {
    setOpenSections((current) => ({ ...current, [key]: !current[key] }))
  }, [])
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
    <div className={`app-shell${inspectorOpen ? '' : ' inspector-collapsed'}`}>
      <aside className="glass side-panel" aria-label="Locations">
        <div className="side-brand">
          <img src="/app-icon.svg" alt="" aria-hidden />
          <span>DirDeck</span>
        </div>

        <div className="side-sections">
          <section className="side-section">
            <div className="side-section-header">
              <button
                type="button"
                className="side-section-toggle"
                aria-expanded={openSections.volumes}
                onClick={() => toggleSection('volumes')}
              >
                {openSections.volumes ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                <HardDrive size={14} />
                <span>Volumes</span>
              </button>
              <button
                type="button"
                className="text-btn"
                title="Refresh availability (Ctrl/Cmd+R)"
                onClick={() => void refreshVolumes().then(() => refreshPanes())}
              >
                Refresh
              </button>
            </div>
            {openSections.volumes ? (
              <ul className="side-list">
                {volumes.map((v) => (
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
                ))}
                {volumes.length === 0 ? (
                  <li className="muted side-empty">No volumes configured.</li>
                ) : null}
              </ul>
            ) : null}
          </section>

          <section className="side-section">
            <div className="side-section-header">
              <button
                type="button"
                className="side-section-toggle"
                aria-expanded={openSections.favorites}
                onClick={() => toggleSection('favorites')}
              >
                {openSections.favorites ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                <Star size={14} />
                <span>Favorites</span>
              </button>
              <button
                type="button"
                className="icon-btn"
                aria-label={currentFav ? 'Remove favorite' : 'Add favorite'}
                title={currentFav ? 'Remove favorite' : 'Favorite current folder (Ctrl/Cmd+D)'}
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
            </div>
            {openSections.favorites ? (
              <ul className="side-list">
                {favorites.map((f) => (
                  <li key={f.id}>
                    <button type="button" onClick={() => navigatePane(activePane, f.volumeId, f.path)}>
                      <Star size={14} />
                      <span className="side-label">{f.label || f.path || f.volumeId}</span>
                    </button>
                  </li>
                ))}
                {favorites.length === 0 ? (
                  <li className="muted side-empty">No favorites yet. Press Ctrl/Cmd+D.</li>
                ) : null}
              </ul>
            ) : null}
          </section>

          <section className="side-section">
            <div className="side-section-header">
              <button
                type="button"
                className="side-section-toggle"
                aria-expanded={openSections.recent}
                onClick={() => toggleSection('recent')}
              >
                {openSections.recent ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                <Clock size={14} />
                <span>Recent</span>
              </button>
              {recents.length > 0 ? (
                <button
                  type="button"
                  className="text-btn"
                  onClick={() => void clearRecent().then(() => setRecents([]))}
                >
                  Clear
                </button>
              ) : null}
            </div>
            {openSections.recent ? (
              <ul className="side-list">
                {recents.map((r) => (
                  <li key={`${r.volumeId}:${r.path}`}>
                    <button type="button" onClick={() => navigatePane(activePane, r.volumeId, r.path)}>
                      <Clock size={14} />
                      <span className="side-label">
                        {volumes.find((v) => v.id === r.volumeId)?.name ?? r.volumeId}
                        {r.path ? ` / ${r.path}` : ' /'}
                      </span>
                    </button>
                  </li>
                ))}
                {recents.length === 0 ? (
                  <li className="muted side-empty">No recent folders yet.</li>
                ) : null}
              </ul>
            ) : null}
          </section>
        </div>

        <div className="side-footer">
          <div className="side-status">
            <strong>{username}</strong>
            <span className="muted">{activeCount ? `${activeCount} transfer${activeCount > 1 ? 's' : ''}` : 'Idle'}</span>
          </div>
          <button
            type="button"
            className="icon-btn"
            aria-label="Sign out"
            title="Sign out"
            onClick={() => {
              void logout().then(() => {
                setAuthed(false)
                setVolumes([])
                setJobs([])
                persistReady.current = false
              })
            }}
          >
            <LogOut size={16} />
          </button>
        </div>
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
        otherPaneName={rightVol?.name ?? 'right pane'}
        canCopySelection={canCopyRight}
        canMoveSelection={canMoveRight}
        canRenameSelection={canRename}
        canDeleteSelection={canDelete}
        onCopySelection={() => void copySelection('ltr')}
        onMoveSelection={() => void moveSelection('ltr')}
        onRenameSelection={openRename}
        onDeleteSelection={openDelete}
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
        otherPaneName={leftVol?.name ?? 'left pane'}
        canCopySelection={canCopyLeft}
        canMoveSelection={canMoveLeft}
        canRenameSelection={canRename}
        canDeleteSelection={canDelete}
        onCopySelection={() => void copySelection('rtl')}
        onMoveSelection={() => void moveSelection('rtl')}
        onRenameSelection={openRename}
        onDeleteSelection={openDelete}
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
        // Collapsed rail keeps a reopen control where the panel actually lives.
        // The toggle in the centre transfer strip is too far away to find.
        <div className="glass inspector-rail">
          <button
            type="button"
            className="inspector-rail-button"
            aria-label="Show inspector"
            title="Show inspector (Ctrl/Cmd+I)"
            onClick={() => setInspectorOpen(true)}
          >
            <PanelRightOpen size={16} />
          </button>
        </div>
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
