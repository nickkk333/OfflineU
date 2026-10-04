import { reactive } from 'vue'
import { api } from './api.js'

// A tiny shared store (no Pinia needed for an app this size): the home view and
// the app shell both read from it.
export const store = reactive({
  loaded: false,
  loading: false,
  error: '',
  version: '',
  course: null,
  tree: null,
  stats: null,
  recentCourses: [],
  roots: []
})

function applyState(payload) {
  store.version = payload.version || ''
  store.course = payload.course || null
  store.tree = payload.tree || null
  store.stats = payload.stats || null
  store.recentCourses = payload.recent_courses || []
  store.roots = payload.roots || []
  store.loaded = true
}

export async function refreshState() {
  store.loading = true
  store.error = ''
  try {
    applyState(await api.state())
  } catch (error) {
    store.error = error.message
  } finally {
    store.loading = false
  }
}

export async function loadCourse(coursePath) {
  const payload = await api.loadCourse(coursePath)
  await refreshState()
  return payload
}

export async function resetCourse() {
  await api.resetCourse()
  await refreshState()
}

export async function forgetCourse(path) {
  await api.forgetCourse(path)
  await refreshState()
}
