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
  roots: [],
  rootsDisplay: [],
  needsMount: false,
  // Why the picker is empty ("empty", "unreadable", "missing", "no_roots") plus
  // the per-folder state, so the notice can name the real cause.
  mountIssue: '',
  rootsDetail: [],
  mountHint: '',
  // Whether the process may bypass file permission checks (CAP_DAC_OVERRIDE of
  // /app/offlineu-cap): true/false inside a Linux container, null where the
  // question cannot be answered at all (desktop build, no /proc).
  readCapability: null,
  // Global "when the lesson ends" settings (once / loop / next) - one for the
  // browser player, one for a cast. The server keeps both, so a new browser -
  // or one with a cleared cache - shows the same choices. They are independent:
  // the two can play at the same time, so one never changes the other.
  playMode: '',
  castPlayMode: ''
})

function applyState(payload) {
  store.version = payload.version || ''
  store.course = payload.course || null
  store.tree = payload.tree || null
  store.stats = payload.stats || null
  store.recentCourses = payload.recent_courses || []
  store.roots = payload.roots || []
  store.rootsDisplay = payload.roots_display || []
  store.needsMount = Boolean(payload.needs_mount)
  store.mountIssue = payload.mount_issue || ''
  store.rootsDetail = payload.roots_detail || []
  store.mountHint = payload.mount_hint || ''
  // Only a plain false means "this container switched the capability off".
  store.readCapability = payload.read_capability === undefined ? null : payload.read_capability
  store.playMode = payload.play_mode || ''
  store.castPlayMode = payload.cast_play_mode || ''
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
