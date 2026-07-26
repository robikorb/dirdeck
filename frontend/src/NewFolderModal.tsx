import { useEffect, useRef, useState } from 'react'
import { FolderPlus, X } from 'lucide-react'

export default function NewFolderModal({
  location,
  busy,
  error,
  onCreate,
  onClose,
}: {
  location: string
  busy: boolean
  error: string | null
  onCreate: (name: string) => void
  onClose: () => void
}) {
  const [name, setName] = useState('New folder')
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    const input = inputRef.current
    if (!input) return
    input.focus()
    input.select()
  }, [])

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        onClose()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [onClose])

  return (
    <div className="modal-backdrop modal-backdrop-compact" role="presentation">
      <form
        className="glass rename-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Create a new folder"
        onSubmit={(event) => {
          event.preventDefault()
          onCreate(name.trim())
        }}
      >
        <header>
          <div>
            <FolderPlus size={17} aria-hidden />
            <strong>New folder</strong>
          </div>
          <button type="button" className="icon-btn" aria-label="Close dialog" onClick={onClose}>
            <X size={17} />
          </button>
        </header>
        <label>
          Name
          <input
            ref={inputRef}
            value={name}
            onChange={(event) => setName(event.target.value)}
            disabled={busy}
            spellCheck={false}
          />
        </label>
        <p className="muted dialog-hint">Created in {location || 'the volume root'}</p>
        {error ? <p className="login-error" role="alert">{error}</p> : null}
        <footer>
          <button type="button" className="text-btn dialog-cancel" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className="dialog-primary" disabled={busy || !name.trim()}>
            {busy ? 'Creating…' : 'Create'}
          </button>
        </footer>
      </form>
    </div>
  )
}
