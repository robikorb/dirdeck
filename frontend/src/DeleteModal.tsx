import { useEffect, useRef } from 'react'
import { AlertTriangle, Trash2, X } from 'lucide-react'
import { useDialogFocus } from './useDialogFocus'

export default function DeleteModal({
  names,
  directoryCount,
  busy,
  error,
  onDelete,
  onClose,
}: {
  names: string[]
  directoryCount: number
  busy: boolean
  error: string | null
  onDelete: () => void
  onClose: () => void
}) {
  const itemCount = names.length
  const itemLabel = itemCount === 1 ? names[0] : `${itemCount} selected items`
  const cancelRef = useRef<HTMLButtonElement>(null)
  const dialogRef = useDialogFocus<HTMLElement>(onClose, !busy)

  useEffect(() => {
    cancelRef.current?.focus()
  }, [])

  return (
    <div className="modal-backdrop modal-backdrop-compact" role="presentation">
      <section
        ref={dialogRef}
        className="glass delete-dialog"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="delete-dialog-title"
        aria-describedby="delete-dialog-description"
      >
        <header>
          <div>
            <span className="delete-warning-icon"><AlertTriangle size={18} aria-hidden /></span>
            <div>
              <strong id="delete-dialog-title">Delete permanently?</strong>
              <span>This action cannot be undone.</span>
            </div>
          </div>
          <button type="button" className="icon-btn" aria-label="Close delete dialog" onClick={onClose}>
            <X size={17} />
          </button>
        </header>
        <div className="delete-dialog-body" id="delete-dialog-description">
          <p>
            Are you sure you want to permanently delete
            {' '}<strong title={names.join(', ')}>{itemLabel}</strong>?
          </p>
          {itemCount > 1 ? (
            <ul className="delete-selection-preview">
              {names.slice(0, 4).map((name) => <li key={name}>{name}</li>)}
              {itemCount > 4 ? <li>and {itemCount - 4} more…</li> : null}
            </ul>
          ) : null}
          {directoryCount > 0 ? (
            <p className="delete-folder-warning">
              <Trash2 size={15} aria-hidden />
              {directoryCount === 1
                ? 'The selected folder and everything inside it will be deleted.'
                : `${directoryCount} folders and everything inside them will be deleted.`}
            </p>
          ) : null}
        </div>
        {error ? <p className="login-error" role="alert">{error}</p> : null}
        <footer>
          <button ref={cancelRef} type="button" className="text-btn dialog-cancel" onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button type="button" className="dialog-danger" onClick={onDelete} disabled={busy}>
            <Trash2 size={14} aria-hidden />
            {busy ? 'Deleting…' : 'Delete permanently'}
          </button>
        </footer>
      </section>
    </div>
  )
}
