// What happens when a lesson ends: 单播循环 (loop), 单播不循环 (once) or 连播 (next).
//
// The browser player and a cast keep *their own* choice: both can play at the
// same time, so switching one must never change the other. Both are stored on
// the server (next to the course bookkeeping) and handed to every client through
// /api/state, so each of them survives a new browser, another machine or a
// cleared cache. localStorage is only the first-paint cache.
import { ref, watch } from 'vue'
import { store } from '../store.js'
import { api } from '../api.js'

export const PLAY_MODES = [
  { id: 'loop', label: 'lesson.modeLoop', title: 'lesson.modeLoopHint' },
  { id: 'once', label: 'lesson.modeOnce', title: 'lesson.modeOnceHint' },
  { id: 'next', label: 'lesson.modeNext', title: 'lesson.modeNextHint' }
]

const MODE_KEY = 'offlineu.playMode'          // browser player
const CAST_MODE_KEY = 'offlineu.castPlayMode' // cast on a TV
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

function readMode(key, legacyKey) {
  const stored = readPreference(key, '')
  if (stored === 'loop' || stored === 'once' || stored === 'next') return stored
  if (legacyKey) {
    const legacy = readPreference(legacyKey, '')
    if (legacy === '1') return 'next'
    if (legacy === '0') return 'once'
  }
  return 'once'
}

// What the browser player does when a lesson ends.
export const playMode = ref(readMode(MODE_KEY, LEGACY_AUTOPLAY_KEY))
// What a cast on the TV does when the lesson it plays ends.
export const castPlayMode = ref(readMode(CAST_MODE_KEY, null))

// Adopt a value without talking to the server (used for what the server reports).
export function applyPlayMode(mode) {
  const next = normalizePlayMode(mode)
  playMode.value = next
  writePreference(MODE_KEY, next)
  return next
}

export function applyCastPlayMode(mode) {
  const next = normalizePlayMode(mode)
  castPlayMode.value = next
  writePreference(CAST_MODE_KEY, next)
  return next
}

// The settings the server handed out win: they are the global ones, shared by
// every browser, while the value in localStorage may be from another machine.
watch(
  () => store.playMode,
  (mode) => {
    if (mode === 'loop' || mode === 'once' || mode === 'next') applyPlayMode(mode)
  },
  { immediate: true }
)

watch(
  () => store.castPlayMode,
  (mode) => {
    if (mode === 'loop' || mode === 'once' || mode === 'next') applyCastPlayMode(mode)
  },
  { immediate: true }
)

// The browser player's choice.
export async function setPlayMode(mode) {
  const next = applyPlayMode(mode)
  try {
    const payload = await api.setPlayMode(next)
    if (payload && typeof payload.play_mode === 'string') applyPlayMode(payload.play_mode)
    return payload || { play_mode: next }
  } catch {
    /* the server is unreachable: the local choice stands until it is back */
    return { play_mode: next }
  }
}

// The cast's own choice - applied to the running cast at the same time, so
// switching 循环 / 连播 works while the TV is playing.
export async function setCastPlayMode(mode, device) {
  const next = applyCastPlayMode(mode)
  try {
    const payload = await api.setCastPlayMode(next, device)
    if (payload && typeof payload.cast_play_mode === 'string') applyCastPlayMode(payload.cast_play_mode)
    return payload || { cast_play_mode: next }
  } catch {
    /* the server is unreachable: the local choice stands until it is back */
    return { cast_play_mode: next }
  }
}
