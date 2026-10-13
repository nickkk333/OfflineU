<script setup>
import { computed, ref } from 'vue'
import CourseTree from '../components/CourseTree.vue'
import ProgressBar from '../components/ProgressBar.vue'
import { api, lessonRoute, lessonFileName } from '../api.js'
import { refreshState, resetCourse, store } from '../store.js'
import { t } from '../i18n.js'
import { useToast } from '../composables/useToast.js'

const toast = useToast()
const filter = ref('')
const busy = ref(false)
const pendingExclude = ref(null)
// Excluded list stays collapsed so a long hidden list never pushes the course
// content down; the header toggles it open.
const showExcluded = ref(false)

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

const excluded = computed(() => store.excluded || [])

const remaining = computed(() => Math.max(0, stats.value.total_lessons - stats.value.completed_lessons))

// The resume card shows the lesson the same way the tree does, extension
// included. last_accessed_path is the lesson's rel_path, hence a valid input.
const resumeName = computed(
  () =>
    lessonFileName({
      title: stats.value.last_accessed_title,
      rel_path: stats.value.last_accessed_path
    }) || stats.value.last_accessed_path
)

async function changeCourse() {
  busy.value = true
  try {
    await resetCourse()
    toast.info(t('toast.pickAnother'))
  } catch (error) {
    toast.error(error.message)
  } finally {
    busy.value = false
  }
}

async function reload() {
  await refreshState()
  toast.info(t('toast.progressRefreshed'))
}

// Hiding is a two-step flow: the tree emits what was clicked, a small
// confirm dialog names it, and only then the backend exclusion is written.
function askExclude(target) {
  pendingExclude.value = target
}

function cancelExclude() {
  pendingExclude.value = null
}

async function confirmExclude() {
  const target = pendingExclude.value
  if (!target) return
  pendingExclude.value = null
  busy.value = true
  try {
    await api.exclude(target.path)
    await refreshState()
    toast.success(t('toast.excluded', { name: target.name || target.path }))
  } catch (error) {
    toast.error(error.message)
  } finally {
    busy.value = false
  }
}

async function restoreOne(path) {
  busy.value = true
  try {
    await api.restoreExcluded(path)
    await refreshState()
    toast.success(t('toast.restored', { name: path }))
  } catch (error) {
    toast.error(error.message)
  } finally {
    busy.value = false
  }
}

async function restoreAll() {
  busy.value = true
  try {
    await api.restoreAllExcluded()
    await refreshState()
    toast.success(t('toast.restoredAll'))
  } catch (error) {
    toast.error(error.message)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="dash">
    <header class="dash__header card">
      <div class="dash__title">
        <span class="dash__icon" aria-hidden="true">📚</span>
        <div>
          <h2>{{ store.course.name }}</h2>
          <p class="mono faint">{{ store.course.display_path || store.course.path }}</p>
        </div>
      </div>
      <div class="row">
        <button type="button" class="btn btn--ghost btn--sm" @click="reload">{{ t('common.refresh') }}</button>
        <button type="button" class="btn btn--ghost btn--sm" :disabled="busy" @click="changeCourse">
          {{ t('dash.changeCourse') }}
        </button>
      </div>
    </header>

    <section class="dash__grid">
      <div class="card dash__progress">
        <div class="section-title">{{ t('dash.progressTitle') }}</div>
        <div class="dash__percent">{{ stats.completion_percentage.toFixed(1) }}<span>%</span></div>
        <ProgressBar
          :percentage="stats.completion_percentage"
          :completed="stats.completed_lessons"
          :total="stats.total_lessons"
        />
        <div class="chips">
          <span class="chip"><strong>{{ stats.total_lessons }}</strong> {{ t('dash.chipLessons') }}</span>
          <span class="chip chip--done"><strong>{{ stats.completed_lessons }}</strong> {{ t('dash.chipCompleted') }}</span>
          <span class="chip"><strong>{{ remaining }}</strong> {{ t('dash.chipRemaining') }}</span>
        </div>
      </div>

      <div class="card dash__resume" :class="{ 'dash__resume--empty': !stats.last_accessed_url }">
        <template v-if="stats.last_accessed_url">
          <span class="faint">{{ t('dash.resumeLabel') }}</span>
          <h3>{{ resumeName }}</h3>
          <RouterLink class="btn" :to="lessonRoute(stats.last_accessed_url)">{{ t('dash.resumeButton') }}</RouterLink>
        </template>
        <template v-else>
          <span class="faint">{{ t('dash.emptyLabel') }}</span>
          <h3>{{ t('dash.emptyTitle') }}</h3>
          <p class="faint">{{ t('dash.emptyText') }}</p>
        </template>
      </div>
    </section>

    <section class="card">
      <div class="dash__tree-head">
        <div class="section-title">{{ t('dash.contentTitle') }}</div>
        <span class="spacer"></span>
        <input v-model="filter" class="input dash__search" type="search" :placeholder="t('dash.searchPlaceholder')" />
      </div>
      <div v-if="excluded.length" class="excluded">
        <div class="excluded__head">
          <button
            type="button"
            class="excluded__toggle"
            :aria-expanded="showExcluded"
            @click="showExcluded = !showExcluded"
          >
            <span class="excluded__chevron" :class="{ 'excluded__chevron--open': showExcluded }" aria-hidden="true">▸</span>
            <span class="faint">{{ t('dash.excludedTitle', { count: excluded.length }) }}</span>
          </button>
          <span class="spacer"></span>
          <button type="button" class="btn btn--ghost btn--sm" :disabled="busy" @click="restoreAll">
            {{ t('dash.restoreAll') }}
          </button>
        </div>
        <ul v-show="showExcluded" class="excluded__list">
          <li v-for="path in excluded" :key="path" class="excluded__item">
            <span class="mono excluded__path">{{ path }}</span>
            <span class="spacer"></span>
            <button type="button" class="btn btn--ghost btn--sm" :disabled="busy" @click="restoreOne(path)">
              {{ t('dash.restore') }}
            </button>
          </li>
        </ul>
      </div>
      <div class="dash__tree">
        <CourseTree v-if="store.tree" :node="store.tree" :filter="filter" @exclude="askExclude" />
        <p v-else class="empty-state">{{ t('dash.emptyContent') }}</p>
      </div>
    </section>

    <div v-if="pendingExclude" class="confirm-backdrop" @click.self="cancelExclude">
      <div class="confirm" role="alertdialog" aria-modal="true">
        <div class="confirm__title">{{ t('dash.excludeConfirmTitle') }}</div>
        <p class="confirm__text">
          {{
            pendingExclude.kind === 'folder'
              ? t('dash.excludeFolderText', { name: pendingExclude.name || pendingExclude.path })
              : t('dash.excludeFileText', { name: pendingExclude.name || pendingExclude.path })
          }}
        </p>
        <span class="confirm__path mono">{{ pendingExclude.path }}</span>
        <div class="confirm__actions">
          <button type="button" class="btn btn--ghost" :disabled="busy" @click="cancelExclude">
            {{ t('common.cancel') }}
          </button>
          <button type="button" class="btn btn--danger" :disabled="busy" @click="confirmExclude">
            {{ t('dash.excludeConfirm') }}
          </button>
        </div>
      </div>
    </div>
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

.excluded {
  margin-bottom: 16px;
  padding: 12px 14px;
  border: 1px dashed rgba(148, 163, 184, 0.35);
  border-radius: 13px;
  background: rgba(148, 163, 184, 0.06);
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.excluded__head {
  display: flex;
  align-items: center;
  gap: 10px;
}

/* The hidden-list header doubles as the expand/collapse switch, so it looks
   like plain text until hovered instead of like the action buttons next to it. */
.excluded__toggle {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  padding: 2px 4px;
  border: none;
  border-radius: 8px;
  background: none;
  cursor: pointer;
  text-align: left;
  transition: background var(--transition);
}

.excluded__toggle:hover {
  background: rgba(255, 255, 255, 0.07);
}

.excluded__chevron {
  color: var(--text-faint);
  transition: transform var(--transition);
}

.excluded__chevron--open {
  transform: rotate(90deg);
}

.excluded__list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.excluded__item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 10px;
  border-radius: 10px;
  background: rgba(255, 255, 255, 0.03);
}

.excluded__path {
  font-size: 0.85rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.confirm-backdrop {
  position: fixed;
  inset: 0;
  z-index: 60;
  display: grid;
  place-items: center;
  padding: 20px;
  background: rgba(2, 6, 16, 0.6);
  backdrop-filter: blur(3px);
}

.confirm {
  width: min(440px, 100%);
  padding: 22px;
  border-radius: 18px;
  border: 1px solid var(--border);
  background: var(--surface-strong);
  box-shadow: 0 24px 60px rgba(0, 0, 0, 0.45);
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.confirm__title {
  font-weight: 700;
}

.confirm__text {
  color: var(--text-muted);
  font-size: 0.92rem;
}

.confirm__path {
  font-size: 0.82rem;
  color: var(--text-faint);
  word-break: break-all;
}

.confirm__actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 4px;
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
