// Vendors the Vditor runtime that the preview API loads by URL at runtime
// into public/vditor-dist, so the published frontend carries its own copy.
// The directory layout mirrors "cdn + /dist/..." (see Vditor's README: the
// options.cdn prefix is concatenated with /dist/js/..., /dist/css/... and
// /dist/index.css), therefore files are copied with their dist-relative
// paths intact and LessonView passes cdn: '/vditor-dist'.
//
// Only the files preview mode can reach are vendored (engines, i18n,
// themes), which keeps the embedded Go binary ~15MB smaller than copying the
// whole 23MB dist. Chart/math libraries are included on purpose: the user
// asked for all render plugins enabled, and they must work offline too.
// Run once per Vditor version bump (it rewrites everything it installs).
import { cpSync, existsSync, mkdirSync, readdirSync, rmSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const root = join(here, '..')
const src = join(root, 'node_modules', 'vditor', 'dist')
const dest = join(root, 'public', 'vditor-dist')

if (!existsSync(join(src, 'method.min.js'))) {
  console.error('vditor is not installed: run "npm install" first')
  process.exit(1)
}

// dist-relative paths copied verbatim (layout must match "cdn + /dist/...").
// method.min.js itself is bundled by Vite; the rest is fetched by URL.
const entries = [
  'index.css',
  'css/content-theme',
  'js/lute/lute.min.js',
  'js/i18n/en_US.js',
  'js/i18n/zh_CN.js',
  'js/icons',
  'js/highlight.js',
  'js/katex/katex.min.css',
  'js/katex/katex.min.js',
  'js/katex/mhchem.min.js',
  'js/mathjax/tex-svg-full.js',
  'js/mermaid/mermaid.min.js',
  'js/markmap/markmap.min.js',
  'js/markmap/prism.css',
  'js/markmap/katex.min.css',
  'js/echarts/echarts.min.js',
  'js/flowchart.js/flowchart.min.js',
  'js/graphviz/full.render.js',
  'js/graphviz/viz.js',
  'js/plantuml/plantuml-encoder.min.js',
  'js/abcjs/abcjs_basic.min.js',
  'js/smiles-drawer/smiles-drawer.min.js',
  'js/wavedrom/wavedrom.min.js',
  'images/img-loading.svg',
  'images/logo.png',
  'images/emoji',
]

rmSync(dest, { recursive: true, force: true })
let total = 0
for (const entry of entries) {
  const from = join(src, entry)
  if (!existsSync(from)) {
    console.error(`missing in vditor dist: ${entry}`)
    process.exit(1)
  }
  const to = join(dest, 'dist', entry)
  mkdirSync(dirname(to), { recursive: true })
  cpSync(from, to, { recursive: true })
  const st = statSync(from)
  total += st.isDirectory() ? dirSize(from) : st.size
}

function dirSize(dir) {
  let bytes = 0
  for (const name of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, name.name)
    bytes += name.isDirectory() ? dirSize(full) : statSync(full).size
  }
  return bytes
}

console.log(`vditor runtime vendored into public/vditor-dist (${(total / 1048576).toFixed(1)} MB top-level)`)
