#!/usr/bin/env node
// Theme token invariants for frontend/src/index.css.
//
// The stylesheet carries four token blocks: a dark :root base, two light
// blocks (the prefers-color-scheme copy and the explicit [data-theme='light']
// copy), and a dark [data-theme='dark'] override. Keeping them consistent by
// hand has failed twice:
//
//   1. A bulk find-and-replace that tokenised colour literals also rewrote the
//      token *definitions*, producing `--danger-text: var(--danger-text)`.
//      Self-referential properties resolve to nothing, so the dark theme lost
//      its destructive colour and every dialog shadow — silently, because the
//      light theme was unaffected.
//   2. A 2-space-indented replacement missed the 4-space media-query copy, so
//      one light block was left without tokens the other had.
//
// Both are cheap to detect and expensive to notice by eye, so CI does it.

import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const cssPath = path.join(root, 'frontend/src/index.css')
const css = fs.readFileSync(cssPath, 'utf-8')

const failures = []

/** Collect `--name: value` declarations from a `{ ... }` body. */
function declarations(body) {
  const out = new Map()
  for (const m of body.matchAll(/^\s*(--[a-z0-9-]+)\s*:\s*([^;]+);/gim)) {
    // Values may span lines (gradients); the two light blocks sit at different
    // indentation depths, so compare on collapsed whitespace.
    out.set(m[1], m[2].replace(/\s+/g, ' ').trim())
  }
  return out
}

/** Find the body of the first rule whose selector matches, with its line number. */
function block(selectorPattern, label) {
  const re = new RegExp(`(${selectorPattern})\\s*\\{`, 'm')
  const m = re.exec(css)
  if (!m) {
    failures.push(`${label}: nem található a szabály (${selectorPattern})`)
    return null
  }
  let depth = 1
  let i = m.index + m[0].length
  const start = i
  while (i < css.length && depth > 0) {
    if (css[i] === '{') depth++
    else if (css[i] === '}') depth--
    i++
  }
  return {
    label,
    body: css.slice(start, i - 1),
    line: css.slice(0, m.index).split('\n').length,
  }
}

// ---------------------------------------------------------------------------
// 1. No custom property may resolve to itself.
// ---------------------------------------------------------------------------
for (const m of css.matchAll(/(--[a-z0-9-]+)\s*:\s*var\(\s*(--[a-z0-9-]+)\s*\)\s*;/gi)) {
  if (m[1] === m[2]) {
    const line = css.slice(0, m.index).split('\n').length
    failures.push(
      `index.css:${line}: a(z) ${m[1]} önmagára hivatkozik — semmire nem oldódik fel`,
    )
  }
}

// ---------------------------------------------------------------------------
// 2. The two light blocks must declare exactly the same token set.
// ---------------------------------------------------------------------------
const lightMedia = block(":root:not\\(\\[data-theme='dark'\\]\\)", 'light (@media)')
const lightExplicit = block(":root\\[data-theme='light'\\]", "light ([data-theme='light'])")

if (lightMedia && lightExplicit) {
  const a = declarations(lightMedia.body)
  const b = declarations(lightExplicit.body)
  for (const name of a.keys()) {
    if (!b.has(name)) failures.push(`${name}: megvan a(z) ${lightMedia.label} blokkban, hiányzik a(z) ${lightExplicit.label} blokkból`)
  }
  for (const name of b.keys()) {
    if (!a.has(name)) failures.push(`${name}: megvan a(z) ${lightExplicit.label} blokkban, hiányzik a(z) ${lightMedia.label} blokkból`)
  }
  for (const [name, value] of a) {
    if (b.has(name) && b.get(name) !== value) {
      failures.push(`${name}: eltérő érték a két világos blokkban (${value} vs ${b.get(name)})`)
    }
  }
}

// ---------------------------------------------------------------------------
// 3. Every dark base token needs a light counterpart, or the dark value leaks
//    into the light theme. Structural tokens (radii, spacing, durations) are
//    theme-independent by design and listed here explicitly.
// ---------------------------------------------------------------------------
const THEME_NEUTRAL = new Set([
  '--radius', '--font', '--mono', '--list-row-height',
])

const dark = block(':root', 'dark (:root)')
if (dark && lightMedia) {
  const d = declarations(dark.body)
  const l = declarations(lightMedia.body)
  for (const name of d.keys()) {
    if (!l.has(name) && !THEME_NEUTRAL.has(name)) {
      failures.push(`${name}: sötét témában definiálva, a világos blokkokból hiányzik`)
    }
  }
}

if (failures.length > 0) {
  console.error('Téma-token ellenőrzés: HIBA\n')
  for (const f of failures) console.error(`  - ${f}`)
  console.error(`\n${failures.length} probléma. Lásd scripts/check-theme-tokens.mjs.`)
  process.exit(1)
}

console.log('Téma-token ellenőrzés: rendben (nincs önhivatkozás, a világos blokkok egyeznek).')
