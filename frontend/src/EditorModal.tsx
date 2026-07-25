import { useCallback, useEffect, useState } from 'react'
import Editor, { loader, type BeforeMount } from '@monaco-editor/react'
import * as monaco from 'monaco-editor'
import editorWorker from 'monaco-editor/editor/editor.worker.js?worker'
import jsonWorker from 'monaco-editor/language/json/json.worker.js?worker'
import cssWorker from 'monaco-editor/language/css/css.worker.js?worker'
import htmlWorker from 'monaco-editor/language/html/html.worker.js?worker'
import tsWorker from 'monaco-editor/language/typescript/ts.worker.js?worker'
import { FileCode2, Save, X } from 'lucide-react'

type MonacoWorkerEnvironment = {
  getWorker: (_moduleId: string, label: string) => Worker
}

;(self as unknown as { MonacoEnvironment: MonacoWorkerEnvironment }).MonacoEnvironment = {
  getWorker(_moduleId, label) {
    if (label === 'json') return new jsonWorker()
    if (label === 'css' || label === 'scss' || label === 'less') return new cssWorker()
    if (label === 'html' || label === 'handlebars' || label === 'razor') return new htmlWorker()
    if (label === 'typescript' || label === 'javascript') return new tsWorker()
    return new editorWorker()
  },
}
loader.config({ monaco })

const configureEditor: BeforeMount = (editorAPI) => {
  editorAPI.editor.defineTheme('dirdeck-dark', {
    base: 'vs-dark',
    inherit: true,
    rules: [
      { token: 'comment', foreground: '6A737D', fontStyle: 'italic' },
      { token: 'string', foreground: 'A5D6A7' },
      { token: 'number', foreground: '79C0FF' },
      { token: 'keyword', foreground: 'FF7B72' },
      { token: 'type', foreground: 'D2A8FF' },
      { token: 'delimiter', foreground: '8B949E' },
    ],
    colors: {
      'editor.background': '#0D1117',
      'editor.foreground': '#D7DEE9',
      'editorLineNumber.foreground': '#4F5968',
      'editorLineNumber.activeForeground': '#AAB4C3',
      'editorCursor.foreground': '#58A6FF',
      'editor.selectionBackground': '#264F78',
      'editor.inactiveSelectionBackground': '#1D3A5A',
      'editor.lineHighlightBackground': '#151B23',
      'editorIndentGuide.background1': '#212936',
      'editorIndentGuide.activeBackground1': '#3B4657',
      'editorWhitespace.foreground': '#303A49',
      'editorGutter.background': '#0D1117',
      'editorWidget.background': '#161B22',
      'editorWidget.border': '#30363D',
      'editorSuggestWidget.background': '#161B22',
      'editorSuggestWidget.border': '#30363D',
      'editorSuggestWidget.selectedBackground': '#26364A',
      'minimap.background': '#0D1117',
      'scrollbarSlider.background': '#6E768140',
      'scrollbarSlider.hoverBackground': '#6E768170',
      'scrollbarSlider.activeBackground': '#8B949E80',
    },
  })
}

export type EditorDocument = {
  name: string
  path: string
  language: string
  content: string
  originalContent: string
  readOnly: boolean
}

export default function EditorModal({
  document,
  saving,
  error,
  onChange,
  onSave,
  onClose,
}: {
  document: EditorDocument
  saving: boolean
  error: string | null
  onChange: (content: string) => void
  onSave: () => void
  onClose: () => void
}) {
  const dirty = document.content !== document.originalContent
  const [confirmClose, setConfirmClose] = useState(false)
  const simpleDocument = ['markdown', 'plaintext', 'text'].includes(document.language)

  const requestClose = useCallback(() => {
    if (dirty) {
      setConfirmClose(true)
      return
    }
    onClose()
  }, [dirty, onClose])

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's') {
        event.preventDefault()
        if (!document.readOnly && dirty && !saving) onSave()
      }
      if (event.key === 'Escape') {
        event.preventDefault()
        if (confirmClose) setConfirmClose(false)
        else requestClose()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [confirmClose, dirty, document.readOnly, onClose, onSave, requestClose, saving])

  useEffect(() => {
    if (!dirty) return
    const beforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault()
    }
    window.addEventListener('beforeunload', beforeUnload)
    return () => window.removeEventListener('beforeunload', beforeUnload)
  }, [dirty])

  return (
    <div className="modal-backdrop" role="presentation">
      <section className="editor-window" role="dialog" aria-modal="true" aria-label={`Edit ${document.name}`}>
        <header className="editor-titlebar">
          <div className="editor-file-title">
            <FileCode2 size={18} aria-hidden />
            <span className="editor-file-name">{document.name}</span>
            {dirty ? <span className="dirty-dot" title="Unsaved changes">●</span> : null}
            {document.readOnly ? <span className="editor-badge">Read only</span> : null}
          </div>
          <div className="editor-actions">
            <button
              type="button"
              className="editor-save"
              disabled={document.readOnly || !dirty || saving}
              onClick={onSave}
            >
              <Save size={15} aria-hidden />
              {saving ? 'Saving…' : 'Save'}
            </button>
            <button type="button" className="icon-btn" aria-label="Close editor" onClick={requestClose}>
              <X size={18} />
            </button>
          </div>
        </header>
        <div className="editor-breadcrumbs" title={document.path}>
          <span>{document.path}</span>
          <span className="editor-language-label">{document.language}</span>
        </div>
        {error ? <div className="editor-error" role="alert">{error}</div> : null}
        <div className="editor-surface">
          <Editor
            height="100%"
            beforeMount={configureEditor}
            theme="dirdeck-dark"
            language={document.language}
            value={document.content}
            onChange={(value) => onChange(value ?? '')}
            loading={<div className="editor-loading">Starting editor…</div>}
            options={{
              readOnly: document.readOnly,
              automaticLayout: true,
              accessibilitySupport: 'auto',
              ariaLabel: `${document.readOnly ? 'View' : 'Edit'} ${document.name}`,
              minimap: {
                enabled: !simpleDocument,
                scale: 0.85,
                showSlider: 'mouseover',
              },
              fontFamily: '"SF Mono", "Cascadia Code", "JetBrains Mono", Menlo, monospace',
              fontSize: 14,
              lineHeight: 22,
              lineNumbersMinChars: 3,
              folding: !simpleDocument,
              foldingHighlight: true,
              glyphMargin: false,
              smoothScrolling: true,
              cursorSmoothCaretAnimation: 'on',
              bracketPairColorization: { enabled: true },
              guides: { bracketPairs: true, indentation: true },
              renderLineHighlight: 'all',
              renderWhitespace: 'selection',
              wordWrap: simpleDocument ? 'on' : 'off',
              wrappingIndent: 'same',
              padding: { top: 16, bottom: 16 },
              scrollBeyondLastLine: false,
              scrollbar: {
                verticalScrollbarSize: 11,
                horizontalScrollbarSize: 11,
              },
              stickyScroll: { enabled: !simpleDocument },
              tabSize: 2,
            }}
          />
        </div>
        <footer className="editor-statusbar">
          <div>
            <span className="editor-status-dot" aria-hidden />
            <span>{document.readOnly ? 'Read only' : dirty ? 'Modified' : 'Saved'}</span>
          </div>
          <div>
            <span>{document.content.split('\n').length} lines</span>
            <span>{document.content.length.toLocaleString()} characters</span>
            <span>{document.language}</span>
            <span>{document.readOnly ? 'Preview' : '⌘S / Ctrl+S'}</span>
          </div>
        </footer>
        {confirmClose ? (
          <div className="editor-confirm" role="alertdialog" aria-modal="true" aria-label="Unsaved changes">
            <div>
              <strong>Discard unsaved changes?</strong>
              <span>Your edits to {document.name} have not been saved.</span>
            </div>
            <button type="button" className="text-btn dialog-cancel" onClick={() => setConfirmClose(false)}>
              Keep editing
            </button>
            <button type="button" className="dialog-danger" onClick={onClose}>
              Discard
            </button>
          </div>
        ) : null}
      </section>
    </div>
  )
}
