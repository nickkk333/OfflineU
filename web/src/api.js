// Thin wrapper around the Go JSON API. Every helper either returns the parsed
// payload or throws an Error carrying the backend's message (translated while
// the UI is not in English, see i18n.js).
import { acceptLanguage, translateServerMessage } from './i18n.js'

async function request(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    headers: {
      // Lets the backend localise the handful of strings it produces itself.
      'Accept-Language': acceptLanguage(),
      ...(options.body ? { 'Content-Type': 'application/json' } : {}),
      ...(options.headers || {})
    }
  })
  const text = await response.text()
  let data = null
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = null
    }
  }
  if (!response.ok) {
    throw new Error(translateServerMessage((data && data.error) || `Request failed with status ${response.status}`))
  }
  return data
}

export const api = {
  state: () => request('/api/state'),
  browse: (path) => request(`/browse?path=${encodeURIComponent(path || '')}`),
  loadCourse: (coursePath) =>
    request('/load_course', { method: 'POST', body: JSON.stringify({ course_path: coursePath }) }),
  resetCourse: () => request('/api/reset_course', { method: 'POST' }),
  forgetCourse: (path) =>
    request('/api/forget_course', { method: 'POST', body: JSON.stringify({ path }) }),
  lesson: (lessonPath, autoplay) =>
    request(`/api/lesson?path=${encodeURIComponent(lessonPath)}${autoplay ? '&autoplay=1' : ''}`),
  saveProgress: (lessonPath, { completed, seconds } = {}) => {
    const payload = { lesson_path: lessonPath }
    if (typeof completed === 'boolean') payload.completed = completed
    if (typeof seconds === 'number' && Number.isFinite(seconds)) {
      payload.progress_seconds = Math.floor(seconds)
    }
    return request('/api/progress', { method: 'POST', body: JSON.stringify(payload) })
  },
  // DLNA / UPnP casting: the server does the network search, the SPA only picks
  // a device and asks the server to hand it the lesson's media URL.
  castDevices: (refresh) => request(`/api/dlna/devices${refresh ? '?refresh=1' : ''}`),
  // converted === true asks the server to repackage/re-encode the file on the
  // fly (ffmpeg), which is what a TV needs for .mkv and friends.
  // duration is what the browser measured while reading the file; the server
  // needs a length to know when the cast ended (and to continue).
  castTo: (device, lessonPath, startSeconds, converted, autoplay, duration) =>
    request('/api/dlna/cast', {
      method: 'POST',
      body: JSON.stringify({
        device,
        lesson_path: lessonPath,
        start_seconds: Math.floor(startSeconds || 0),
        transcode: converted ? 'on' : 'off',
        autoplay: autoplay !== false,
        duration: Math.floor(duration || 0)
      })
    }),
  castControl: (device, action) =>
    request('/api/dlna/control', { method: 'POST', body: JSON.stringify({ device, action }) }),
  // Where the TV currently is - the browser polls this to follow the cast.
  castSession: () => request('/api/dlna/session')
}

// Builds the SPA route of a lesson ("Section 1/01 - Intro.mp4/Intro").
export function lessonRoute(lessonUrl) {
  if (!lessonUrl) return ''
  return `/lesson/${lessonUrl.split('/').map(encodeURIComponent).join('/')}`
}

export function formatTime(totalSeconds) {
  const seconds = Math.max(0, Math.floor(totalSeconds || 0))
  const minutes = Math.floor(seconds / 60)
  const rest = String(seconds % 60).padStart(2, '0')
  if (minutes < 60) return `${minutes}:${rest}`
  const hours = Math.floor(minutes / 60)
  return `${hours}:${String(minutes % 60).padStart(2, '0')}:${rest}`
}
