<script setup>
// Pushes the media of the current lesson to a DLNA/UPnP renderer (TV, speaker,
// Kodi, …). The Go backend does the SSDP search and the SOAP calls - the SPA
// only picks a device and asks for it.
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api, formatTime, reloadPage } from '../api.js'
import { t } from '../i18n.js'
import { useToast } from '../composables/useToast.js'
import { castPlayMode } from '../composables/usePlayMode.js'

const props = defineProps({
  lesson: { type: Object, required: true },
  enabled: { type: Boolean, default: true },
  // What the server decided for this file: { needs_transcode, transcode_available }
  plan: { type: Object, default: () => ({}) }
})

const emit = defineEmits(['casted'])

const DEVICE_KEY = 'offlineu.castDevice'

const toast = useToast()
const open = ref(false)
const loading = ref(false)
const busy = ref(false)
const devices = ref([])
const error = ref('')
const activeUDN = ref(readStoredDevice())
const activeName = ref('')

function readStoredDevice() {
  try {
    return window.localStorage.getItem(DEVICE_KEY) || ''
  } catch {
    return ''
  }
}

function storeDevice(udn) {
  try {
    if (udn) window.localStorage.setItem(DEVICE_KEY, udn)
    else window.localStorage.removeItem(DEVICE_KEY)
  } catch {
    /* storage disabled: the device is simply forgotten after a reload */
  }
}

const resumeAt = computed(() => Number(props.lesson?.progress_seconds || 0))

// Compatibility mode: repackage (or re-encode) the file while it plays. It is
// pre-set to what the server worked out for this lesson and can be overridden.
const compat = ref(Boolean(props.plan?.needs_transcode))
const canConvert = computed(() => Boolean(props.plan?.transcode_available))
const needsConversion = computed(() => Boolean(props.plan?.needs_transcode))
const showConvertWarning = computed(() => needsConversion.value && !canConvert.value)

watch(
  () => props.plan,
  (value) => {
    compat.value = Boolean(value?.needs_transcode)
  },
  { deep: true }
)

async function loadDevices(refresh) {
  loading.value = true
  error.value = ''
  try {
    const payload = await api.castDevices(refresh)
    devices.value = payload.devices || []
    if (!devices.value.length) error.value = ''
  } catch (cause) {
    devices.value = []
    error.value = cause.message
  } finally {
    loading.value = false
  }
}

function toggle() {
  open.value = !open.value
  if (open.value && !devices.value.length) loadDevices(false)
}

// Lets the lesson view open the menu on its own - it suggests casting when a
// lesson would have to be re-encoded for the browser.
function openMenu() {
  open.value = true
  if (!devices.value.length) loadDevices(false)
}

defineExpose({ openMenu })

// The browser can read the length of most files while the server may not have
// ffmpeg to do it - hand it over so the cast knows when the lesson ended.
function measureDuration() {
  const source = props.lesson?.video_src || props.lesson?.audio_src
  if (!source || typeof window === 'undefined') return Promise.resolve(0)
  return new Promise((resolve) => {
    const probe = document.createElement(props.lesson?.video_src ? 'video' : 'audio')
    let settled = false
    const finish = (value) => {
      if (settled) return
      settled = true
      window.clearTimeout(timer)
      probe.removeAttribute('src')
      probe.load?.()
      resolve(Number.isFinite(value) && value > 0 ? value : 0)
    }
    const timer = window.setTimeout(() => finish(probe.duration), 1500)
    probe.preload = 'metadata'
    probe.muted = true
    probe.addEventListener('loadedmetadata', () => finish(probe.duration))
    probe.addEventListener('error', () => finish(0))
    probe.src = source
  })
}

async function cast(device) {
  busy.value = true
  try {
    const duration = await measureDuration()
    const result = await api.castTo(
      device.udn,
      props.lesson.rel_path,
      resumeAt.value,
      compat.value,
      // The cast keeps its own play mode (single-loop / single / continuous) -
      // independent of the browser player's, and remembered on the server - so
      // starting a cast must not pick up the browser's local choice.
      castPlayMode.value,
      duration
    )
    activeUDN.value = device.udn
    activeName.value = result.device || device.name
    storeDevice(device.udn)
    if (result.converted) {
      toast.success(t('toast.castConverted', { title: props.lesson.title, device: activeName.value }))
    } else {
      toast.success(t('toast.castStarted', { title: props.lesson.title, device: activeName.value }))
    }
    emit('casted')
    // The lesson now plays on the TV: drop the in-browser player and reload so
    // the page reflects the cast session cleanly (browser playback stops).
    reloadPage()
  } catch (cause) {
    toast.error(cause.message)
  } finally {
    busy.value = false
  }
}

async function control(action) {
  if (!activeUDN.value) return
  busy.value = true
  try {
    await api.castControl(activeUDN.value, action)
    if (action === 'stop') {
      toast.info(t('toast.castStopped', { device: activeName.value }))
      activeUDN.value = ''
      activeName.value = ''
      storeDevice('')
      emit('casted')
    }
  } catch (cause) {
    toast.error(cause.message)
  } finally {
    busy.value = false
  }
}

function onDocumentClick(event) {
  if (!open.value) return
  if (!event.target.closest || !event.target.closest('.cast')) open.value = false
}

if (typeof document !== 'undefined') {
  document.addEventListener('click', onDocumentClick)
}
onBeforeUnmount(() => {
  if (typeof document !== 'undefined') document.removeEventListener('click', onDocumentClick)
})
</script>

<template>
  <div v-if="enabled" class="cast">
    <button type="button" class="btn btn--sm btn--ghost" @click="toggle">
      {{ t('cast.button') }}
    </button>

    <div v-if="open" class="cast__panel">
      <div class="cast__head">
        <strong>{{ t('cast.title') }}</strong>
        <button
          type="button"
          class="cast__refresh"
          :disabled="loading"
          @click="loadDevices(true)"
        >
          {{ t('cast.refresh') }}
        </button>
      </div>

      <p v-if="error" class="cast__error">{{ error }}</p>
      <p v-else-if="loading" class="cast__hint">
        <span class="spinner"></span> {{ t('cast.scanning') }}
      </p>
      <p v-else-if="!devices.length" class="cast__hint">{{ t('cast.empty') }}</p>

      <ul v-else class="cast__list">
        <li v-for="device in devices" :key="device.udn">
          <button
            type="button"
            class="cast__device"
            :class="{ 'cast__device--active': device.udn === activeUDN }"
            :disabled="busy"
            @click="cast(device)"
          >
            <span class="cast__icon">📺</span>
            <span class="cast__names">
              <span class="cast__name">{{ device.name || device.address }}</span>
              <span class="cast__meta faint">
                {{ [device.manufacturer, device.model].filter(Boolean).join(' · ') || device.address }}
              </span>
            </span>
          </button>
        </li>
      </ul>

      <label v-if="canConvert" class="cast__compat">
        <input type="checkbox" :checked="compat" :disabled="busy" @change="compat = $event.target.checked" />
        <span>{{ t('cast.compatMode') }}</span>
      </label>
      <p v-if="canConvert" class="cast__hint faint">{{ t('cast.compatHint') }}</p>
      <p v-if="showConvertWarning" class="cast__warn">{{ t('cast.noFfmpeg') }}</p>

      <p v-if="devices.length && resumeAt > 0" class="cast__hint faint">
        {{ t('cast.resumeFrom', { time: formatTime(resumeAt) }) }}
      </p>

      <p v-if="devices.length && lesson.subtitle_src" class="cast__hint faint">
        📝 {{ t('cast.subtitleSent') }}
      </p>

      <div v-if="activeUDN" class="cast__active">
        <span class="cast__playing">{{ t('cast.casting', { name: activeName }) }}</span>
        <span class="spacer"></span>
        <button type="button" class="btn btn--sm btn--ghost" :disabled="busy" @click="control('pause')">
          {{ t('cast.pause') }}
        </button>
        <button type="button" class="btn btn--sm btn--ghost" :disabled="busy" @click="control('play')">
          {{ t('cast.resume') }}
        </button>
        <button type="button" class="btn btn--sm" :disabled="busy" @click="control('stop')">
          {{ t('cast.stop') }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.cast {
  position: relative;
}

.cast__panel {
  position: absolute;
  right: 0;
  top: calc(100% + 10px);
  /* 置顶：比页面里任何卡片、工具条和置顶的投屏条都高 */
  z-index: 999;
  width: min(340px, 78vw);
  padding: 16px;
  border-radius: var(--radius);
  border: 1px solid var(--border-strong);
  background: var(--surface-solid);
  box-shadow: var(--shadow);
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.cast__head {
  display: flex;
  align-items: center;
  gap: 10px;
  justify-content: space-between;
}

.cast__refresh {
  border: none;
  background: none;
  color: var(--accent-3);
  font-size: 0.82rem;
  cursor: pointer;
  padding: 2px 4px;
}

.cast__refresh:disabled {
  opacity: 0.5;
  cursor: progress;
}

.cast__list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-height: 260px;
  overflow: auto;
}

.cast__device {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 11px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--text);
  cursor: pointer;
  text-align: left;
  transition: all var(--transition);
}

.cast__device:hover:not(:disabled) {
  border-color: var(--border-strong);
  background: rgba(255, 255, 255, 0.11);
}

.cast__device--active {
  border-color: var(--accent);
  background: var(--accent-soft);
}

.cast__device:disabled {
  opacity: 0.6;
  cursor: progress;
}

.cast__icon {
  font-size: 1.05rem;
}

.cast__names {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.cast__name {
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cast__meta {
  font-size: 0.76rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cast__hint {
  font-size: 0.82rem;
  color: var(--text-muted);
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0;
  line-height: 1.5;
}

.cast__error {
  margin: 0;
  font-size: 0.82rem;
  color: var(--danger);
  line-height: 1.5;
}

.cast__warn {
  margin: 0;
  padding: 9px 11px;
  border-radius: var(--radius-sm);
  border: 1px solid rgba(251, 191, 36, 0.35);
  background: rgba(251, 191, 36, 0.12);
  font-size: 0.82rem;
  line-height: 1.5;
}

.cast__compat {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  user-select: none;
  font-size: 0.87rem;
}

.cast__compat input {
  width: 16px;
  height: 16px;
  accent-color: var(--accent);
  cursor: pointer;
}

.cast__active {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  padding-top: 10px;
  border-top: 1px solid var(--border);
}

.cast__playing {
  font-size: 0.84rem;
  font-weight: 600;
  color: var(--accent-3);
}
</style>
