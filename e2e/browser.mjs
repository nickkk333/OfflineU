// Browser scenarios: a real Edge instance drives the SPA and plays the 6 s MP4s
// of courses/E2E Course through all three play modes.
//
//   node browser.mjs <base-url> [course-path]
import { chromium } from 'playwright-core'
import { check, collectLessons, lessonRoute, note, request, summary, waitFor } from './lib.mjs'

const base = process.argv[2] || 'http://127.0.0.1:5100'
const MODE_INDEX = { loop: 0, once: 1, next: 2 } // PLAY_MODES order in usePlayMode.js

async function state() {
  return (await request(base, '/api/state')).json
}

async function activeModeIndex(page) {
  return page.evaluate(() => {
    const buttons = [...document.querySelectorAll('.player-toolbar .seg__btn')]
    return buttons.findIndex((button) => button.classList.contains('seg__btn--active'))
  })
}

async function clickMode(page, mode) {
  await page.locator('.player-toolbar .seg__btn').nth(MODE_INDEX[mode]).click()
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

const settingsPosts = []
let browserRef = null // so an error can still close Edge (a hung run blocks the next one)

// settingsBody parses a captured /api/settings request (null when it is none).
function settingsBody(response) {
  if (!response.url().includes('/api/settings') || response.request().method() !== 'POST') return null
  try {
    return JSON.parse(response.request().postData())
  } catch {
    return null
  }
}

// clickModeAndWait arms the response listener *before* the click: the POST is
// dispatched while Playwright is still finishing the click, so a listener that
// is attached afterwards can miss the response and time out. Resolving on the
// response (not on the click) also means a reload right after cannot cancel it.
async function clickModeAndWait(page, mode) {
  const waited = page.waitForResponse(
    (response) => {
      const body = settingsBody(response)
      return body ? body.play_mode === mode : false
    },
    { timeout: 8000 },
  )
  try {
    await clickMode(page, mode)
  } catch (error) {
    waited.catch(() => {}) // no unhandled rejection when the click itself fails
    throw error
  }
  return waited
}

async function main() {
  console.log(`Browser scenarios against ${base}`)
  // The toolbar has to open on the mode the server has stored, so the run fixes
  // that value itself instead of relying on the previous state. 循环 is used
  // because its index is 0, i.e. the value a truthiness-based probe would miss.
  const seeded = await request(base, '/api/settings', { method: 'POST', body: { play_mode: 'loop' } })
  check('B0 server mode set to 循环', seeded.ok, `status=${seeded.status}`)
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
  page.on('request', (request) => {
    if (request.method() === 'POST' && request.url().includes('/api/settings')) {
      try {
        settingsPosts.push(JSON.parse(request.postData()))
      } catch {
        settingsPosts.push({ raw: request.postData() })
      }
    }
  })

  // ---- B1: the segmented control writes the browser mode --------------
  await page.goto(base + lessonRoute(alpha))
  await page.waitForSelector('.player-toolbar .seg__btn')
  await page.waitForFunction(() => {
    const video = document.querySelector('video')
    return video && Number.isFinite(video.duration) && video.duration > 0
  })
  const buttonCount = await page.locator('.player-toolbar .seg__btn').count()
  check('B1 player toolbar shows the three modes', buttonCount === 3, `buttons=${buttonCount}`)

  const serverMode = payload.play_mode
  const shownMode = await waitFor(
    'the toolbar to show the server mode',
    async () => {
      const index = await activeModeIndex(page)
      // waitFor keeps polling while the probe result is falsy, so the match
      // has to be wrapped: 循环 is index 0 and a bare 0 would never be
      // accepted, i.e. the check could not pass for the default mode.
      return index === MODE_INDEX[serverMode] ? { index } : null
    },
    { timeout: 8000 },
  ).catch(() => null)
  check(
    'B1 fresh browser on a deep link shows the server mode',
    Boolean(shownMode) && shownMode.index === MODE_INDEX[serverMode],
    `server=${serverMode}, shown=${shownMode ? shownMode.index : null}`
  )

  const nextResponse = await clickModeAndWait(page, 'next')
  check('B1 picking 连播 posts play_mode=next', nextResponse.status() === 200, `status=${nextResponse.status()}`)
  check('B1 localStorage remembers it', (await page.evaluate(() => localStorage.getItem('offlineu.playMode'))) === 'next')

  // A cleared cache must not lose the choice: the server value wins.
  await page.evaluate(() => localStorage.clear())
  await page.reload()
  await page.waitForSelector('.player-toolbar .seg__btn')
  const serverAfterReload = await state()
  check('B1 server stored the choice', serverAfterReload.play_mode === 'next', serverAfterReload.play_mode)
  const shownAfterReload = await waitFor(
    'the reloaded toolbar to show the server mode',
    async () => {
      const index = await activeModeIndex(page)
      return index === MODE_INDEX.next ? index : null
    },
    { timeout: 8000 },
  ).catch(() => null)
  check('B1 mode survives a reload with an empty localStorage', shownAfterReload === MODE_INDEX.next, `shown=${shownAfterReload}`)
  const storedAfterReload = await waitFor(
    'localStorage to be repopulated from the server',
    async () => {
      const value = await page.evaluate(() => localStorage.getItem('offlineu.playMode'))
      return value === 'next' ? value : null
    },
    { timeout: 8000 },
  ).catch(() => null)
  check('B1 localStorage is repopulated from the server', storedAfterReload === 'next', String(storedAfterReload))

  // ---- B2: speed carries over, 连播 skips the document lesson ----------
  await page.selectOption('.player-toolbar .input--select', '1.5')
  check('B2 speed preference stored', (await page.evaluate(() => localStorage.getItem('offlineu.playbackRate'))) === '1.5')

  await reachEnd(page)
  await page.waitForURL((url) => url.href.includes('03%20-%20Beta'), { timeout: 12000 })
  const advancedUrl = page.url()
  check('B2 连播 jumps to the next media lesson', advancedUrl.includes('Beta'), advancedUrl)
  check('B2 the document lesson (02 - Notes.txt) is skipped', !advancedUrl.includes('Notes'), advancedUrl)

  // Switch to 循环 right away so a natural end can never advance the page.
  await page.waitForSelector('.player-toolbar .seg__btn')
  const loopResponse = await clickModeAndWait(page, 'loop')
  check('B2 循环 posted from the toolbar', loopResponse.status() === 200, `status=${loopResponse.status()}`)

  await waitFor(
    'playback speed carried over to the next lesson',
    () => page.evaluate(() => document.querySelector('video')?.playbackRate === 1.5),
    { timeout: 6000 },
  )
  check('B2 the picked speed carries over (连播 keeps 1.5x)', true)

  // ---- B3: 单播循环 replays the same lesson ---------------------------
  await reachEnd(page)
  await page.waitForTimeout(2500)
  check('B3 循环 stays on the same lesson', page.url().includes('Beta'), page.url())
  const looped = await page.evaluate(() => {
    const video = document.querySelector('video')
    return video ? { time: video.currentTime, paused: video.paused, ended: video.ended } : null
  })
  check('B3 循环 restarts playback from the top', looped && !looped.paused && !looped.ended && looped.time < 4.5, JSON.stringify(looped))
  check('B3 localStorage mode is loop', (await page.evaluate(() => localStorage.getItem('offlineu.playMode'))) === 'loop')

  // ---- B4: 单播不循环 stops at the end --------------------------------
  const onceResponse = await clickModeAndWait(page, 'once')
  check('B4 单播不循环 posted from the toolbar', onceResponse.status() === 200, `status=${onceResponse.status()}`)
  await reachEnd(page)
  await page.waitForTimeout(2500)
  check('B4 单播不循环 does not navigate away', page.url().includes('Beta'), page.url())
  const stopped = await page.evaluate(() => {
    const video = document.querySelector('video')
    return video ? { paused: video.paused, ended: video.ended, time: video.currentTime } : null
  })
  check('B4 单播不循环 ends playback without restarting', stopped && stopped.ended && stopped.paused, JSON.stringify(stopped))
  const afterOnce = await state()
  check('B4 server play_mode is once', afterOnce.play_mode === 'once', afterOnce.play_mode)

  // ---- B5: ?autoplay=1 starts the media by itself ---------------------
  await page.goto(base + lessonRoute(gamma) + '?autoplay=1')
  await page.waitForSelector('video')
  await page.waitForTimeout(2500)
  const autoplaying = await page.evaluate(() => {
    const video = document.querySelector('video')
    return video ? !video.paused && video.currentTime > 0 : false
  })
  check('B5 ?autoplay=1 plays the lesson without a click', autoplaying)

  // ---- B6: the last lesson in 循环/连播 finishes where it is -----------
  const lastResponse = await clickModeAndWait(page, 'next')
  check('B6 连播 posted from the toolbar', lastResponse.status() === 200, `status=${lastResponse.status()}`)
  await reachEnd(page)
  await page.waitForTimeout(2500)
  check('B6 last lesson stays put (no next target)', page.url().includes('Gamma'), page.url())

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
