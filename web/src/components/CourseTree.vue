<script setup>
import { computed, ref, watch } from 'vue'
import TypeIcon from './TypeIcon.vue'
import { lessonRoute, formatTime } from '../api.js'

const props = defineProps({
  node: { type: Object, required: true },
  depth: { type: Number, default: 0 },
  filter: { type: String, default: '' }
})

const open = ref(props.depth < 1)
const query = computed(() => props.filter.trim().toLowerCase())

function matches(lesson) {
  if (!query.value) return true
  return (
    lesson.title.toLowerCase().includes(query.value) ||
    lesson.rel_path.toLowerCase().includes(query.value)
  )
}

const lessons = computed(() => (props.node.lessons || []).filter(matches))

function hasVisibleContent(node) {
  if ((node.lessons || []).some(matches)) return true
  return (node.children || []).some(hasVisibleContent)
}

const visibleChildren = computed(() => (props.node.children || []).filter(hasVisibleContent))
const visible = computed(() => (props.node.lessons || []).some(matches) || visibleChildren.value.length > 0)
const itemCount = computed(() => (props.node.children || []).length + (props.node.lessons || []).length)

watch(query, (value) => {
  if (value && visible.value) open.value = true
})

const stats = computed(() => props.node.stats || { completed_lessons: 0, total_lessons: 0, completion_percentage: 0 })
</script>

<template>
  <div v-if="visible" class="node">
    <button
      type="button"
      class="node__header"
      :class="{ 'node__header--leaf': itemCount === 0 }"
      :disabled="itemCount === 0"
      @click="open = !open"
    >
      <span class="node__chevron" :class="{ 'node__chevron--open': open }" aria-hidden="true">▸</span>
      <span class="node__folder" aria-hidden="true">📁</span>
      <span class="node__name">{{ node.name }}</span>
      <span class="faint">{{ itemCount }} items</span>
      <span class="spacer"></span>
      <span v-if="stats.total_lessons" class="node__stats">
        <span class="node__stats-bar">
          <span class="node__stats-fill" :style="{ width: `${stats.completion_percentage}%` }"></span>
        </span>
        <span class="faint">{{ stats.completed_lessons }}/{{ stats.total_lessons }}</span>
      </span>
    </button>

    <Transition name="tree">
      <div v-show="open" class="node__body">
        <CourseTree
          v-for="child in visibleChildren"
          :key="child.path"
          :node="child"
          :depth="depth + 1"
          :filter="filter"
        />

        <RouterLink
          v-for="lesson in lessons"
          :key="lesson.rel_path"
          class="lesson"
          :class="{ 'lesson--completed': lesson.completed }"
          :to="lessonRoute(lesson.url)"
        >
          <TypeIcon :type="lesson.lesson_type" :with-label="false" />
          <span class="lesson__title">{{ lesson.title }}</span>
          <span v-if="lesson.progress_seconds" class="faint">{{ formatTime(lesson.progress_seconds) }}</span>
          <span class="spacer"></span>
          <span v-if="lesson.completed" class="lesson__done" title="Completed">✓</span>
          <span v-else class="lesson__pending" title="Not completed yet"></span>
        </RouterLink>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.node {
  display: flex;
  flex-direction: column;
}

.node__header {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 11px 14px;
  border: 1px solid var(--border);
  border-radius: 13px;
  background: var(--surface-strong);
  color: var(--text);
  cursor: pointer;
  text-align: left;
  transition: all var(--transition);
}

.node__header:hover:not(:disabled) {
  border-color: var(--border-strong);
  background: rgba(255, 255, 255, 0.11);
}

.node__header--leaf {
  cursor: default;
}

.node__chevron {
  display: inline-block;
  color: var(--text-faint);
  transition: transform var(--transition);
}

.node__chevron--open {
  transform: rotate(90deg);
}

.node__folder {
  font-size: 1rem;
}

.node__name {
  font-weight: 600;
  letter-spacing: -0.01em;
}

.node__stats {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.node__stats-bar {
  display: block;
  width: 74px;
  height: 6px;
  border-radius: 999px;
  background: rgba(148, 163, 184, 0.2);
  overflow: hidden;
}

.node__stats-fill {
  display: block;
  height: 100%;
  border-radius: 999px;
  background: var(--gradient);
  transition: width 400ms cubic-bezier(0.4, 0, 0.2, 1);
}

.node__body {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin: 8px 0 4px 20px;
  padding-left: 14px;
  border-left: 1px dashed rgba(148, 163, 184, 0.22);
}

.lesson {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 14px;
  border: 1px solid transparent;
  border-radius: 12px;
  background: rgba(255, 255, 255, 0.03);
  transition: all var(--transition);
}

.lesson:hover {
  background: rgba(255, 255, 255, 0.08);
  border-color: var(--border);
  transform: translateX(3px);
}

.lesson--completed {
  background: rgba(52, 211, 153, 0.07);
  border-color: rgba(52, 211, 153, 0.2);
}

.lesson--completed:hover {
  background: rgba(52, 211, 153, 0.13);
}

.lesson__title {
  font-size: 0.95rem;
}

.lesson__done {
  color: var(--success);
  font-weight: 700;
}

.lesson__pending {
  width: 9px;
  height: 9px;
  border-radius: 999px;
  border: 1.5px solid rgba(148, 163, 184, 0.5);
}

.tree-enter-active,
.tree-leave-active {
  transition: opacity 180ms ease;
}

.tree-enter-from,
.tree-leave-to {
  opacity: 0;
}

@media (max-width: 720px) {
  .node__body {
    margin-left: 10px;
    padding-left: 10px;
  }

  .node__stats {
    display: none;
  }
}
</style>
