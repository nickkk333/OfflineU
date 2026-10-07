// What happens when a lesson ends: 单播循环 (loop), 单播不循环 (once) or 连播
// (next). One choice for the whole app, because the same three modes drive the
// browser player and a cast on the TV.
//
// The server is the source of truth: the choice is stored there (next to the
// course bookkeeping) and handed to every client through /api/state, so it
// survives a new browser, another machine or a cleared cache. localStorage is
// only a cache for the first paint and for the moment before the state arrives.
import { ref, watch } from 'vue'
import { store } from '../store.js'
import { api } from '../api.js'

export const PLAY_MODES = [
  { id: 'loop', label: 'lesson.modeLoop', title: 'lesson.modeLoopHint' },
  { id: 'once', label: 'lesson.modeOnce', title: 'lesson.modeOnceHint' },
  { id: 'next', label: 'lesson.modeNext', title: 'lesson.modeNextHint' }
]

const MODE_KEY = 'offlineu.playMode'
// Old releases stored a plain "autoplay next" checkbox; a choice made there is
// honoured once and then replaced by the three mode values.
const LEGACY_AUTOPLAY_KEY = 'offlineu.autoplayNext'

function readPreference(key, fallback) {
  try {
    const value = window.localStorage.getItem(key)
    return value === null ? fallback : value
  } catch {
    return fallback
  }
}

function writePreference(key, value) {
  try {
    window.localStorage.setItem(key, value)
  } catch {
    /* storage disabled (private mode): the server still remembers the choice */
  }
}

// Anything an old release (or a hand-typed value) stored becomes one of the
// three modes; unknown and empty values fall back to the product default.
export function normalizePlayMode(value) {
  return value === 'loop' || value === 'next' ? value : 'once'
}

function readPlayMode() {
  const stored = readPreference(MODE_KEY, '')
  if (stored === 'loop' || stored === 'once' || stored === 'next') return stored
  const legacy = readPreference(LEGACY_AUTOPLAY_KEY, '')
  if (legacy === '1') return 'next'
  if (legacy === '0') return 'once'
  return 'once'
}

// A module level singleton (like the cast in useCast.js): every control reads
// and writes this one ref, so the lesson page and the cast bar cannot drift
// apart.
export const playMode = ref(readPlayMode())

// Adopt a mode without talking to the server: used when the value came from the
// server in the first place.
export function applyPlayMode(mode) {
  const next = normalizePlayMode(mode)
  playMode.value = next
  writePreference(MODE_KEY, next)
  return next
}

// The setting the server handed out wins: it is the global one, shared by every
// browser, while the value in localStorage may be from another machine.
watch(
  () => store.playMode,
  (mode) => {
    if (mode === 'loop' || mode === 'once' || mode === 'next') applyPlayMode(mode)
  },
  { immediate: true }
)

// Remember the choice - locally for the first paint and on the server, which is
// where it becomes global. A cast that is running picks it up at the same time,
// so switching 循环 / 连播 works while the TV is playing.
export async function setPlayMode(mode, device) {
  const next = applyPlayMode(mode)
  try {
    const payload = await api.setPlayMode(next, device)
    if (payload && (payload.play_mode === 'loop' || payload.play_mode === 'once' || payload.play_mode === 'next')) {
      applyPlayMode(payload.play_mode)
      return payload
    }
  } catch {
    /* the server is unreachable: the local choice stands until it is back */
  }
  return { play_mode: next }
}
