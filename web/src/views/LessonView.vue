<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import TypeIcon from '../components/TypeIcon.vue'
import LanguageSwitch from '../components/LanguageSwitch.vue'
import CastMenu from '../components/CastMenu.vue'
import CastBar from '../components/CastBar.vue'
import { api, formatTime, lessonRoute, navigateFull } from '../api.js'
import { refreshState, store } from '../store.js'
import { t, translateServerMessage } from '../i18n.js'
import { useToast } from '../composables/useToast.js'
import { useCast } from '../composables/useCast.js'
import { PLAY_MODES, playMode, setPlayMode as savePlayMode, applyPlayMode, applyCastPlayMode } from '../composables/usePlayMode.js'

const route = useRoute()
const router = useRouter()
const toast = useToast()

// Preferences that have to survive page changes (playback speed).
const RATE_KEY = 'offlineu.playbackRate'
const SAVE_INTERVAL = 15 // seconds between two automatic progress saves

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
    /* storage disabled (private mode): everything still works, just without memory */
  }
}

const loading = ref(true)
const error = ref('')
const payload = ref(null)
const lesson = ref(null)
const resources = ref([])
const textContents = ref({})
const completed = ref(false)
const mediaEl = ref(null)
const castMenu = ref(null)
const playbackRate = ref(parseFloat(readPreference(RATE_KEY, '1')) || 1)

// True when the lesson's video cannot be copied into the stream and therefore has
// to be re-encoded piece by piece - the one thing a low powered NAS cannot do
// smoothly. The device (a TV, a box, Kodi) decodes it itself, so casting is the
// better answer, and the lesson view says so.
const suggestCast = computed(() => {
  const mode = mediaStatus.value?.mode
  // Only when the lesson really goes through the transcoder. A file that is
  // served as it is (or a WebM the browser decodes itself) needs no advice,
  // even when its codec is not one HLS could copy.
  if (mode !== 'hls' && mode !== 'remux') return false
  return Boolean(mediaStatus.value?.needs_reencode) && payload.value?.dlna_enabled !== false && !activeCast.value
})

function openCastMenu() {
  castMenu.value?.openMenu()
}

// How the media of this lesson reaches the browser. A file the browser cannot
// play as it is (an .mkv, or an .mp4 that is really an MPEG-TS stream) used to
// be repackaged from start to end before the first frame appeared - on a slow
// NAS that is minutes of waiting. Now the server answers with a plan: play the
// file as it is, stream it in short pieces (HLS - one piece is converted per
// request, so playback starts right away and jumping into the middle costs one
// piece), or repackage it in the background while this page says so.
const mediaStatus = ref(null)
const mediaPreparing = ref(false)
const mediaError = ref('')
let hlsPlayer = null
let hlsRetryTimer = 0
let remuxTimer = 0

// What the *browser player* does when the lesson ends: its own global setting
// (see usePlayMode.js). The cast keeps a separate one, so this never changes
// what the TV does - even while both are playing.
function setPlayMode(mode) {
  savePlayMode(mode)
}

// The cast that is running on a TV: the browser follows it, and the server
// pushes the next lesson when this one ends.
const { cast: castSession, refreshCast } = useCast()
let castEndedHandled = false

// A cast can still be running on a TV while the browser looks at another
// course: everything below then has to ignore it, otherwise the page would
// jump back to a lesson that does not exist in the course that is open.
function castBelongsHere(data) {
  if (!data || !data.active) return false
  const coursePath = payload.value?.course?.path
  if (coursePath && data.course_path && data.course_path !== coursePath) return false
  return true
}

// Follow the TV - but only as long as the page is watching along: the page moves
// to the next lesson when the device does, and nothing happens when the reader
// went somewhere else on their own (another lesson of this course, or another
// course). Local browsing and playback never get interrupted by the cast, and
// the cast is never interrupted by them: it lives on the server.
let castLesson = ''
watch(castSession, (data) => {
  if (!data || !castBelongsHere(data) || !data.lesson_url) {
    castLesson = ''
    return
  }
  const previous = castLesson
  castLesson = data.lesson_url
  if (!previous || previous === castLesson) return // nothing moved on the TV
  // Only follow from the lesson the TV is leaving, otherwise the reader would be
  // yanked back from whatever they opened in the meantime.
  if (!isSamePath(lessonRoute(previous), route.path)) return
  const target = lessonRoute(castLesson)
  if (target) router.push(target)
})

// The router keeps percent escapes, a manually typed address may not.
function isSamePath(left, right) {
  if (left === right) return true
  try {
    return decodeURIComponent(left) === decodeURIComponent(right)
  } catch {
    return false
  }
}

watch(
  castSession,
  (data) => {
    // The TV finished the lesson: reload so the completion mark and the
    // progress counters of this page match what was stored. Only the page of
    // that very lesson is touched - anything else the reader has opened stays.
    if (!data || !data.active || data.state !== 'ended' || castEndedHandled) return
    if (!castBelongsHere(data) || !lesson.value || lesson.value.completed) return
    if (data.lesson_path !== lesson.value.rel_path) return
    castEndedHandled = true
    load(lesson.value.rel_path)
  }
)

// The cast to show on this page: only the one that plays this very lesson of
// the course that is open.
const activeCast = computed(() => {
  const data = castSession.value
  if (!data || !lesson.value || data.lesson_path !== lesson.value.rel_path) return null
  return castBelongsHere(data) ? data : null
})

// Starting a cast takes the lesson to the TV, so the browser has to stop
// playing it: two copies of the same audio help nobody, and the local position
// would drift away from the one the device reports.
watch(activeCast, (data) => {
  if (!data) return
  const media = mediaEl.value
  if (!media) return
  if (media.currentTime > 0) save(media.currentTime)
  media.pause()
})

// A cast carries its own mode (the server applies it to the device): follow it
// so the cast bar and this page show what the TV really does, without touching
// the browser player's own choice.
watch(
  () => activeCast.value?.play_mode,
  (mode) => {
    if (mode === 'loop' || mode === 'once' || mode === 'next') applyCastPlayMode(mode)
  },
  { immediate: true }
)

const RATES = [0.5, 0.75, 1, 1.25, 1.5, 1.75, 2]
const isMedia = computed(() => Boolean(lesson.value && (lesson.value.video_file || lesson.value.audio_file)))
// True while hls.js feeds the element: the <video> has no src of its own then.
const streaming = computed(() => mediaStatus.value?.mode === 'hls' && !mediaError.value)
// Nothing is loaded while the server is still repackaging, and nothing is
// loaded through src while hls.js is in charge - otherwise the element would
// fetch the raw file (and fail on it) behind the stream's back.
const videoSrc = computed(() => (mediaPreparing.value || streaming.value ? '' : lesson.value?.video_src || ''))
const audioSrc = computed(() => (mediaPreparing.value || streaming.value ? '' : lesson.value?.audio_src || ''))
const positionLabel = computed(() =>
  payload.value ? `${payload.value.position + 1} / ${payload.value.total}` : ''
)
// The lesson path as the user should see it: course root relative to the folder
// they mapped in, so container paths never show up in the player header.
const lessonLocation = computed(() => {
  const relative = lesson.value?.rel_path || lesson.value?.path || ''
  const course = payload.value?.course?.display_path || ''
  return course && relative ? `${course}/${relative}` : relative
})
// The backend writes its warnings in English; this localises the known ones.
const storageWarning = computed(() => translateServerMessage(payload.value?.storage_warning || ''))

// Guess the subtitle language from its file name so the <track> gets a truthful
// label (the server only knows it found a .srt/.vtt beside the video).
const subtitleLang = computed(() => {
  const name = (lesson.value?.subtitle_file || lesson.value?.subtitle_src || '').toLowerCase()
  if (/zh|cn|chs|chi|中文|汉语|chinese/.test(name)) return 'zh'
  if (/ja|jp|日|japanese/.test(name)) return 'ja'
  if (/ko|kr|韩|korean/.test(name)) return 'ko'
  if (/fr|french|法/.test(name)) return 'fr'
  if (/de|german|德/.test(name)) return 'de'
  if (/en|eng|英|english/.test(name)) return 'en'
  return 'zh' // most course material here is Chinese; browsers still show it either way
})
let warningShown = false

let currentPath = ''
let lastSavedSecond = 0
let advanceTimer = 0
let savingEnabled = false

async function load(path) {
  currentPath = path
  loading.value = true
  error.value = ''
  textContents.value = {}
  castEndedHandled = false
  // While another lesson (or another course) is being loaded, the player of
  // the previous one still fires pause/timeupdate: those must not be saved
  // under the new lesson's name.
  savingEnabled = false
  try {
    const data = await api.lesson(path, route.query.autoplay === '1')
    payload.value = data
    lesson.value = data.lesson
    resources.value = data.resources || []
    completed.value = Boolean(data.lesson.completed)
    warningShown = false
    savingEnabled = true
    // The media element lives in the v-else-if branch that is hidden while
    // loading is true, so it is not in the DOM until here: flip loading off and
    // let Vue render the player before we ever touch mediaEl, otherwise the
    // resume position and - for autoplay - media.play() are skipped.
    loading.value = false
    await nextTick()
    await prepareMedia()
    initialisePlayer()
    loadTextResources()
  } catch (cause) {
    error.value = cause.message
  } finally {
    loading.value = false
  }
}

// Asks the server how this lesson has to be played and sets the player up
// accordingly. A failure is never fatal: the file is then played the old way.
async function prepareMedia() {
  destroyHls()
  stopRemuxPoll()
  hlsAttempts = 0
  mediaStatus.value = null
  mediaPreparing.value = false
  mediaError.value = ''
  if (!isMedia.value || !lesson.value) return
  const path = lesson.value.rel_path
  let status
  try {
    status = await api.mediaStatus(path)
  } catch {
    return // no answer: play the file as it is
  }
  mediaStatus.value = status
  if (status.mode === 'hls' && status.hls_url) {
    if (status.hls_ready) {
      await attachHls(status.hls_url)
      return
    }
    // Reading the keyframes of a long lesson takes minutes on a slow machine.
    // Say so and wait for it - handing the playlist to hls.js too early makes
    // it fail and fall back to the raw file, which a browser cannot decode.
    mediaPreparing.value = true
    startHlsPoll(path, status.hls_url)
    return
  }
  if (status.mode === 'remux') {
    if (status.error) {
      mediaError.value = status.error
      return
    }
    if (!status.ready) {
      // The server is repackaging the whole file; say so instead of letting
      // the request hang until it is done.
      mediaPreparing.value = true
      startRemuxPoll(path)
    }
  }
}

// Waits for the segment plan of a lesson that is streamed as HLS. The server
// reads the keyframes once, which costs a demux of the whole file; everything
// after that is cached, so this wait only ever happens for the first play.
function startHlsPoll(path, url) {
  let attempts = 0
  const tick = async () => {
    if (currentPath !== path) return // the reader moved on
    try {
      const status = await api.mediaStatus(path)
      mediaStatus.value = status
      if (status.hls_ready && status.hls_url) {
        stopRemuxPoll()
        mediaPreparing.value = false
        await nextTick()
        await attachHls(status.hls_url)
        initialisePlayer()
        return
      }
      if (status.mode !== 'hls') {
        // The server gave up on streaming it: fall back to the other modes.
        stopRemuxPoll()
        mediaPreparing.value = false
        await playWithoutHls()
        return
      }
      if (++attempts > 600) {
        stopRemuxPoll()
        mediaPreparing.value = false
        mediaError.value = t('lesson.preparingSlow')
        return
      }
    } catch {
      /* the server is busy reading the file; keep waiting */
    }
    remuxTimer = window.setTimeout(tick, 2000)
  }
  remuxTimer = window.setTimeout(tick, 2000)
}

// Polls until the repackaged copy is there. The conversion of a long lesson can
// take minutes on a slow machine, and it runs once - the result is cached.
function startRemuxPoll(path) {
  let attempts = 0
  const tick = async () => {
    if (currentPath !== path) return // the reader moved on
    try {
      const status = await api.mediaStatus(path)
      mediaStatus.value = status
      if (status.ready || status.error) {
        stopRemuxPoll()
        mediaPreparing.value = false
        if (status.error) {
          mediaError.value = status.error
          return
        }
        await nextTick()
        initialisePlayer()
        return
      }
    } catch {
      /* the server is busy converting; keep waiting */
    }
    if (++attempts > 300) {
      stopRemuxPoll()
      mediaPreparing.value = false
      mediaError.value = t('lesson.preparingSlow')
      return
    }
    remuxTimer = window.setTimeout(tick, 2000)
  }
  remuxTimer = window.setTimeout(tick, 2000)
}

function stopRemuxPoll() {
  if (remuxTimer) {
    window.clearTimeout(remuxTimer)
    remuxTimer = 0
  }
}

// The first play of a long lesson has to wait for its segment plan (the server
// reads the keyframes once, then caches them), so the manifest is given time and
// a handful of retries before giving up.
const HLS_RETRIES = 20
const HLS_RETRY_DELAY = 5000
let hlsAttempts = 0

function stopHlsRetry() {
  if (hlsRetryTimer) {
    window.clearTimeout(hlsRetryTimer)
    hlsRetryTimer = 0
  }
}

async function attachHls(url) {
  const media = mediaEl.value
  if (!media) return
  // Safari and iOS play HLS without help.
  if (media.canPlayType('application/vnd.apple.mpegurl')) {
    media.src = url
    return
  }
  try {
    const { default: Hls } = await import('hls.js')
    if (!Hls.isSupported()) {
      await playWithoutHls()
      return
    }
    const player = new Hls({
      enableWorker: true,
      maxBufferLength: 30,
      maxMaxBufferLength: 60,
      manifestLoadTimeOut: 120000
    })
    player.loadSource(url)
    player.attachMedia(media)
    player.on(Hls.Events.ERROR, (_event, data) => {
      if (!data || !data.fatal) return
      destroyHls()
      if (data.type === Hls.ErrorTypes.NETWORK_ERROR && hlsAttempts < HLS_RETRIES) {
        // 503 while the segment plan is still being built: try again shortly.
        hlsAttempts += 1
        hlsRetryTimer = window.setTimeout(() => attachHls(url), HLS_RETRY_DELAY)
        return
      }
      toast.error(t('toast.streamFailed'))
      playWithoutHls()
    })
    hlsPlayer = player
  } catch {
    await playWithoutHls()
  }
}

// HLS did not work out. Asking the server for a plan without HLS makes it
// repackage the whole lesson instead - slow, but it plays. Falling back to the
// plain file would hand the browser an .mkv it cannot decode at all.
async function playWithoutHls() {
  const path = lesson.value?.rel_path
  if (!path) {
    mediaStatus.value = { mode: 'direct' }
    return
  }
  try {
    const status = await api.mediaStatus(path, false)
    mediaStatus.value = status
    mediaError.value = status.error || ''
    if (status.mode === 'remux' && !status.error && !status.ready) {
      mediaPreparing.value = true
      startRemuxPoll(path)
    }
  } catch {
    mediaStatus.value = { mode: 'direct' }
  }
}

function destroyHls() {
  stopHlsRetry()
  if (!hlsPlayer) return
  try {
    hlsPlayer.destroy()
  } catch {
    /* already gone */
  }
  hlsPlayer = null
}

function initialisePlayer() {
  const media = mediaEl.value
  if (!media) return

  lastSavedSecond = 0
  media.playbackRate = playbackRate.value

  const resumeAt = Number(lesson.value.progress_seconds || 0)
  const applyResume = () => {
    if (resumeAt > 0 && resumeAt < media.duration - 1) {
      media.currentTime = resumeAt
    }
  }
  if (media.readyState >= 1) applyResume()
  else media.addEventListener('loadedmetadata', applyResume, { once: true })

  // ?autoplay=1 (the resume card, or the jump from the previous lesson) must not
  // start the local player when a cast is running - the TV is what plays now.
  // The cast session is checked too, not only the one matching this lesson:
  // right after "cast" the poll may not have named the lesson yet.
  const casting = Boolean(
    activeCast.value || (castSession.value && castSession.value.active && castBelongsHere(castSession.value))
  )
  if (payload.value.requested_autoplay && !casting) autoplay(media)
}

function autoplay(media) {
  const attempt = media.play()
  if (attempt && typeof attempt.catch === 'function') {
    attempt.catch(() => {
      toast.error(t('toast.autoplayBlocked'))
    })
  }
}

async function loadTextResources() {
  for (const resource of resources.value) {
    if (resource.mode !== 'text') continue
    try {
      const response = await fetch(resource.src)
      if (!response.ok) throw new Error(`HTTP ${response.status}`)
      textContents.value[resource.url] = await response.text()
    } catch (cause) {
      textContents.value[resource.url] = t('toast.couldNotLoadFile', { message: cause.message })
    }
  }
}

async function save(seconds, completedFlag) {
  if (!savingEnabled || !lesson.value) return
  try {
    await api.saveProgress(lesson.value.rel_path, { seconds, completed: completedFlag })
  } catch (cause) {
    if (!warningShown) {
      warningShown = true
      toast.error(cause.message)
    }
  }
}

async function markCompleted() {
  if (completed.value) return
  completed.value = true
  toast.success(t('toast.markedCompleted'))
  const media = mediaEl.value
  await save(media ? media.currentTime : 0, true)
}

function onTimeUpdate() {
  const media = mediaEl.value
  if (!media) return
  if (media.currentTime - lastSavedSecond >= SAVE_INTERVAL) {
    lastSavedSecond = media.currentTime
    save(media.currentTime)
  }
}

function onPause() {
  const media = mediaEl.value
  if (media && media.currentTime > 0) save(media.currentTime)
}

function onEnded() {
  const media = mediaEl.value
  save(media ? media.currentTime : 0, true)
  completed.value = true
  if (playMode.value === 'loop') {
    // Same lesson from the top. Seconds never move backwards in the store and
    // the completion flag was just written, so a repeat cannot lose progress.
    if (media) {
      media.currentTime = 0
      autoplay(media)
    }
    return
  }
  if (playMode.value !== 'next') return
  const href = payload.value && payload.value.autoplay_href
  if (href) {
    // ~1 s so the completion toast is readable before the next lesson loads
    advanceTimer = window.setTimeout(() => router.push(href), 900)
  }
}

function onRateChange() {
  const media = mediaEl.value
  if (!media) return
  playbackRate.value = media.playbackRate
  writePreference(RATE_KEY, String(media.playbackRate))
}

function applyRate(rate) {
  playbackRate.value = rate
  writePreference(RATE_KEY, String(rate))
  const media = mediaEl.value
  if (media) media.playbackRate = rate
}

function onKeydown(event) {
  const media = mediaEl.value
  if (!media) return
  const target = event.target
  if (target && ['INPUT', 'SELECT', 'TEXTAREA'].includes(target.tagName)) return

  switch (event.key) {
    case ' ':
      event.preventDefault()
      if (media.paused) media.play().catch(() => {})
      else media.pause()
      break
    case 'ArrowRight':
      event.preventDefault()
      media.currentTime = Math.min(media.duration || Infinity, media.currentTime + 10)
      toast.info(t('toast.skipForward'))
      break
    case 'ArrowLeft':
      event.preventDefault()
      media.currentTime = Math.max(0, media.currentTime - 10)
      toast.info(t('toast.skipBack'))
      break
    case 'ArrowUp':
      event.preventDefault()
      media.volume = Math.min(1, media.volume + 0.1)
      toast.info(t('toast.volume', { percent: Math.round(media.volume * 100) }))
      break
    case 'ArrowDown':
      event.preventDefault()
      media.volume = Math.max(0, media.volume - 0.1)
      toast.info(t('toast.volume', { percent: Math.round(media.volume * 100) }))
      break
    default:
  }
}

watch(
  () => route.params.lessonPath,
  (value) => {
    const next = Array.isArray(value) ? value.join('/') : value || ''
    if (next && next !== currentPath) load(next)
  }
)

onMounted(() => {
  const initial = route.params.lessonPath
  load(Array.isArray(initial) ? initial.join('/') : initial || '')
  window.addEventListener('keydown', onKeydown)
  // The lesson page is often opened directly (deep link, fresh browser, cleared
  // cache). The play modes are global server settings handed out by /api/state,
  // and only the home view asks for them - without this the toolbar and the
  // cast menu would silently fall back to localStorage here.
  if (!store.loaded) refreshState()
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown)
  window.clearTimeout(advanceTimer)
  stopRemuxPoll()
  destroyHls()
})
</script>

<template>
  <div class="shell lesson-view">
    <div v-if="loading" class="card empty-state">
      <div class="empty-state__icon"><span class="spinner"></span></div>
      {{ t('lesson.loading') }}
    </div>

    <div v-else-if="error" class="card empty-state">
      <div class="empty-state__icon">⚠️</div>
      <p>{{ error }}</p>
      <button type="button" class="btn btn--ghost" style="margin-top: 14px" @click="navigateFull('/')">
        {{ t('common.backToDashboard') }}
      </button>
    </div>

    <template v-else-if="lesson">
      <header class="lesson-view__bar">
        <button type="button" class="btn btn--ghost btn--sm" @click="navigateFull('/')">
          {{ t('common.backToCourse') }}
        </button>
        <TypeIcon :type="lesson.lesson_type" />
        <span class="spacer"></span>
        <span class="badge">{{ positionLabel }}</span>
        <!-- 投屏按钮只出现在播放页，紧挨中英文切换 -->
        <CastMenu
          v-if="isMedia"
          ref="castMenu"
          :lesson="lesson"
          :enabled="payload.dlna_enabled !== false"
          :plan="payload.cast_plan || {}"
          @casted="refreshCast"
        />
        <LanguageSwitch />
        <button
          type="button"
          class="btn btn--sm"
          :class="{ 'btn--ghost': completed }"
          :disabled="completed"
          @click="markCompleted"
        >
          {{ completed ? t('lesson.completed') : t('lesson.markCompleted') }}
        </button>
      </header>

      <section class="card lesson-view__head">
        <h1>{{ lesson.title }}</h1>
        <div class="lesson-view__meta">
          <span class="mono faint">{{ lessonLocation }}</span>
          <span v-if="lesson.progress_seconds > 0" class="badge">
            {{ completed ? t('lesson.watched') : t('lesson.resumeAt') }} {{ formatTime(lesson.progress_seconds) }}
          </span>
        </div>
      </section>

      <div v-if="payload.storage_warning" class="banner banner--danger">
        <strong>{{ t('lesson.progressWarning') }}</strong>
        <span>{{ storageWarning }}</span>
      </div>

      <section v-if="isMedia" class="card lesson-view__player">
        <CastBar
          v-if="activeCast"
          :session="activeCast"
          @refresh="refreshCast"
          @ended="refreshCast"
        />
        <div class="player-toolbar">
          <label class="toolbar-field">
            <span class="faint">{{ t('lesson.speed') }}</span>
            <select
              class="input input--select"
              :value="playbackRate"
              @change="applyRate(parseFloat($event.target.value))"
            >
              <option v-for="rate in RATES" :key="rate" :value="rate">{{ rate }}×</option>
            </select>
          </label>
          <div class="toolbar-field">
            <span class="faint">{{ t('lesson.whenFinished') }}</span>
            <div class="seg" role="radiogroup" :aria-label="t('lesson.whenFinished')">
              <button
                v-for="mode in PLAY_MODES"
                :key="mode.id"
                type="button"
                class="seg__btn"
                :class="{ 'seg__btn--active': playMode === mode.id }"
                role="radio"
                :aria-checked="playMode === mode.id"
                :title="t(mode.title)"
                @click="setPlayMode(mode.id)"
              >
                {{ t(mode.label) }}
              </button>
            </div>
          </div>
          <span v-if="playMode === 'loop'" class="faint">{{ t('lesson.loopHint') }}</span>
          <span v-else-if="playMode === 'next' && payload.autoplay_title" class="faint">
            {{ t('lesson.upNext') }} <strong>{{ payload.autoplay_title }}</strong>
          </span>
          <span v-else-if="playMode === 'next'" class="faint">{{ t('lesson.lastLesson') }}</span>
        </div>

        <div v-if="suggestCast" class="banner banner--tip">
          <strong>{{ t('lesson.suggestCastTitle') }}</strong>
          <span>{{ t('lesson.suggestCastBody', { codec: mediaStatus.video_codec || '' }) }}</span>
          <button type="button" class="btn btn--sm" @click="openCastMenu">
            {{ t('lesson.suggestCastAction') }}
          </button>
        </div>

        <div v-if="mediaPreparing" class="player-note">
          <span class="spinner"></span>
          {{ t('lesson.preparing') }}
        </div>
        <p v-else-if="mediaError" class="player-note player-note--error">{{ mediaError }}</p>

        <video
          v-if="lesson.video_file"
          ref="mediaEl"
          class="player"
          controls
          playsinline
          :preload="payload.requested_autoplay ? 'auto' : 'metadata'"
          :src="videoSrc"
          @timeupdate="onTimeUpdate"
          @pause="onPause"
          @ended="onEnded"
          @ratechange="onRateChange"
        >
          <track
            v-if="lesson.subtitle_src"
            kind="subtitles"
            :src="lesson.subtitle_src"
            :srclang="subtitleLang"
            :label="t('lesson.subtitles')"
            default
          />
        </video>

        <audio
          v-else
          ref="mediaEl"
          class="player player--audio"
          controls
          :preload="payload.requested_autoplay ? 'auto' : 'metadata'"
          :src="audioSrc"
          @timeupdate="onTimeUpdate"
          @pause="onPause"
          @ended="onEnded"
          @ratechange="onRateChange"
        >
          <track
            v-if="lesson.subtitle_src"
            kind="subtitles"
            :src="lesson.subtitle_src"
            :srclang="subtitleLang"
            :label="t('lesson.subtitles')"
            default
          />
        </audio>
      </section>

      <section v-if="resources.length" class="card stack">
        <div class="section-title">{{ t('lesson.contentTitle') }}</div>
        <div v-for="resource in resources" :key="resource.url" class="resource">
          <h4 class="resource__name">{{ resource.name }}</h4>
          <iframe
            v-if="resource.mode === 'iframe'"
            class="resource__frame"
            :src="resource.src"
            :title="resource.name"
          ></iframe>
          <pre v-else-if="resource.mode === 'text'" class="resource__text">{{ textContents[resource.url] ?? t('lesson.loadingResource') }}</pre>
          <p v-else class="faint">{{ t('lesson.cannotPreview') }}</p>
          <a class="resource__link" :href="resource.src" target="_blank" rel="noopener">
            {{ t('lesson.openInNewTab', { name: resource.name }) }}
          </a>
        </div>
      </section>

      <footer class="lesson-view__nav">
        <RouterLink v-if="payload.prev_url" class="btn btn--ghost" :to="lessonRoute(payload.prev_url)">
          {{ t('common.previous') }}
        </RouterLink>
        <button v-else type="button" class="btn btn--ghost" disabled>{{ t('common.previous') }}</button>
        <span class="spacer"></span>
        <RouterLink v-if="payload.next_url" class="btn" :to="lessonRoute(payload.next_url)">
          {{ t('common.next') }}
        </RouterLink>
        <button v-else type="button" class="btn" disabled>{{ t('common.next') }}</button>
      </footer>

      <p class="faint shortcuts">{{ t('lesson.shortcuts') }}</p>
    </template>
  </div>
</template>

<style scoped>
.lesson-view {
  padding-top: 26px;
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.lesson-view__bar {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.lesson-view__head h1 {
  font-size: 1.5rem;
  font-weight: 700;
  letter-spacing: -0.02em;
  margin-bottom: 6px;
}

.lesson-view__meta {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.banner {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 14px 18px;
  border-radius: var(--radius);
  font-size: 0.9rem;
}

.banner--danger {
  background: var(--danger-soft);
  border: 1px solid rgba(248, 113, 113, 0.35);
  color: #fecaca;
}

/* "This lesson has to be re-encoded - cast it instead." */
.banner--tip {
  background: rgba(56, 189, 248, 0.12);
  border: 1px solid rgba(56, 189, 248, 0.35);
  color: #bae6fd;
}

.banner--tip .btn {
  align-self: flex-start;
  margin-top: 4px;
}

.player-toolbar {
  display: flex;
  align-items: center;
  gap: 18px;
  flex-wrap: wrap;
  padding-bottom: 16px;
  margin-bottom: 16px;
  border-bottom: 1px solid var(--border);
}

.toolbar-field {
  display: flex;
  align-items: center;
  gap: 8px;
}

.input--select {
  width: auto;
  padding: 7px 12px;
  cursor: pointer;
}

.toolbar-check {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  user-select: none;
  font-size: 0.9rem;
}

/* Segmented control for the play mode (loop / once / next) --------------- */
.seg {
  display: inline-flex;
  border: 1px solid var(--border);
  border-radius: 10px;
  overflow: hidden;
  background: var(--surface-strong);
}

.seg__btn {
  padding: 6px 12px;
  background: none;
  border: none;
  border-right: 1px solid var(--border);
  color: var(--text-muted);
  font-size: 0.85rem;
  cursor: pointer;
  transition: all var(--transition);
}

.seg__btn:last-child {
  border-right: none;
}

.seg__btn:hover {
  color: var(--text);
  background: rgba(255, 255, 255, 0.06);
}

.seg__btn--active {
  background: var(--accent-soft);
  color: var(--text);
  font-weight: 600;
}

.toolbar-check input {
  width: 16px;
  height: 16px;
  accent-color: var(--accent);
  cursor: pointer;
}

.player {
  display: block;
  width: 100%;
  max-width: 960px;
  margin: 0 auto;
  border-radius: var(--radius);
  background: #000;
}

.player--audio {
  background: rgba(8, 12, 22, 0.6);
  padding: 12px;
}

/* Shown instead of the player while the server converts the lesson. */
.player-note {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  min-height: 120px;
  padding: 24px;
  border: 1px dashed var(--border);
  border-radius: var(--radius);
  color: var(--text-muted, #8b95a8);
  text-align: center;
}

.player-note--error {
  color: var(--danger, #e2555f);
}

.resource {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.resource__name {
  font-weight: 600;
  color: var(--accent-3);
}

.resource__frame {
  width: 100%;
  height: min(70vh, 620px);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: #fff;
}

.resource__text {
  max-height: 620px;
  overflow: auto;
  padding: 18px;
  border-radius: var(--radius);
  border: 1px solid var(--border);
  background: rgba(8, 12, 22, 0.6);
  font-family: var(--mono);
  font-size: 0.87rem;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.resource__link {
  align-self: flex-start;
  padding: 9px 16px;
  border-radius: 11px;
  border: 1px solid var(--border);
  background: var(--surface-strong);
  font-size: 0.88rem;
  transition: all var(--transition);
}

.resource__link:hover {
  border-color: var(--border-strong);
  background: rgba(255, 255, 255, 0.12);
}

.lesson-view__nav {
  display: flex;
  align-items: center;
  gap: 12px;
  padding-top: 4px;
}

.shortcuts {
  text-align: center;
}
</style>


