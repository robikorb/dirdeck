/** Lightweight sanitized markdown — no raw HTML, http(s) links only. */
export function renderSafeMarkdown(src: string): string {
  const escaped = escapeHtml(src)
  const lines = escaped.split(/\r?\n/)
  const out: string[] = []
  let inCode = false
  let codeBuf: string[] = []

  const flushPara = (buf: string[]) => {
    if (!buf.length) return
    out.push(`<p>${inline(buf.join(' '))}</p>`)
    buf.length = 0
  }

  let para: string[] = []
  let listType: 'ul' | 'ol' | null = null

  const closeList = () => {
    if (listType) {
      out.push(listType === 'ul' ? '</ul>' : '</ol>')
      listType = null
    }
  }

  for (const line of lines) {
    if (line.startsWith('```')) {
      closeList()
      flushPara(para)
      if (inCode) {
        out.push(`<pre><code>${codeBuf.join('\n')}</code></pre>`)
        codeBuf = []
        inCode = false
      } else {
        inCode = true
      }
      continue
    }
    if (inCode) {
      codeBuf.push(line)
      continue
    }

    const heading = /^(#{1,6})\s+(.+)$/.exec(line)
    if (heading) {
      closeList()
      flushPara(para)
      const level = heading[1].length
      out.push(`<h${level}>${inline(heading[2])}</h${level}>`)
      continue
    }

    const ul = /^[-*]\s+(.+)$/.exec(line)
    if (ul) {
      flushPara(para)
      if (listType !== 'ul') {
        closeList()
        out.push('<ul>')
        listType = 'ul'
      }
      out.push(`<li>${inline(ul[1])}</li>`)
      continue
    }

    const ol = /^\d+\.\s+(.+)$/.exec(line)
    if (ol) {
      flushPara(para)
      if (listType !== 'ol') {
        closeList()
        out.push('<ol>')
        listType = 'ol'
      }
      out.push(`<li>${inline(ol[1])}</li>`)
      continue
    }

    if (line.trim() === '') {
      closeList()
      flushPara(para)
      continue
    }

    closeList()
    para.push(line)
  }

  if (inCode) {
    out.push(`<pre><code>${codeBuf.join('\n')}</code></pre>`)
  }
  closeList()
  flushPara(para)
  return out.join('\n')
}

function inline(s: string): string {
  let out = s
  out = out.replace(/`([^`]+)`/g, '<code>$1</code>')
  out = out.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
  out = out.replace(/\*([^*]+)\*/g, '<em>$1</em>')
  out = out.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (_m, label: string, href: string) => {
    const safe = sanitizeHref(href)
    if (!safe) return label
    return `<a href="${safe}" rel="noopener noreferrer" target="_blank">${label}</a>`
  })
  return out
}

function sanitizeHref(href: string): string | null {
  const trimmed = href.trim()
  if (/^https?:\/\//i.test(trimmed)) return escapeAttr(trimmed)
  return null
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

function escapeAttr(s: string): string {
  return escapeHtml(s).replace(/'/g, '&#39;')
}
