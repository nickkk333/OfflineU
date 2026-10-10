// API-level scenarios for 连播 (a lesson always continues with the next one)
// against a running server.
//
//   node api.mjs <base-url> [course-path]
//   node api.mjs http://127.0.0.1:5100 "D:\Documents\OfflineU\courses\E2E Course"
//   node api.mjs http://127.0.0.1:5200 "/courses/E2E Course"        (docker)
import { check, collectLessons, note, request, summary } from './lib.mjs'

const base = process.argv[2] || 'http://127.0.0.1:5100'
const coursePath = process.argv[3] || 'D:\\Documents\\OfflineU\\courses\\E2E Course'

async function state() {
  return (await request(base, '/api/state')).json
}

async function main() {
  console.log(`API scenarios against ${base}`)

  // A1: health endpoint (docker HEALTHCHECK uses it too).
  const health = await request(base, '/health')
  check('A1 /health responds 200', health.status === 200, `status=${health.status}`)

  // Make sure the E2E course is loaded (a local binary gets it on the command line).
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

  // A4: the next playable lesson - the document (Notes.txt) must be skipped.
  let response = await request(base, `/api/lesson?path=${encodeURIComponent(alpha.url)}`)
  const alphaPayload = response.json
  check('A4 autoplay target of Alpha is Beta (02 - Notes.txt skipped)', alphaPayload?.autoplay_url === beta.url, alphaPayload?.autoplay_url)
  const expectedHref = `/lesson/${(alphaPayload?.autoplay_url || '').split('/').map(encodeURIComponent).join('/')}?autoplay=1`
  check('A5 autoplay href carries ?autoplay=1', alphaPayload?.autoplay_href === expectedHref, alphaPayload?.autoplay_href)
  check('A6 Alpha has a playable video', Boolean(alphaPayload?.lesson?.video_src), alphaPayload?.lesson?.video_src)

  // A7: the last lesson must not promise a next one.
  response = await request(base, `/api/lesson?path=${encodeURIComponent(gamma.url)}`)
  check(
    'A7 last lesson has no autoplay target',
    response.ok && !response.json?.autoplay_url && !response.json?.autoplay_href,
    response.json?.autoplay_url,
  )

  // A8: ?autoplay=1 is forwarded to the player.
  response = await request(base, `/api/lesson?path=${encodeURIComponent(alpha.url)}&autoplay=1`)
  check('A8 ?autoplay=1 sets requested_autoplay', response.ok && response.json?.requested_autoplay === true, String(response.json?.requested_autoplay))

  // A9: the SPA shell, a deep link and the media bytes are all served.
  const index = await request(base, '/')
  check('A9 SPA index served', index.status === 200 && index.text.includes('id="app"'), `status=${index.status}`)
  const deepLink = await request(base, `/lesson/${alpha.url.split('/').map(encodeURIComponent).join('/')}`)
  check('A10 /lesson/... deep link falls back to the SPA', deepLink.status === 200 && deepLink.text.includes('id="app"'), `status=${deepLink.status}`)
  const media = await fetch(new URL(alphaPayload.lesson.video_src, base))
  const mediaType = media.headers.get('content-type') || ''
  check('A11 media file streams as video', media.status === 200 && mediaType.includes('video'), `${media.status} ${mediaType}`)
  await media.body?.cancel()
}

try {
  await main()
} catch (error) {
  check('api scenarios ran without crashing', false, error.message)
}
summary(`API (${base})`)
