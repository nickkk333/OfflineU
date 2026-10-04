<script setup>
import { computed } from 'vue'

const props = defineProps({
  percentage: { type: Number, default: 0 },
  completed: { type: Number, default: 0 },
  total: { type: Number, default: 0 },
  compact: { type: Boolean, default: false }
})

const width = computed(() => `${Math.min(100, Math.max(0, props.percentage || 0))}%`)
const label = computed(() => `${(props.percentage || 0).toFixed(1)}%`)
</script>

<template>
  <div class="progress-block">
    <div v-if="!compact" class="progress-block__meta">
      <span><strong>{{ completed }}</strong> / {{ total }} lessons completed</span>
      <span class="progress-block__percent">{{ label }}</span>
    </div>
    <div class="progress">
      <div class="progress__fill" :style="{ width }"></div>
    </div>
  </div>
</template>

<style scoped>
.progress-block {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.progress-block__meta {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  color: var(--text-muted);
  font-size: 0.88rem;
}

.progress-block__meta strong {
  color: var(--text);
  font-size: 1.05rem;
}

.progress-block__percent {
  font-weight: 700;
  background: var(--gradient);
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
  font-size: 1rem;
}
</style>
