<script setup>
import { computed, ref } from 'vue'
import CourseTree from '../components/CourseTree.vue'
import ProgressBar from '../components/ProgressBar.vue'
import { lessonRoute } from '../api.js'
import { refreshState, resetCourse, store } from '../store.js'
import { useToast } from '../composables/useToast.js'

const toast = useToast()
const filter = ref('')
const busy = ref(false)

const stats = computed(
  () =>
    store.stats || {
      total_lessons: 0,
      completed_lessons: 0,
      completion_percentage: 0,
      last_accessed_url: '',
      last_accessed_title: ''
    }
)

const remaining = computed(() => Math.max(0, stats.value.total_lessons - stats.value.completed_lessons))

async function changeCourse() {
  busy.value = true
  try {
    await resetCourse()
    toast.info('Pick another course')
  } catch (error) {
    toast.error(error.message)
  } finally {
    busy.value = false
  }
}

async function reload() {
  await refreshState()
  toast.info('Progress refreshed')
}
</script>

<template>
  <div class="dash">
    <header class="dash__header card">
      <div class="dash__title">
        <span class="dash__icon" aria-hidden="true">📚</span>
        <div>
          <h2>{{ store.course.name }}</h2>
          <p class="mono faint">{{ store.course.path }}</p>
        </div>
      </div>
      <div class="row">
        <button type="button" class="btn btn--ghost btn--sm" @click="reload">⟳ Refresh</button>
        <button type="button" class="btn btn--ghost btn--sm" :disabled="busy" @click="changeCourse">
          ⇄ Select different course
        </button>
      </div>
    </header>

    <section class="dash__grid">
      <div class="card dash__progress">
        <div class="section-title">📈 Your progress</div>
        <div class="dash__percent">{{ stats.completion_percentage.toFixed(1) }}<span>%</span></div>
        <ProgressBar
          :percentage="stats.completion_percentage"
          :completed="stats.completed_lessons"
          :total="stats.total_lessons"
        />
        <div class="chips">
          <span class="chip"><strong>{{ stats.total_lessons }}</strong> lessons</span>
          <span class="chip chip--done"><strong>{{ stats.completed_lessons }}</strong> completed</span>
          <span class="chip"><strong>{{ remaining }}</strong> remaining</span>
        </div>
      </div>

      <div class="card dash__resume" :class="{ 'dash__resume--empty': !stats.last_accessed_url }">
        <template v-if="stats.last_accessed_url">
          <span class="faint">Continue where you left off</span>
          <h3>{{ stats.last_accessed_title || stats.last_accessed_path }}</h3>
          <RouterLink class="btn" :to="lessonRoute(stats.last_accessed_url)">▶ Resume lesson</RouterLink>
        </template>
        <template v-else>
          <span class="faint">Nothing started yet</span>
          <h3>Pick a lesson below to begin</h3>
          <p class="faint">Your position is remembered automatically, even after a restart.</p>
        </template>
      </div>
    </section>

    <section class="card">
      <div class="dash__tree-head">
        <div class="section-title">🧭 Course content</div>
        <span class="spacer"></span>
        <input v-model="filter" class="input dash__search" type="search" placeholder="Search lessons…" />
      </div>
      <div class="dash__tree">
        <CourseTree v-if="store.tree" :node="store.tree" :filter="filter" />
        <p v-else class="empty-state">This course has no supported files yet.</p>
      </div>
    </section>
  </div>
</template>

<style scoped>
.dash {
  display: flex;
  flex-direction: column;
  gap: 22px;
}

.dash__header {
  display: flex;
  align-items: center;
  gap: 16px;
  flex-wrap: wrap;
}

.dash__title {
  display: flex;
  align-items: center;
  gap: 14px;
  min-width: 0;
}

.dash__icon {
  display: grid;
  place-items: center;
  width: 48px;
  height: 48px;
  border-radius: 14px;
  background: var(--surface-strong);
  border: 1px solid var(--border);
  font-size: 1.3rem;
}

.dash__title h2 {
  font-size: 1.3rem;
  font-weight: 700;
  letter-spacing: -0.02em;
}

.dash__grid {
  display: grid;
  grid-template-columns: minmax(300px, 1.15fr) minmax(280px, 1fr);
  gap: 22px;
}

.dash__progress {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.dash__percent {
  font-size: 2.6rem;
  font-weight: 750;
  letter-spacing: -0.03em;
  line-height: 1;
  background: var(--gradient);
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
}

.dash__percent span {
  font-size: 1.3rem;
}

.chips {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  margin-top: 4px;
}

.chip {
  padding: 6px 14px;
  border-radius: 999px;
  border: 1px solid var(--border);
  background: var(--surface-strong);
  color: var(--text-muted);
  font-size: 0.84rem;
}

.chip strong {
  color: var(--text);
  margin-right: 4px;
}

.chip--done {
  border-color: rgba(52, 211, 153, 0.3);
  background: var(--success-soft);
  color: #a7f3d0;
}

.chip--done strong {
  color: #6ee7b7;
}

.dash__resume {
  display: flex;
  flex-direction: column;
  gap: 10px;
  justify-content: center;
  background:
    linear-gradient(135deg, rgba(99, 102, 241, 0.22), rgba(34, 211, 238, 0.12)),
    var(--surface);
  border-color: rgba(99, 102, 241, 0.35);
}

.dash__resume h3 {
  font-size: 1.15rem;
  font-weight: 650;
}

.dash__resume .btn {
  align-self: flex-start;
  margin-top: 6px;
}

.dash__resume--empty {
  background: var(--surface);
  border-color: var(--border);
}

.dash__tree-head {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 16px;
}

.dash__search {
  max-width: 260px;
}

.dash__tree {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.dash__tree :deep(.node > .node__header) {
  background: rgba(255, 255, 255, 0.05);
}

@media (max-width: 860px) {
  .dash__grid {
    grid-template-columns: 1fr;
  }

  .dash__search {
    max-width: none;
    width: 100%;
  }
}
</style>
