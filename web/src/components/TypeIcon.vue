<script setup>
import { computed } from 'vue'
import { t } from '../i18n.js'

const props = defineProps({
  type: { type: String, default: 'text' },
  withLabel: { type: Boolean, default: true }
})

const ICONS = { video: '▶', audio: '♫', quiz: '✎', text: '▤' }

const icon = computed(() => ICONS[props.type] || ICONS.text)
const label = computed(() => t(ICONS[props.type] ? `types.${props.type}` : 'types.text'))
</script>

<template>
  <span class="type" :class="`type--${type}`">
    <span class="type__icon" aria-hidden="true">{{ icon }}</span>
    <span v-if="withLabel" class="type__label">{{ label }}</span>
  </span>
</template>

<style scoped>
.type {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  font-size: 0.88rem;
  font-weight: 600;
}

.type__icon {
  display: grid;
  place-items: center;
  width: 26px;
  height: 26px;
  flex: none;
  border-radius: 8px;
  font-size: 0.78rem;
  background: var(--surface-strong);
  border: 1px solid var(--border);
}

.type--video .type__icon {
  background: rgba(248, 113, 113, 0.16);
  border-color: rgba(248, 113, 113, 0.3);
  color: #fca5a5;
}

.type--audio .type__icon {
  background: rgba(167, 139, 250, 0.16);
  border-color: rgba(167, 139, 250, 0.32);
  color: #c4b5fd;
}

.type--quiz .type__icon {
  background: rgba(251, 191, 36, 0.16);
  border-color: rgba(251, 191, 36, 0.32);
  color: #fcd34d;
}
</style>
