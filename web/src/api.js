// Thin wrapper around the Go JSON API. Every helper either returns the parsed
// payload or throws an Error carrying the backend's message.
async function request(path, options = {}) {
  const response = await fetch(path, {
    headers: options.body ? { 'Content-Type': 'application/json' } : undefined,
    ...options
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
    throw new Error((data && data.error) || `Request failed with status ${response.status}`)
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
  }
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
