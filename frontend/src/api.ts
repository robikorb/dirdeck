export type Volume = {
  id: string
  name: string
  readOnly: boolean
  showHiddenFiles: boolean
  thumbnails: boolean
  available: boolean
}

export type DirEntry = {
  name: string
  path: string
  isDir: boolean
  isSymlink: boolean
  size: number
  modTime: string
  mode: string
}

export type ListResult = {
  entries: DirEntry[]
  truncated: boolean
}

export type FileMeta = {
  name: string
  path: string
  isDir: boolean
  isSymlink: boolean
  size: number
  modTime: string
  mode: string
}

export type Session = {
  authenticated: boolean
  username?: string
  csrfToken?: string
  expiresAt?: string
}

export type TransferJob = {
  id: string
  kind: string
  status: string
  sourceVolumeId: string
  sourcePath: string
  sourcePaths: string[]
  destVolumeId: string
  destDir: string
  destName: string
  conflictPolicy: string
  applyToAll: boolean
  bytesTotal: number
  bytesDone: number
  filesTotal: number
  filesDone: number
  bytesPerSecond: number
  bytesRemaining: number
  currentPath: string
  stagingName: string
  conflictName: string | null
  errorMessage: string | null
  freeSpaceKnown: boolean
  freeSpaceBytes: number | null
  createdAt: string
  updatedAt: string
  startedAt: string | null
  finishedAt: string | null
}

export type Favorite = {
  id: number
  volumeId: string
  path: string
  label: string
  createdAt: string
}

export type RecentLocation = {
  volumeId: string
  path: string
  visitedAt: string
}

export type PaneSnapshot = {
  volumeId: string
  path: string
  view: 'list' | 'grid' | string
}

export type PaneStatePayload = {
  left: PaneSnapshot
  right: PaneSnapshot
  inspectorOpen?: boolean
  activePane?: 'left' | 'right' | string
  updatedAt?: string
}

export type ConflictAction = 'skip' | 'replace' | 'rename'

const IMAGE_EXT = /\.(jpe?g|png|gif|webp)$/i
const MEDIA_PREVIEW_EXT = /\.(jpe?g|png|gif|webp|mp4|webm|mp3|wav|ogg)$/i
const TEXT_PREVIEW_EXT =
  /\.(txt|md|markdown|json|jsonc|ya?ml|toml|csv|log|xml|html?|css|jsx?|tsx?|go|py|rs|sh|docx)$/i

export type TextPreviewKind = 'text' | 'markdown' | 'json' | 'code' | 'docx'

export type TextPreview = {
  kind: TextPreviewKind | string
  language?: string
  text: string
  truncated: boolean
  bytesRead: number
  fileSize: number
}

export type EditableFile = {
  content: string
  language: string
  size: number
  modTime: string
}

let csrfToken = ''

export function setCsrfToken(token: string) {
  csrfToken = token
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  if (init.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  if (init.method && init.method !== 'GET' && init.method !== 'HEAD' && csrfToken) {
    headers.set('X-CSRF-Token', csrfToken)
  }
  const res = await fetch(path, {
    ...init,
    headers,
    credentials: 'same-origin',
  })
  if (!res.ok) {
    let message = res.statusText
    try {
      const data = (await res.json()) as { error?: string }
      if (data.error) message = data.error
    } catch {
      /* ignore */
    }
    throw new Error(message)
  }
  if (res.status === 204) {
    return undefined as T
  }
  return (await res.json()) as T
}

export async function getSession(): Promise<Session> {
  return request<Session>('/api/auth/session')
}

export async function login(username: string, password: string): Promise<Session> {
  const data = await request<{ username: string; csrfToken: string; expiresAt: string }>(
    '/api/auth/login',
    {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    },
  )
  setCsrfToken(data.csrfToken)
  return {
    authenticated: true,
    username: data.username,
    csrfToken: data.csrfToken,
    expiresAt: data.expiresAt,
  }
}

export async function logout(): Promise<void> {
  await request('/api/auth/logout', { method: 'POST' })
  setCsrfToken('')
}

export async function listVolumes(): Promise<Volume[]> {
  const data = await request<{ volumes: Volume[] }>('/api/volumes')
  return data.volumes
}

export async function listDir(volumeId: string, path = ''): Promise<ListResult> {
  const q = new URLSearchParams({ path })
  return request<ListResult>(`/api/volumes/${encodeURIComponent(volumeId)}/list?${q}`)
}

export async function statPath(volumeId: string, path = ''): Promise<FileMeta> {
  const q = new URLSearchParams({ path })
  return request<FileMeta>(`/api/volumes/${encodeURIComponent(volumeId)}/stat?${q}`)
}

export function thumbnailURL(volumeId: string, path: string): string {
  const q = new URLSearchParams({ path })
  return `/api/volumes/${encodeURIComponent(volumeId)}/thumbnail?${q}`
}

export function previewURL(volumeId: string, path: string, kind?: string): string {
  const q = new URLSearchParams({ path })
  if (kind) q.set('kind', kind)
  return `/api/volumes/${encodeURIComponent(volumeId)}/preview?${q}`
}

export async function fetchTextPreview(volumeId: string, path: string): Promise<TextPreview> {
  const res = await fetch(previewURL(volumeId, path), { credentials: 'same-origin' })
  if (!res.ok) {
    let message = res.statusText
    try {
      const data = (await res.json()) as { error?: string }
      if (data.error) message = data.error
    } catch {
      /* ignore */
    }
    throw new Error(message)
  }
  return (await res.json()) as TextPreview
}

export async function fetchEditableFile(volumeId: string, path: string): Promise<EditableFile> {
  const q = new URLSearchParams({ path })
  return request<EditableFile>(`/api/volumes/${encodeURIComponent(volumeId)}/content?${q}`)
}

export async function saveEditableFile(
  volumeId: string,
  path: string,
  content: string,
  expectedModTime: string,
): Promise<FileMeta> {
  const q = new URLSearchParams({ path })
  return request<FileMeta>(`/api/volumes/${encodeURIComponent(volumeId)}/content?${q}`, {
    method: 'PUT',
    body: JSON.stringify({ content, expectedModTime }),
  })
}

export async function renameEntry(
  volumeId: string,
  path: string,
  newName: string,
): Promise<FileMeta> {
  return request<FileMeta>(`/api/volumes/${encodeURIComponent(volumeId)}/rename`, {
    method: 'POST',
    body: JSON.stringify({ path, newName }),
  })
}

export async function deleteEntries(volumeId: string, paths: string[]): Promise<void> {
  await request<void>(`/api/volumes/${encodeURIComponent(volumeId)}/entry`, {
    method: 'DELETE',
    body: JSON.stringify({ paths }),
  })
}

export function isImagePath(path: string): boolean {
  return IMAGE_EXT.test(path)
}

export function isMediaPreviewablePath(path: string): boolean {
  return MEDIA_PREVIEW_EXT.test(path)
}

/** @deprecated Prefer isMediaPreviewablePath for media; use isTextPreviewablePath for text. */
export function isPreviewablePath(path: string): boolean {
  return isMediaPreviewablePath(path) || isTextPreviewablePath(path)
}

export function isTextPreviewablePath(path: string): boolean {
  const base = path.split('/').pop() ?? path
  if (/^\.env(\.|$)/i.test(base)) return true
  return TEXT_PREVIEW_EXT.test(base)
}

export function isEditablePath(path: string): boolean {
  return isTextPreviewablePath(path) && !/\.docx$/i.test(path)
}

export async function listFavorites(): Promise<Favorite[]> {
  const data = await request<{ favorites: Favorite[] }>('/api/favorites')
  return data.favorites
}

export async function addFavorite(volumeId: string, path: string, label?: string): Promise<Favorite> {
  return request<Favorite>('/api/favorites', {
    method: 'POST',
    body: JSON.stringify({ volumeId, path, label }),
  })
}

export async function removeFavorite(id: number): Promise<void> {
  await request(`/api/favorites/${id}`, { method: 'DELETE' })
}

export async function listRecent(): Promise<RecentLocation[]> {
  const data = await request<{ recent: RecentLocation[] }>('/api/recent')
  return data.recent
}

export async function recordRecent(volumeId: string, path: string): Promise<void> {
  await request('/api/recent', {
    method: 'POST',
    body: JSON.stringify({ volumeId, path }),
  })
}

export async function clearRecent(): Promise<void> {
  await request('/api/recent', { method: 'DELETE' })
}

export async function getPaneState(): Promise<PaneStatePayload | null> {
  const data = await request<{ paneState: PaneStatePayload | null }>('/api/preferences/panes')
  return data.paneState
}

export async function savePaneState(state: PaneStatePayload): Promise<PaneStatePayload> {
  const data = await request<{ paneState: PaneStatePayload }>('/api/preferences/panes', {
    method: 'PUT',
    body: JSON.stringify(state),
  })
  return data.paneState
}

export async function listTransfers(): Promise<TransferJob[]> {
  const data = await request<{ transfers: TransferJob[] }>('/api/transfers')
  return data.transfers
}

export async function createCopy(body: {
  sourceVolumeId: string
  sourcePath?: string
  sourcePaths?: string[]
  destVolumeId: string
  destDir: string
  conflictPolicy?: string
}): Promise<TransferJob> {
  return request<TransferJob>('/api/transfers', {
    method: 'POST',
    body: JSON.stringify({ kind: 'copy', ...body }),
  })
}

export async function createMove(body: {
  sourceVolumeId: string
  sourcePath?: string
  sourcePaths?: string[]
  destVolumeId: string
  destDir: string
  conflictPolicy?: string
}): Promise<TransferJob> {
  return request<TransferJob>('/api/transfers', {
    method: 'POST',
    body: JSON.stringify({ kind: 'move', ...body }),
  })
}

export async function cancelTransfer(id: string): Promise<TransferJob> {
  return request<TransferJob>(`/api/transfers/${encodeURIComponent(id)}/cancel`, {
    method: 'POST',
  })
}

export async function resolveConflict(
  id: string,
  action: ConflictAction,
  applyToAll = false,
  renameTo?: string,
): Promise<TransferJob> {
  return request<TransferJob>(`/api/transfers/${encodeURIComponent(id)}/conflict`, {
    method: 'POST',
    body: JSON.stringify({ action, applyToAll, renameTo }),
  })
}

/** Open SSE stream; returns a close function. Reconnects are handled by the caller. */
export function subscribeTransfers(onJob: (job: TransferJob) => void): () => void {
  const es = new EventSource('/api/transfers/events', { withCredentials: true })
  const handler = (ev: MessageEvent) => {
    try {
      const job = JSON.parse(String(ev.data)) as TransferJob
      if (job && job.id) onJob(job)
    } catch {
      /* ignore malformed */
    }
  }
  es.addEventListener('transfer', handler as EventListener)
  return () => {
    es.removeEventListener('transfer', handler as EventListener)
    es.close()
  }
}

export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = bytes / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i += 1
  }
  return `${v.toFixed(v >= 10 ? 0 : 1)} ${units[i]}`
}

export function formatDate(iso: string): string {
  try {
    return new Date(iso).toLocaleString()
  } catch {
    return iso
  }
}

export function formatSpeed(bps: number): string {
  if (!bps || bps < 1) return '—'
  return `${formatSize(bps)}/s`
}

export type UploadOutcome = {
  name: string
  skipped: boolean
  conflict: boolean
  meta?: FileMeta
}

export type UploadConflictAction = 'skip' | 'replace' | 'rename'

/**
 * Uploads one file with progress. XHR rather than fetch: fetch still has no
 * upload progress event, and per-file progress is the whole point here.
 * Returns an abort function alongside the promise so a queue can cancel.
 */
export function uploadFile(
  volumeId: string,
  destDir: string,
  file: File,
  options: {
    conflict?: UploadConflictAction
    /** Relative subdirectory inside a dropped folder; the server creates it. */
    subDir?: string
    onProgress?: (loaded: number, total: number) => void
  } = {},
): { promise: Promise<UploadOutcome>; abort: () => void } {
  const q = new URLSearchParams({ path: destDir, name: file.name })
  if (options.subDir) q.set('dir', options.subDir)
  if (options.conflict) q.set('conflict', options.conflict)
  const xhr = new XMLHttpRequest()

  const promise = new Promise<UploadOutcome>((resolve, reject) => {
    xhr.open('POST', `/api/volumes/${encodeURIComponent(volumeId)}/upload?${q}`)
    xhr.withCredentials = true
    if (csrfToken) xhr.setRequestHeader('X-CSRF-Token', csrfToken)
    xhr.setRequestHeader('Content-Type', 'application/octet-stream')

    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) options.onProgress?.(event.loaded, event.total)
    }
    xhr.onload = () => {
      let payload: { name?: string; skipped?: boolean; conflict?: boolean; meta?: FileMeta; error?: string } = {}
      try {
        payload = JSON.parse(xhr.responseText)
      } catch {
        /* non-JSON error body */
      }
      if (xhr.status === 409) {
        resolve({ name: payload.name ?? file.name, skipped: false, conflict: true })
        return
      }
      if (xhr.status >= 400) {
        reject(new Error(payload.error || xhr.statusText || 'upload failed'))
        return
      }
      resolve({
        name: payload.name ?? file.name,
        skipped: Boolean(payload.skipped),
        conflict: false,
        meta: payload.meta,
      })
    }
    xhr.onerror = () => reject(new Error('network error during upload'))
    xhr.onabort = () => reject(new Error('upload cancelled'))
    xhr.send(file)
  })

  return { promise, abort: () => xhr.abort() }
}


/** One file from a dropped tree, with its path relative to the drop target. */
export type PickedFile = { file: File; subDir: string }

/**
 * Walks a dropped FileSystemEntry into a flat list of files plus their relative
 * directories.
 *
 * readEntries returns a bounded batch (about 100) and must be called until it
 * yields nothing — a single call silently truncates large folders, which for an
 * upload would mean quietly losing files.
 */
export async function collectDroppedEntry(
  entry: FileSystemEntry,
  parentDir = '',
  out: PickedFile[] = [],
): Promise<PickedFile[]> {
  if (entry.isFile) {
    const file = await new Promise<File>((resolve, reject) =>
      (entry as FileSystemFileEntry).file(resolve, reject),
    )
    out.push({ file, subDir: parentDir })
    return out
  }
  if (entry.isDirectory) {
    const dir = parentDir ? `${parentDir}/${entry.name}` : entry.name
    const reader = (entry as FileSystemDirectoryEntry).createReader()
    for (;;) {
      const batch = await new Promise<FileSystemEntry[]>((resolve, reject) =>
        reader.readEntries(resolve, reject),
      )
      if (batch.length === 0) break
      for (const child of batch) {
        await collectDroppedEntry(child, dir, out)
      }
    }
  }
  return out
}

/** Splits a webkitRelativePath ("Folder/Sub/file.txt") into its directory part. */
export function relativeDirOf(file: File): string {
  const rel = (file as File & { webkitRelativePath?: string }).webkitRelativePath
  if (!rel) return ''
  const parts = rel.split('/')
  parts.pop()
  return parts.join('/')
}

export async function createFolder(
  volumeId: string,
  path: string,
  name: string,
): Promise<FileMeta> {
  return request<FileMeta>(`/api/volumes/${encodeURIComponent(volumeId)}/folder`, {
    method: 'POST',
    body: JSON.stringify({ path, name }),
  })
}
