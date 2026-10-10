// Browser scenarios: a real Edge instance drives the SPA and plays the 6 s MP4s
// of courses/E2E Course. A lesson always continues with the next one (连播) -
// there is no mode to pick anywhere in the UI.
//
//   node browser.mjs <base-url> [course-path]
import { chromium } from 'playwright-core'
import { check, collectLessons, lessonRoute, note, request, summary, waitFor } from './lib.mjs'

const base = process.argv[2] || 'http://127.0.0.1:5100'

async function state() {
  return (await request(base, '/api/state')).json
}

// reachEnd jumps the player to the last fraction of a second so the real
// "ended" event fires almost immediately.
async function reachEnd(page) {
  await page.evaluate(async () => {
    const video = document.querySelector('video')
    if (!video) throw new Error('no <video> on the page')
    if (!Number.isFinite(video.duration)) {
      await new Promise((resolve) => video.addEventListener('loadedmetadata', resolve, { once: true }))
    }
    video.muted = true
    video.currentTime = Math.max(0, video.duration - 0.15)
    try {
      await video.play()
    } catch {
      /* the flag on launch already allows autoplay */
    }
  })
}

let browserRef = null // so an error can still close Edge (a hung run blocks the next one)

async function main() {
  console.log(`Browser scenarios against ${base}`)
  const payload = await state()
  const lessons = collectLessons(payload.tree)
  const byTitle = (title) => lessons.find((lesson) => lesson.title === title)
  const alpha = byTitle('Alpha')
  const beta = byTitle('Beta')
  const gamma = byTitle('Gamma')
  check('B0 course lessons found', Boolean(alpha && beta && gamma), lessons.map((lesson) => lesson.title).join(', '))
  if (!alpha || !beta || !gamma) return

  const browser = await chromium.launch({
    channel: 'msedge',
    headless: true,
    args: ['--autoplay-policy=no-user-gesture-required'],
  })
  browserRef = browser
  const page = await browser.newPage()

  // ---- B1: the player toolbar has no play-mode control left ----------
  await page.goto(base + lessonRoute(alpha))
  await page.waitForSelector('.player-toolbar')
  await page.waitForFunction(() => {
    const video = document.querySelector('video')
    return video && Number.isFinite(video.duration) && video.duration > 0
  })
  const modeButtons = await page.locator('.player-toolbar .seg__btn').count()
  check('B1 player toolbar shows no play-mode buttons', modeButtons === 0, `buttons=${modeButtons}`)
  const upNext = await page.locator('.player-toolbar .faint').last().textContent()
  check('B1 the toolbar names the lesson that follows', (upNext || '').includes('Beta'), upNext || '')

  // ---- B2: speed carries over, 连播 skips the document lesson ----------
  await page.selectOption('.player-toolbar .input--select', '1.5')
  check('B2 speed preference stored', (await page.evaluate(() => localStorage.getItem('offlineu.playbackRate'))) === '1.5')

  await reachEnd(page)
  await page.waitForURL((url) => url.href.includes('03%20-%20Beta'), { timeout: 12000 })
  const advancedUrl = page.url()
  check('B2 连播 jumps to the next media lesson', advancedUrl.includes('Beta'), advancedUrl)
  check('B2 the document lesson (02 - Notes.txt) is skipped', !advancedUrl.includes('Notes'), advancedUrl)

  await waitFor(
    'playback speed carried over to the next lesson',
    () => page.evaluate(() => document.querySelector('video')?.playbackRate === 1.5),
    { timeout: 6000 },
  )
  check('B2 the picked speed carries over (连播 keeps 1.5x)', true)

  // ---- B3: ?autoplay=1 starts the media by itself ---------------------
  await page.goto(base + lessonRoute(gamma) + '?autoplay=1')
  await page.waitForSelector('video')
  await page.waitForTimeout(2500)
  const autoplaying = await page.evaluate(() => {
    const video = document.querySelector('video')
    return video ? !video.paused && video.currentTime > 0 : false
  })
  check('B3 ?autoplay=1 plays the lesson without a click', autoplaying)

  // ---- B4: the last lesson finishes where it is -----------------------
  await reachEnd(page)
  await page.waitForTimeout(2500)
  check('B4 last lesson stays put (no next target)', page.url().includes('Gamma'), page.url())
  const stopped = await page.evaluate(() => {
    const video = document.querySelector('video')
    return video ? { paused: video.paused, ended: video.ended } : null
  })
  check('B4 the last lesson ends without restarting', stopped && stopped.ended && stopped.paused, JSON.stringify(stopped))

  await browser.close()
}

try {
  await main()
} catch (error) {
  check('browser scenarios ran without crashing', false, error.message)
}
try {
  await browserRef?.close()
} catch {
  /* already closed */
}
summary(`BROWSER (${base})`)
