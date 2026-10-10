// Cast scenarios: a fake DLNA renderer (e2e/fakerenderer) answers real SSDP and
// SOAP on this machine, so 连播 can be verified against a running server end to
// end - discovery, the watchdog walking the course, stopping after the last
// lesson and the cast bar in the browser.
//
//   node cast.mjs <base-url> [course-path]
import { spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright-core'
import { check, collectLessons, lanAddress, lessonRoute, request, summary, waitFor } from './lib.mjs'

const base = process.argv[2] || 'http://127.0.0.1:5100'
const fakeBin = process.argv[3] || fileURLToPath(new URL('./fakerenderer.exe', import.meta.url))
const fakeBase = 'http://127.0.0.1:7920'
const UDN = 'uuid:offlineu-e2e-renderer'

async function state() {
  return (await request(base, '/api/state')).json
}
async function castSession() {
  return (await request(base, '/api/dlna/session')).json
}
async function fakeEvents() {
  const payload = (await request(fakeBase, '/state')).json
  return payload ? payload.events : []
}
async function setUris() {
  return (await fakeEvents()).filter((event) => event.action === 'SetAVTransportURI').map((event) => event.uri)
}
async function fakeReset() {
  await request(fakeBase, '/reset', { method: 'POST' })
}
async function cast(lessonPath) {
  const response = await request(base, '/api/dlna/cast', {
    method: 'POST',
    body: { device: UDN, lesson_path: lessonPath, start_seconds: 0, transcode: 'off', duration: 6 },
  })
  if (!response.ok) throw new Error(`cast failed: ${response.text}`)
  return response.json
}
async function stopCast() {
  await request(base, '/api/dlna/control', { method: 'POST', body: { device: UDN, action: 'stop' } })
}
async function main() {
  console.log(`Cast scenarios against ${base}`)

  // ---- start the fake renderer ---------------------------------------
  const advertise = lanAddress()
  const fake = spawn(fakeBin, ['-http', '0.0.0.0:7920', '-advertise', advertise], { stdio: 'inherit' })
  const stopFake = () => {
    try {
      fake.kill()
    } catch {
      /* already gone */
    }
  }
  await waitFor(
    'the fake renderer to answer',
    async () => (await request(fakeBase, '/state')).ok,
    { timeout: 10000, interval: 300 },
  ).catch((error) => {
    stopFake()
    throw error
  })

  try {
    const payload = await state()
    const lessons = collectLessons(payload.tree)
    const byTitle = (title) => lessons.find((lesson) => lesson.title === title)
    const alpha = byTitle('Alpha')
    const beta = byTitle('Beta')
    const gamma = byTitle('Gamma')
    check('C0 course lessons found', Boolean(alpha && beta && gamma), lessons.map((lesson) => lesson.title).join(', '))
    if (!alpha || !beta || !gamma) return

    // ---- C1: SSDP discovery ------------------------------------------
    const devices = await waitFor(
      'the renderer to appear in /api/dlna/devices',
      async () => {
        const response = await request(base, '/api/dlna/devices?refresh=1')
        if (!response.ok || !response.json?.devices) return null
        return response.json.devices.find((device) => device.udn === UDN) || null
      },
      { timeout: 20000, interval: 1000 },
    )
    check('C1 SSDP discovery finds the fake renderer', true, `${devices.name} @ ${devices.address}`)
    check('C1 renderer exposes an AVTransport control URL', Boolean(devices.control_url), devices.control_url)

    // ---- C2: 连播 walks the course and stops after the last lesson ------
    await fakeReset()
    await cast(alpha.rel_path)
    const chain = await waitFor(
      'the 连播 chain Alpha -> Beta -> Gamma -> stop',
      async () => {
        const events = await fakeEvents()
        const uris = events.filter((event) => event.action === 'SetAVTransportURI').map((event) => event.uri)
        const stopped = events.some((event) => event.action === 'Stop')
        return uris.length >= 3 && stopped ? uris : null
      },
      { timeout: 60000, interval: 500 },
    )
    check(
      'C2 连播 order: Alpha -> Beta -> Gamma',
      chain[0].includes('Alpha') && chain[1].includes('Beta') && chain[2].includes('Gamma'),
      chain.join('  ->  '),
    )
    check('C2 the document lesson (Notes.txt) is never pushed', !chain.some((uri) => uri.includes('Notes')), chain.join('  ->  '))
    const endSession = await castSession()
    check('C2 after the last lesson the cast ends', endSession.state === 'ended', endSession.state)
    const alphaLesson = (await request(base, `/api/lesson?path=${encodeURIComponent(alpha.url)}`)).json
    check('C3 the finished lesson is marked completed', alphaLesson?.lesson?.completed === true, String(alphaLesson?.lesson?.completed))

    // ---- C4: the cast bar in the browser -------------------------------
    await fakeReset()
    await cast(alpha.rel_path)
    const browser = await chromium.launch({
      channel: 'msedge',
      headless: true,
      args: ['--autoplay-policy=no-user-gesture-required'],
    })
    try {
      const page = await browser.newPage()
      await page.goto(base + lessonRoute(alpha))
      await page.waitForSelector('.cast-bar', { timeout: 15000 })
      const castButtons = await page.locator('.cast-bar .seg__btn').count()
      check('C4 cast bar shows no play-mode buttons', castButtons === 0, `buttons=${castButtons}`)
      const nextUp = await page.locator('.cast-bar .faint').last().textContent()
      check('C4 cast bar names the lesson that follows (Beta)', (nextUp || '').includes('Beta'), nextUp || '')
      const uiSession = await castSession()
      check('C4 the running cast is active', uiSession.active === true, String(uiSession.active))
    } finally {
      try {
        await browser.close()
      } catch {
        /* already closed */
      }
    }
    await stopCast()
  } finally {
    stopFake()
  }
}

try {
  await main()
} catch (error) {
  check('cast scenarios ran without crashing', false, error.message)
}
summary(`CAST (${base})`)
