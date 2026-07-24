import { useEffect, useRef, useState } from 'react'
import { Pencil, X } from 'lucide-react'

export default function RenameModal({
  currentName,
  busy,
  error,
  onRename,
  onClose,
}: {
  currentName: string
  busy: boolean
  error: string | null
  onRename: (newName: string) => void
  onClose: () => void
}) {
  const [name, setName] = useState(currentName)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    const input = inputRef.current
    if (!input) return
    input.focus()
    const dot = currentName.lastIndexOf('.')
    input.setSelectionRange(0, dot > 0 ? dot : currentName.length)
  }, [currentName])

  return (
    <div className="modal-backdrop modal-backdrop-compact" role="presentation">
      <form
        className="glass rename-dialog"
        role="dialog"
        aria-modal="true"
        aria-label={`Rename ${currentName}`}
        onSubmit={(event) => {
          event.preventDefault()
          onRename(name.trim())
        }}
      >
        <header>
          <div>
            <Pencil size={17} aria-hidden />
            <strong>Rename</strong>
          </div>
          <button type="button" className="icon-btn" aria-label="Close rename dialog" onClick={onClose}>
            <X size={17} />
          </button>
        </header>
        <label>
          New name
          <input
            ref={inputRef}
            value={name}
            onChange={(event) => setName(event.target.value)}
            disabled={busy}
            spellCheck={false}
          />
        </label>
        {error ? <p className="login-error" role="alert">{error}</p> : null}
        <footer>
          <button type="button" className="text-btn dialog-cancel" onClick={onClose}>Cancel</button>
          <button type="submit" className="dialog-primary" disabled={busy || !name.trim() || name.trim() === currentName}>
            {busy ? 'Renaming…' : 'Rename'}
          </button>
        </footer>
      </form>
    </div>
  )
}
