// API-level scenarios for 单播循环 / 单播不循环 / 连播 against a running server.
//
//   node api.mjs <base-url> [course-path]
//   node api.mjs http://127.0.0.1:5100 "D:\Documents\OfflineU\courses\E2E Course"
//   node api.mjs http://127.0.0.1:5200 "/courses/E2E Course"        (docker/fpk)
import { check, collectLessons, note, request, summary } from './lib.mjs'

const base = process.argv[2] || 'http://127.0.0.1:5100'
const coursePath = process.argv[3] || 'D:\\Documents\\OfflineU\\courses\\E2E Course'
const MODES = ['once', 'loop', 'next']

async function state() {
  return (await request(base, '/api/state')).json
}

async function main() {
  console.log(`API scenarios against ${base}`)

  // A1: health endpoint (docker HEALTHCHECK uses it too).
  const health = await request(base, '/health')
  check('A1 /health responds 200', health.status === 200, `status=${health.status}`)

  // Make sure the E2E course is loaded (the exe gets it on the command line).
  let payload = await state()
  if (!payload || !payload.course) {
    note(`no active course - loading ${coursePath}`)
    const loaded = await request(base, '/load_course', { method: 'POST', body: { course_path: coursePath } })
    check('A2 course loads', loaded.ok, loaded.text.slice(0, 160))
    payload = await state()
  }
  check('A2 course is active', Boolean(payload && payload.course), payload?.course?.name || 'none')

  const lessons = collectLessons(payload.tree)
  const byTitle = (title) => lessons.find((lesson) => lesson.title === title)
  const alpha = byTitle('Alpha')
  const notes = byTitle('Notes')
  const beta = byTitle('Beta')
  const gamma = byTitle('Gamma')
  check(
    'A3 E2E course parsed (Alpha, Notes.txt, Beta, Gamma)',
    alpha && notes && beta && gamma,
    lessons.map((lesson) => lesson.title).join(', '),
  )
  if (!alpha || !beta || !gamma) return

  // A4: the defaults are one of the three modes.
  check('A4 play_mode is a valid mode', MODES.includes(payload.play_mode), payload.play_mode)
  check('A4 cast_play_mode is a valid mode', MODES.includes(payload.cast_play_mode), payload.cast_play_mode)

  // A5: browser mode and cast mode are stored independently. The cast mode
  // starts unset and - by design (CourseStore.CastPlayMode) - mirrors the
  // browser mode until it is chosen on its own, so pin it first.
  await request(base, '/api/settings', { method: 'POST', body: { cast_play_mode: 'once' } })
  const castBefore = (await state()).cast_play_mode
  let response = await request(base, '/api/settings', { method: 'POST', body: { play_mode: 'loop' } })
  check('A5 POST play_mode=loop accepted', response.ok && response.json?.play_mode === 'loop', response.text.slice(0, 160))
  check(
    'A5 changing the browser mode leaves the cast mode alone',
    response.json?.cast_play_mode === castBefore,
    `${castBefore} -> ${response.json?.cast_play_mode}`,
  )

  response = await request(base, '/api/settings', { method: 'POST', body: { cast_play_mode: 'next' } })
  check('A6 POST cast_play_mode=next accepted', response.ok && response.json?.cast_play_mode === 'next', response.text.slice(0, 160))
  check('A6 changing the cast mode leaves the browser mode alone', response.json?.play_mode === 'loop', response.json?.play_mode)

  // A7: an empty settings body is rejected...
  response = await request(base, '/api/settings', { method: 'POST', body: {} })
  check('A7 empty settings body rejected', response.status === 400, `status=${response.status}`)

  // ...and an unknown mode falls back to the product default without touching
  // the other setting.
  response = await request(base, '/api/settings', { method: 'POST', body: { play_mode: 'nonsense' } })
  check('A8 nonsense play_mode normalizes to once', response.ok && response.json?.play_mode === 'once', response.text.slice(0, 160))
  check('A8 nonsense kept cast_play_mode', response.json?.cast_play_mode === 'next', response.json?.cast_play_mode)

  // A9: the next playable lesson - the document (Notes.txt) must be skipped.
  response = await request(base, `/api/lesson?path=${encodeURIComponent(alpha.url)}`)
  const alphaPayload = response.json
  check('A9 autoplay target of Alpha is Beta (02 - Notes.txt skipped)', alphaPayload?.autoplay_url === beta.url, alphaPayload?.autoplay_url)
  const expectedHref = `/lesson/${(alphaPayload?.autoplay_url || '').split('/').map(encodeURIComponent).join('/')}?autoplay=1`
  check('A10 autoplay href carries ?autoplay=1', alphaPayload?.autoplay_href === expectedHref, alphaPayload?.autoplay_href)
  check('A11 Alpha has a playable video', Boolean(alphaPayload?.lesson?.video_src), alphaPayload?.lesson?.video_src)

  // A12: the last lesson must not promise a next one.
  response = await request(base, `/api/lesson?path=${encodeURIComponent(gamma.url)}`)
  check(
    'A12 last lesson has no autoplay target',
    response.ok && !response.json?.autoplay_url && !response.json?.autoplay_href,
    response.json?.autoplay_url,
  )

  // A13: ?autoplay=1 is forwarded to the player.
  response = await request(base, `/api/lesson?path=${encodeURIComponent(alpha.url)}&autoplay=1`)
  check('A13 ?autoplay=1 sets requested_autoplay', response.ok && response.json?.requested_autoplay === true, String(response.json?.requested_autoplay))

  // A14: the SPA shell, a deep link and the media bytes are all served.
  const index = await request(base, '/')
  check('A14 SPA index served', index.status === 200 && index.text.includes('id="app"'), `status=${index.status}`)
  const deepLink = await request(base, `/lesson/${alpha.url.split('/').map(encodeURIComponent).join('/')}`)
  check('A15 /lesson/... deep link falls back to the SPA', deepLink.status === 200 && deepLink.text.includes('id="app"'), `status=${deepLink.status}`)
  const media = await fetch(new URL(alphaPayload.lesson.video_src, base))
  const mediaType = media.headers.get('content-type') || ''
  check('A16 media file streams as video', media.status === 200 && mediaType.includes('video'), `${media.status} ${mediaType}`)
  await media.body?.cancel()

  // A17: the settings made it into the state every client reads. The browser
  // mode is left on "loop": the browser scenarios open a fresh browser whose
  // localStorage knows nothing, so it can only show the right mode when the
  // lesson page really picks the server value up.
  await request(base, '/api/settings', { method: 'POST', body: { play_mode: 'loop' } })
  payload = await state()
  check('A17 play_mode persisted', payload?.play_mode === 'loop', payload?.play_mode)
  check('A17 cast_play_mode persisted', payload?.cast_play_mode === 'next', payload?.cast_play_mode)
}

try {
  await main()
} catch (error) {
  check('api scenarios ran without crashing', false, error.message)
}
summary(`API (${base})`)
