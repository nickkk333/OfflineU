// Cast scenarios: a fake DLNA renderer (e2e/fakerenderer) answers real SSDP and
// SOAP on this machine, so 单播循环 / 单播不循环 / 连播 can be verified against a
// running server end to end - discovery, the watchdog, mode switching while the
// "TV" plays, waking an ended cast and the cast bar in the browser.
//
//   node cast.mjs <base-url> [course-path]
import { spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright-core'
import { check, collectLessons, lanAddress, lessonRoute, note, request, summary, waitFor } from './lib.mjs'

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
async function cast(lessonPath, playMode) {
  const response = await request(base, '/api/dlna/cast', {
    method: 'POST',
    body: { device: UDN, lesson_path: lessonPath, start_seconds: 0, transcode: 'off', play_mode: playMode, duration: 6 },
  })
  if (!response.ok) throw new Error(`cast failed: ${response.text}`)
  return response.json
}
async function stopCast() {
  await request(base, '/api/dlna/control', { method: 'POST', body: { device: UDN, action: 'stop' } })
}
async function setCastMode(mode, device) {
  return request(base, '/api/settings', { method: 'POST', body: { cast_play_mode: mode, ...(device ? { device } : {}) } })
}
async function waitForEnded(timeout = 25000) {
  return waitFor(
    'the cast to end',
    async () => {
      const session = await castSession()
      return session && session.state === 'ended' ? session : null
    },
    { timeout, interval: 400 },
  )
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

    // ---- C2: 单播不循环 stops the device ------------------------------
    await fakeReset()
    await cast(alpha.rel_path, 'once')
    const onceSession = await waitForEnded()
    check('C2 单播不循环: session reports state=ended', onceSession.state === 'ended', onceSession.state)
    let events = await fakeEvents()
    check('C2 device was told to Play', events.some((event) => event.action === 'Play'))
    check('C2 device was stopped at the end', events.some((event) => event.action === 'Stop'), events.map((event) => event.action).join(','))
    check('C2 cast_play_mode stored as once', (await state()).cast_play_mode === 'once')

    // ---- C3: 单播循环 repeats the same lesson ---------------------------
    await fakeReset()
    await cast(alpha.rel_path, 'loop')
    await waitFor(
      'the loop to push Alpha a second time',
      async () => (await setUris()).filter((uri) => uri.includes('Alpha')).length >= 2,
      { timeout: 40000, interval: 500 },
    )
    check('C3 单播循环: the same lesson was handed to the device again', true)
    const loopSession = await castSession()
    check('C3 session still active with play_mode=loop', loopSession.active && loopSession.play_mode === 'loop', `${loopSession.state}/${loopSession.play_mode}`)
    await stopCast()

    // ---- C4: 连播 walks the course and stops after the last lesson ------
    await fakeReset()
    await cast(alpha.rel_path, 'next')
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
      'C4 连播 order: Alpha -> Beta -> Gamma',
      chain[0].includes('Alpha') && chain[1].includes('Beta') && chain[2].includes('Gamma'),
      chain.join('  ->  '),
    )
    check('C4 the document lesson (Notes.txt) is never pushed', !chain.some((uri) => uri.includes('Notes')), chain.join('  ->  '))
    const endSession = await castSession()
    check('C4 after the last lesson the cast ends', endSession.state === 'ended', endSession.state)
    const alphaLesson = (await request(base, `/api/lesson?path=${encodeURIComponent(alpha.url)}`)).json
    check('C4 the finished lesson is marked completed', alphaLesson?.lesson?.completed === true, String(alphaLesson?.lesson?.completed))

    // ---- C5: a cast that ended in 单播不循环 wakes up again -------------
    await fakeReset()
    await cast(alpha.rel_path, 'once')
    await waitForEnded()
    await setCastMode('loop')
    await waitFor(
      'a repeat after the play mode changed',
      async () => (await setUris()).filter((uri) => uri.includes('Alpha')).length >= 2,
      { timeout: 30000, interval: 500 },
    )
    check('C5 switching to 循环 wakes an ended cast up again', true)
    await stopCast()

    // ---- C6: switching the mode while the device is playing ------------
    await fakeReset()
    await cast(alpha.rel_path, 'once')
    const started = await castSession()
    check('C6 cast starts in 单播不循环', started.play_mode === 'once', started.play_mode)
    await setCastMode('next')
    const switched = await castSession()
    check('C6 mode switches live while the device plays', switched.play_mode === 'next', switched.play_mode)
    await waitFor(
      'the next lesson after the live switch',
      async () => (await setUris()).some((uri) => uri.includes('Beta')),
      { timeout: 35000, interval: 500 },
    )
    check('C6 单播不循环 -> 连播 mid-cast continues with Beta', true)

    // ---- C7: the browser mode and the cast mode never touch each other -
    const beforeSettings = await state()
    const beforeSession = await castSession()
    await request(base, '/api/settings', { method: 'POST', body: { play_mode: 'loop' } })
    const afterSettings = await state()
    const afterSession = await castSession()
    check('C7 browser play_mode changed to loop', afterSettings.play_mode === 'loop', afterSettings.play_mode)
    check(
      'C7 cast_play_mode untouched by the browser change',
      afterSettings.cast_play_mode === beforeSettings.cast_play_mode,
      `${beforeSettings.cast_play_mode} -> ${afterSettings.cast_play_mode}`,
    )
    check(
      'C7 running cast keeps its own mode',
      afterSession.play_mode === beforeSession.play_mode,
      `${beforeSession.play_mode} -> ${afterSession.play_mode}`,
    )
    await stopCast()

    // ---- C8: the cast bar in the browser -------------------------------
    await fakeReset()
    await cast(alpha.rel_path, 'loop')
    const browser = await chromium.launch({
      channel: 'msedge',
      headless: true,
      args: ['--autoplay-policy=no-user-gesture-required'],
    })
    try {
      const page = await browser.newPage()
      const posts = []
      page.on('request', (req) => {
        if (req.method() === 'POST' && req.url().includes('/api/settings')) {
          try {
            posts.push(JSON.parse(req.postData()))
          } catch {
            /* not JSON - ignored */
          }
        }
      })
      await page.goto(base + lessonRoute(alpha))
      await page.waitForSelector('.cast-bar', { timeout: 15000 })
      const castButtons = await page.locator('.cast-bar .seg__btn').count()
      check('C8 cast bar shows the three modes', castButtons === 3, `buttons=${castButtons}`)
      const castActive = await page.evaluate(() => {
        const buttons = [...document.querySelectorAll('.cast-bar .seg__btn')]
        return buttons.findIndex((button) => button.classList.contains('seg__btn--active'))
      })
      check('C8 cast bar shows the running cast mode (循环)', castActive === 0, String(castActive))
      await page.locator('.cast-bar .seg__btn').nth(2).click()
      await waitFor(
        'cast_play_mode=next posted from the cast bar',
        () => posts.some((post) => post.cast_play_mode === 'next'),
        { timeout: 6000 },
      )
      const uiState = await state()
      check('C8 cast mode switched from the UI', uiState.cast_play_mode === 'next', uiState.cast_play_mode)
      check('C8 the browser mode is untouched by the cast bar', uiState.play_mode === 'loop', uiState.play_mode)
      const uiSession = await castSession()
      check('C8 running cast picked the new mode up', uiSession.play_mode === 'next', uiSession.play_mode)
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
