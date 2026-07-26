#!/usr/bin/env node
// Source-order check for responsive overrides in frontend/src/index.css.
//
// A rule inside `@media (max-width: …)` has the same specificity as the base
// rule it overrides, so whichever comes last in the file wins. When a base rule
// is written *below* its own breakpoint override, the override silently stops
// applying — CSS reports no error and the browser shows no warning.
//
// This is not hypothetical: `.transfer-strip { flex-direction: row }` sat in a
// max-width block above `.transfer-strip { flex-direction: column }`, so the
// stacked layout kept a vertical strip and wedged a 278px empty bar between the
// panes at every width below 1280px.
//
// The rule this enforces: a declaration inside a width media query must not be
// re-declared for the same selector later in the file at base level.

import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const cssPath = path.join(root, 'frontend/src/index.css')
const css = fs.readFileSync(cssPath, 'utf-8')

const lineOf = (index) => css.slice(0, index).split('\n').length

/** Walk the stylesheet, yielding every rule with its selector, body and media context. */
function* rules(source) {
  const re = /([^{}]+)\{/g
  const stack = []
  let m
  let i = 0
  while ((m = re.exec(source))) {
    if (m.index < i) continue
    const prelude = m[1].trim()
    let depth = 1
    let j = re.lastIndex
    while (j < source.length && depth > 0) {
      if (source[j] === '{') depth++
      else if (source[j] === '}') depth--
      j++
    }
    const body = source.slice(re.lastIndex, j - 1)
    if (prelude.startsWith('@')) {
      if (/^@media/.test(prelude) && /width\s*:/.test(prelude)) {
        stack.push(prelude)
        for (const inner of rules(body)) {
          yield { ...inner, media: prelude, index: re.lastIndex + inner.index }
        }
        stack.pop()
      }
      i = j
      re.lastIndex = j
      continue
    }
    yield { selector: prelude, body, media: null, index: m.index }
    i = j
    re.lastIndex = j
  }
}

const collected = [...rules(css)]
const failures = []

for (const rule of collected) {
  if (!rule.media) continue
  const props = [...rule.body.matchAll(/^\s*([a-z-]+)\s*:/gim)]
    .map((m) => m[1])
    .filter((p) => !p.startsWith('--'))
  for (const later of collected) {
    if (later.media) continue
    if (later.index <= rule.index) continue
    if (later.selector !== rule.selector) continue
    const laterProps = new Set(
      [...later.body.matchAll(/^\s*([a-z-]+)\s*:/gim)].map((m) => m[1]),
    )
    for (const prop of props) {
      if (laterProps.has(prop)) {
        failures.push(
          `${rule.selector} { ${prop} } a(z) "${rule.media}" blokkban ` +
            `(index.css:${lineOf(rule.index)}) sosem érvényesül: ` +
            `ugyanez a tulajdonság újra ki van írva alább, index.css:${lineOf(later.index)}`,
        )
      }
    }
  }
}

if (failures.length > 0) {
  console.error('CSS sorrend-ellenőrzés: HIBA\n')
  for (const f of failures) console.error(`  - ${f}`)
  console.error('\nTedd a breakpoint-szabályokat a bázisszabályok alá (a fájl végén van egy blokk erre).')
  process.exit(1)
}

console.log('CSS sorrend-ellenőrzés: rendben (minden breakpoint-felülírás a bázisszabálya után áll).')
