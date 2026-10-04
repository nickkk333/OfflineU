<script setup>
// The picture is on the TV, the progress lives here: this bar follows the cast
// (the server asks the device where it is), shows how far the lesson got and
// lets the lesson be paused, skipped or stopped from the browser. When the
// device reaches the end, the server pushes the next lesson by itself.
import { computed } from 'vue'
import { api, formatTime } from '../api.js'
import { t } from '../i18n.js'
import { useToast } from '../composables/useToast.js'

const props = defineProps({
  session: { type: Object, required: true }
})

const emit = defineEmits(['refresh', 'ended'])
const toast = useToast()

const position = computed(() => Number(props.session.position || 0))
const duration = computed(() => Number(props.session.duration || 0))
const percent = computed(() => {
  if (!duration.value) return 0
  return Math.min(100, Math.max(0, (position.value / duration.value) * 100))
})
const paused = computed(() => props.session.state === 'paused')
const ended = computed(() => props.session.state === 'ended')
const hasNext = computed(() => Boolean(props.session.next_lesson_path))

async function control(action) {
  try {
    await api.castControl(props.session.udn, action)
    emit('refresh')
  } catch (cause) {
    toast.error(cause.message)
  }
}

async function stop() {
  try {
    await api.castControl(props.session.udn, 'stop')
    emit('ended')
  } catch (cause) {
    toast.error(cause.message)
  }
}
</script>

<template>
  <div class="cast-bar">
    <div class="cast-bar__head">
      <span class="cast-bar__icon">📺</span>
      <span class="cast-bar__device">{{ session.device }}</span>
      <span v-if="paused" class="cast-bar__state">{{ t('cast.paused') }}</span>
      <span v-else-if="ended" class="cast-bar__state">{{ t('cast.finished') }}</span>
      <span v-else class="cast-bar__state cast-bar__state--live">{{ t('cast.playing') }}</span>
      <span class="spacer"></span>
      <span class="cast-bar__time mono">
        {{ formatTime(position) }}<template v-if="duration"> / {{ formatTime(duration) }}</template>
        <template v-else> · {{ t('cast.unknownDuration') }}</template>
      </span>
    </div>

    <div class="cast-bar__track">
      <div class="cast-bar__fill" :class="{ 'cast-bar__fill--idle': paused || ended }" :style="{ width: percent + '%' }"></div>
    </div>

    <div class="cast-bar__meta">
      <span class="faint">
        {{ t('cast.onLesson', { title: session.lesson_title || session.lesson_path }) }}
      </span>
      <span v-if="!session.reported && !ended" class="faint">· {{ t('cast.estimated') }}</span>
      <span v-if="session.converted" class="faint">· {{ t('cast.convertedHint') }}</span>
      <span v-if="!duration && !ended" class="faint">· {{ t('cast.noDuration') }}</span>
    </div>

    <div class="cast-bar__actions">
      <button v-if="!paused" type="button" class="btn btn--sm btn--ghost" @click="control('pause')">
        {{ t('cast.pause') }}
      </button>
      <button v-else type="button" class="btn btn--sm btn--ghost" @click="control('play')">
        {{ t('cast.resume') }}
      </button>
      <button type="button" class="btn btn--sm btn--ghost" :disabled="!hasNext" @click="control('next')">
        {{ t('cast.next') }}
      </button>
      <button type="button" class="btn btn--sm" @click="stop">{{ t('cast.stop') }}</button>
      <span class="spacer"></span>
      <span v-if="hasNext" class="faint">{{ t('cast.nextUp', { title: session.next_title }) }}</span>
      <span v-else class="faint">{{ t('cast.lastLesson') }}</span>
    </div>
  </div>
</template>

<style scoped>
.cast-bar {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px 16px;
  margin-bottom: 16px;
  border-radius: var(--radius);
  border: 1px solid var(--accent);
  background: var(--accent-soft);
}

.cast-bar__head {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.cast-bar__icon {
  font-size: 1.1rem;
}

.cast-bar__device {
  font-weight: 600;
}

.cast-bar__state {
  font-size: 0.78rem;
  padding: 2px 9px;
  border-radius: 999px;
  border: 1px solid var(--border);
  color: var(--text-muted);
}

.cast-bar__state--live {
  border-color: rgba(52, 211, 153, 0.4);
  background: var(--success-soft);
  color: var(--success);
}

.cast-bar__time {
  font-size: 0.85rem;
}

.cast-bar__track {
  height: 7px;
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.12);
  overflow: hidden;
}

.cast-bar__fill {
  height: 100%;
  border-radius: 999px;
  background: var(--gradient);
  transition: width 900ms linear;
}

.cast-bar__fill--idle {
  background: var(--text-faint);
}

.cast-bar__meta {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  font-size: 0.82rem;
}

.cast-bar__actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  font-size: 0.82rem;
}
</style>
