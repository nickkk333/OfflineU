<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import TypeIcon from '../components/TypeIcon.vue'
import LanguageSwitch from '../components/LanguageSwitch.vue'
import { api, formatTime, lessonRoute } from '../api.js'
import { t, translateServerMessage } from '../i18n.js'
import { useToast } from '../composables/useToast.js'

const route = useRoute()
const router = useRouter()
const toast = useToast()

// Preferences that have to survive page changes (playback speed, autoplay).
const RATE_KEY = 'offlineu.playbackRate'
const AUTOPLAY_KEY = 'offlineu.autoplayNext'
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
const autoplayEnabled = ref(readPreference(AUTOPLAY_KEY, '1') !== '0')
const playbackRate = ref(parseFloat(readPreference(RATE_KEY, '1')) || 1)

const RATES = [0.5, 0.75, 1, 1.25, 1.5, 1.75, 2]
const isMedia = computed(() => Boolean(lesson.value && (lesson.value.video_file || lesson.value.audio_file)))
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
let warningShown = false

let currentPath = ''
let lastSavedSecond = 0
let advanceTimer = 0

async function load(path) {
  currentPath = path
  loading.value = true
  error.value = ''
  textContents.value = {}
  try {
    const data = await api.lesson(path, route.query.autoplay === '1')
    payload.value = data
    lesson.value = data.lesson
    resources.value = data.resources || []
    completed.value = Boolean(data.lesson.completed)
    warningShown = false
    await nextTick()
    initialisePlayer()
    loadTextResources()
  } catch (cause) {
    error.value = cause.message
  } finally {
    loading.value = false
  }
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

  if (payload.value.requested_autoplay) autoplay(media)
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
  const href = payload.value && payload.value.autoplay_href
  if (autoplayEnabled.value && href) {
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

function toggleAutoplay() {
  autoplayEnabled.value = !autoplayEnabled.value
  writePreference(AUTOPLAY_KEY, autoplayEnabled.value ? '1' : '0')
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
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown)
  window.clearTimeout(advanceTimer)
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
      <RouterLink class="btn btn--ghost" style="margin-top: 14px" to="/">
        {{ t('common.backToDashboard') }}
      </RouterLink>
    </div>

    <template v-else-if="lesson">
      <header class="lesson-view__bar">
        <RouterLink class="btn btn--ghost btn--sm" to="/">{{ t('common.backToCourse') }}</RouterLink>
        <TypeIcon :type="lesson.lesson_type" />
        <span class="spacer"></span>
        <span class="badge">{{ positionLabel }}</span>
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
          <label class="toolbar-check">
            <input type="checkbox" :checked="autoplayEnabled" @change="toggleAutoplay" />
            {{ t('lesson.autoplayNext') }}
          </label>
          <span v-if="payload.autoplay_title" class="faint">
            {{ t('lesson.upNext') }} <strong>{{ payload.autoplay_title }}</strong>
          </span>
          <span v-else class="faint">{{ t('lesson.lastLesson') }}</span>
        </div>

        <video
          v-if="lesson.video_file"
          ref="mediaEl"
          class="player"
          controls
          playsinline
          :preload="payload.requested_autoplay ? 'auto' : 'metadata'"
          :src="lesson.video_src"
          @timeupdate="onTimeUpdate"
          @pause="onPause"
          @ended="onEnded"
          @ratechange="onRateChange"
        >
          <track
            v-if="lesson.subtitle_src"
            kind="subtitles"
            :src="lesson.subtitle_src"
            srclang="en"
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
          :src="lesson.audio_src"
          @timeupdate="onTimeUpdate"
          @pause="onPause"
          @ended="onEnded"
          @ratechange="onRateChange"
        >
          <track
            v-if="lesson.subtitle_src"
            kind="subtitles"
            :src="lesson.subtitle_src"
            srclang="en"
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


