// Markdown rendering via Vditor (https://github.com/Vanessa219/vditor, MIT).
//
// The app only ever shows read-only documents, so no editor is mounted: the
// static preview() API renders into an element that is already on the page.
// Rendering *into the live node* (instead of taking the HTML of a detached
// one) matters because preview() resolves before its post-renderers have
// finished: highlight.js, KaTeX, mermaid, echarts, ... keep working on the
// same element afterwards, and a detached copy would lose all of it.
//
// method.min.js (~51KB) is bundled and imported on the first Markdown lesson;
// the Lute engine, the themes and every plugin runtime are fetched by URL from
// the vendored copy (see scripts/vendor-vditor.js), so rendering stays
// offline. A plugin that fails to load only leaves its own block unrendered.
//
// Before handing text to Vditor, relative links and images are rewritten to
// absolute course URLs: `![x](pic.png)` inside `Section 1/notes.md` becomes
// `/files/Section%201/pic.png`, so images referenced by a Markdown file keep
// showing up automatically.

const VDITOR_CDN = '/vditor-dist'

let vditorPromise = null
let stylesPromise = null

function loadVditor() {
  if (!vditorPromise) {
    // method.min.js is small enough to bundle; everything else (lute.min.js,
    // themes, highlight.js) loads from VDITOR_CDN at preview() time.
    vditorPromise = import('vditor/dist/method.min.js').then((module) => module.default ?? module)
  }
  return vditorPromise
}

// Vditor fetches its themes and plugins on its own, but the base .vditor-reset
// stylesheet is expected to be part of the page already. It is added on the
// first Markdown render so pages without one never download it.
function loadStyles() {
  if (!stylesPromise) {
    stylesPromise = new Promise((resolve) => {
      if (document.getElementById('vditorIndexStyle')) return resolve()
      const link = document.createElement('link')
      link.id = 'vditorIndexStyle'
      link.rel = 'stylesheet'
      link.href = `${VDITOR_CDN}/dist/index.css`
      link.onload = () => resolve()
      // Without it the text is still readable, just plain: never fail a render.
      link.onerror = () => resolve()
      document.head.appendChild(link)
    })
  }
  return stylesPromise
}

// Rewrites relative Markdown links/images to absolute course URLs against the
// folder of the source file (`base` is its course relative path). Absolute
// URLs, root paths, fragments and mailto/data targets pass through; anything
// else becomes an encoded /files/ URL.
export function resolveMarkdownUrl(target, base) {
  if (!target) return ''
  if (/^(https?:\/\/|mailto:|#|data:image\/)/i.test(target)) return target
  const anchorAt = target.indexOf('#')
  const path = anchorAt === -1 ? target : target.slice(0, anchorAt)
  const anchor = anchorAt === -1 ? '' : target.slice(anchorAt)
  const rooted = path.startsWith('/')
  const dir = !rooted && base && base.includes('/') ? base.slice(0, base.lastIndexOf('/')) : ''
  const stack = []
  for (const part of (rooted ? path : dir ? `${dir}/${path}` : path).split('/')) {
    if (!part || part === '.') continue
    if (part === '..') stack.pop()
    else stack.push(part)
  }
  if (stack.length === 0) return ''
  return '/files/' + stack.map(encodeURIComponent).join('/') + anchor
}

const IMAGE_PATTERN = /!\[([^\]]*)\]\(([^)\s]+)\)/g
const LINK_PATTERN = /\[([^\]]+)\]\(([^)\s]+)\)/g
// Raw HTML is common in hand-written documents (<img src="images/pad.png">).
// Vditor keeps such tags exactly as they are, so their src/href has to be
// rewritten too - a relative path left in place resolves against the lesson
// URL (…/README.md/images/…) and 404s, which looks like a missing image.
const HTML_ATTRIBUTE_PATTERN = /(<(?:img|a)\b[^>]*?\b(?:src|href)\s*=\s*)(?:"([^"]*)"|'([^']*)'|([^\s>]+))/gi

function rewriteRelativeTargets(markdown, base) {
  return String(markdown ?? '')
    .replace(IMAGE_PATTERN, (match, alt, src) => {
      const url = resolveMarkdownUrl(src, base)
      return url ? `![${alt}](${url})` : match
    })
    .replace(LINK_PATTERN, (match, label, href) => {
      // Skip the images just rewritten above (they re-match as links).
      if (match.startsWith('!')) return match
      const url = resolveMarkdownUrl(href, base)
      return url ? `[${label}](${url})` : match
    })
    .replace(HTML_ATTRIBUTE_PATTERN, (match, prefix, doubleQuoted, singleQuoted, bare) => {
      const raw = doubleQuoted ?? singleQuoted ?? bare
      const url = resolveMarkdownUrl(raw, base)
      if (!url || url === raw) return match
      const quote = doubleQuoted !== undefined ? '"' : singleQuoted !== undefined ? "'" : ''
      return `${prefix}${quote}${url}${quote}`
    })
}

// Renders Markdown into `element`, a node the caller lends to Vditor for the
// lifetime of the document (Vue must not own its children afterwards).
// Resolves once the Markdown HTML is in place; syntax highlighting and the
// diagram renderers may keep updating the same element afterwards.
export async function renderMarkdownInto(element, source, { base = '', locale = 'en' } = {}) {
  const [, Vditor] = await Promise.all([loadStyles(), loadVditor()])
  await Vditor.preview(element, rewriteRelativeTargets(source, base), {
    cdn: VDITOR_CDN,
    // The app ships a single dark look, so both the content theme and the
    // plugin mode (mermaid, echarts) are fixed instead of following the OS.
    mode: 'dark',
    theme: { current: 'dark', path: `${VDITOR_CDN}/dist/css/content-theme` },
    lang: locale === 'zh' ? 'zh_CN' : 'en_US',
    hljs: { enable: true, lineNumber: false, style: 'github-dark' },
    markdown: {
      autoSpace: true,
      fixTermTypo: true,
      gfmAutoLink: true,
      listStyle: false,
      toc: false,
      // Raw HTML in the document is allowed, but scripts and event handlers
      // are not: a course folder can hold files OfflineU did not write.
      sanitize: true,
    },
    math: { engine: 'KaTeX' },
    // Diagrams and charts stay enabled; speech is a reading feature tied to an
    // audio element the viewer does not have.
    speech: { enable: false },
    // Headings get their ids but no extra anchor links in a read-only view.
    anchor: 0,
    customEmoji: {},
    emojiPath: `${VDITOR_CDN}/dist/images/emoji`,
  })
}
