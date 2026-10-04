<script setup>
import { computed, ref } from 'vue'
import DirectoryBrowser from '../components/DirectoryBrowser.vue'
import { forgetCourse, loadCourse, refreshState, store } from '../store.js'
import { t } from '../i18n.js'
import { useToast } from '../composables/useToast.js'

const toast = useToast()
const manualPath = ref('')
const loading = ref(false)

async function openRecent(path) {
  loading.value = true
  try {
    const payload = await loadCourse(path)
    toast.success(t('toast.loaded', { name: payload.course_name }))
  } catch (error) {
    toast.error(error.message)
  } finally {
    loading.value = false
  }
}

async function loadFromInput() {
  const value = manualPath.value.trim()
  if (!value) {
    toast.error(t('toast.enterPath'))
    return
  }
  loading.value = true
  try {
    const payload = await loadCourse(value)
    toast.success(t('toast.loaded', { name: payload.course_name }))
    manualPath.value = ''
  } catch (error) {
    toast.error(error.message)
  } finally {
    loading.value = false
  }
}

async function forget(path) {
  try {
    await forgetCourse(path)
    toast.info(t('toast.removedFromRecent'))
  } catch (error) {
    toast.error(error.message)
  }
}

// Completion percentage of a remembered course (0 when it was never parsed).
function percentOf(recent) {
  const total = recent.total_lessons || 0
  if (total <= 0) return 0
  return Math.round((Math.min(recent.completed_lessons || 0, total) / total) * 100)
}

function isFinished(recent) {
  return Boolean(recent.total_lessons) && (recent.completed_lessons || 0) >= recent.total_lessons
}

// The first mapped folder is the one the notice talks about (the image maps
// exactly one: /courses).
const primaryRoot = computed(() => store.roots[0] || '/courses')

// The notice used to claim "no course folder is mapped" whatever the real cause
// was. The server now reports which case it is, so the wording can name it: an
// empty folder, a folder the container may not read, or a mapping that never
// arrived.
const noticeTitle = computed(() => {
  if (store.mountIssue === 'empty') return t('picker.noticeTitleEmpty', { root: primaryRoot.value })
  if (store.mountIssue === 'unreadable') return t('picker.noticeTitleUnreadable', { root: primaryRoot.value })
  if (store.mountIssue === 'missing') return t('picker.noticeTitleMissing', { root: primaryRoot.value })
  return t('picker.noticeTitle')
})

const noticeText = computed(() => {
  if (store.mountIssue === 'empty') return t('picker.noticeTextEmpty')
  if (store.mountIssue === 'unreadable') return t('picker.noticeTextUnreadable')
  if (store.mountIssue === 'missing') return t('picker.noticeTextMissing', { root: primaryRoot.value })
  return t('picker.noticeText')
})

// The docker run/compose reference only helps when nothing was mapped at all;
// once the folders are mapped it is just noise.
const showCommands = computed(() => !['empty', 'unreadable'].includes(store.mountIssue))

// One readable line per mapped folder for the status list.
function rootStateLabel(status) {
  if (!status.exists) return t('picker.mountStatusMissing')
  if (!status.readable) return t('picker.mountStatusUnreadable')
  if (status.has_files) return t('picker.mountStatusOk', { count: status.entries || 0 })
  if (status.mount === 'volume') return t('picker.mountStatusVolume')
  if (status.mount === 'none') return t('picker.mountStatusNotMounted')
  return t('picker.mountStatusEmpty')
}
</script>

<template>
  <div class="picker">
    <section v-if="store.needsMount" class="card card--notice">
      <div class="section-title">{{ noticeTitle }}</div>
      <p class="card__hint">{{ noticeText }}</p>

      <p v-if="store.rootsDetail.length" class="card__hint">
        <strong>{{ t('picker.mountStatusTitle') }}</strong>
      </p>
      <ul v-if="store.rootsDetail.length" class="tips">
        <li v-for="status in store.rootsDetail" :key="status.path">
          <strong>{{ status.display || status.path }}</strong>
          <span class="mono">{{ status.path }}</span> — {{ rootStateLabel(status) }}
          <span v-if="status.mount_detail" class="faint mono">({{ status.mount_detail }})</span>
        </li>
      </ul>

      <ul v-if="store.mountIssue === 'empty'" class="tips">
        <li>{{ t('picker.emptyFixCopy') }}</li>
        <li>{{ t('picker.emptyFixRecheck') }}</li>
        <li>{{ t('picker.emptyFixBrowse') }}</li>
      </ul>
      <ul v-else-if="store.mountIssue === 'unreadable'" class="tips">
        <li>{{ t('picker.unreadableFixChmod') }}</li>
        <li>{{ t('picker.unreadableFixOwner') }}</li>
        <li>{{ t('picker.unreadableFixRoot') }}</li>
        <li>{{ t('picker.unreadableFixData') }}</li>
        <li v-if="store.readCapability === false">{{ t('picker.unreadableFixNoCaps') }}</li>
      </ul>
      <ul v-else-if="store.mountIssue === 'volume' || store.mountIssue === 'not_mounted'" class="tips">
        <li>{{ t('picker.mappingFixFolders') }}</li>
        <li>{{ t('picker.mappingFixRecreate') }}</li>
      </ul>

      <pre v-if="showCommands" class="notice__code mono">{{ store.mountHint }}</pre>

      <div class="notice__actions">
        <button type="button" class="btn btn--ghost btn--sm" :disabled="store.loading" @click="refreshState()">
          {{ t('common.checkAgain') }}
        </button>
        <span class="faint">
          {{ t('picker.noticeFooterBefore') }}<span class="mono">{{ primaryRoot }}</span>{{ t('picker.noticeFooterAfter') }}
        </span>
      </div>
    </section>

    <section v-if="store.recentCourses.length" class="card">
      <div class="section-title">{{ t('picker.recentTitle') }}</div>
      <p class="card__hint">{{ t('picker.recentHint') }}</p>
      <div class="recent">
        <div v-for="recent in store.recentCourses" :key="recent.path" class="recent__row">
          <span class="recent__icon" aria-hidden="true">📚</span>
          <div class="recent__info">
            <span class="recent__name">{{ recent.name }}</span>
            <span class="recent__path mono">{{ recent.display_path || recent.path }}</span>
          </div>
          <div
            v-if="recent.total_lessons"
            class="recent__progress"
            :title="t('picker.recentProgressTitle', { completed: recent.completed_lessons || 0, total: recent.total_lessons })"
          >
            <span class="recent__track" aria-hidden="true">
              <span class="recent__fill" :style="{ width: percentOf(recent) + '%' }"></span>
            </span>
            <span class="recent__percent mono" :class="{ 'recent__percent--done': isFinished(recent) }">
              {{ percentOf(recent) }}%
            </span>
          </div>
          <button type="button" class="btn btn--sm" :disabled="loading" @click="openRecent(recent.path)">
            {{ t('common.open') }}
          </button>
          <button
            type="button"
            class="icon-btn"
            :title="t('common.remove')"
            @click="forget(recent.path)"
          >
            ✕
          </button>
        </div>
      </div>
    </section>

    <section class="card">
      <div class="section-title">{{ t('picker.selectTitle') }}</div>
      <p class="card__hint">{{ t('picker.selectHint') }}</p>
      <DirectoryBrowser />
      <div class="manual">
        <input
          v-model="manualPath"
          class="input"
          type="text"
          :placeholder="t('picker.manualPlaceholder')"
          @keydown.enter="loadFromInput"
        />
        <button type="button" class="btn" :disabled="loading" @click="loadFromInput">
          {{ t('common.loadCourse') }}
        </button>
      </div>
    </section>

    <section class="grid">
      <div class="card">
        <div class="section-title">{{ t('picker.howToTitle') }}</div>
        <ul class="tips">
          <li>
            <strong>{{ t('picker.howTo.prepareLabel') }}</strong>
            {{ t('picker.howTo.prepareText') }}
          </li>
          <li>
            <strong>{{ t('picker.howTo.browseLabel') }}</strong>
            {{ t('picker.howTo.browseText') }}
          </li>
          <li>
            <strong>{{ t('picker.howTo.loadLabel') }}</strong>
            {{ t('picker.howTo.loadText') }}
          </li>
          <li>
            <strong>{{ t('picker.howTo.comeBackLabel') }}</strong>
            {{ t('picker.howTo.comeBackText') }}
          </li>
          <li>
            <strong>{{ t('picker.howTo.autoplayLabel') }}</strong>
            {{ t('picker.howTo.autoplayText') }}
          </li>
        </ul>
      </div>

      <div class="card">
        <div class="section-title">{{ t('picker.typesTitle') }}</div>
        <ul class="tips">
          <li>
            <strong>{{ t('picker.types.videoLabel') }}</strong>
            {{ t('picker.types.videoText') }}
          </li>
          <li>
            <strong>{{ t('picker.types.audioLabel') }}</strong>
            {{ t('picker.types.audioText') }}
          </li>
          <li>
            <strong>{{ t('picker.types.docsLabel') }}</strong>
            {{ t('picker.types.docsText') }}
          </li>
          <li>
            <strong>{{ t('picker.types.subsLabel') }}</strong>
            {{ t('picker.types.subsText') }}
          </li>
          <li>
            <strong>{{ t('picker.types.quizLabel') }}</strong>
            {{ t('picker.types.quizText') }}
          </li>
        </ul>
        <p class="faint" style="margin-top: 12px">{{ t('picker.shortcuts') }}</p>
      </div>
    </section>
  </div>
</template>

<style scoped>
.picker {
  display: flex;
  flex-direction: column;
  gap: 22px;
}

.recent {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin-top: 16px;
}

.recent__row {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 12px 16px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: rgba(8, 12, 22, 0.4);
  transition: border-color var(--transition), background var(--transition);
}

.recent__row:hover {
  border-color: var(--border-strong);
  background: rgba(255, 255, 255, 0.05);
}

.recent__icon {
  font-size: 1.2rem;
}

.recent__info {
  display: flex;
  flex-direction: column;
  min-width: 0;
  flex: 1;
}

.recent__name {
  font-weight: 600;
}

.recent__path {
  color: var(--text-faint);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* Progress pill sitting right next to the "Open" button ----------------- */
.recent__progress {
  display: flex;
  align-items: center;
  gap: 9px;
  flex-shrink: 0;
  padding: 4px 11px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--surface-strong);
}

.recent__track {
  display: block;
  width: 56px;
  height: 6px;
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.13);
  overflow: hidden;
}

.recent__fill {
  display: block;
  height: 100%;
  border-radius: 999px;
  background: var(--gradient);
  transition: width var(--transition);
}

.recent__percent {
  min-width: 36px;
  text-align: right;
  font-size: 0.78rem;
  font-weight: 600;
  color: var(--text-muted);
}

.recent__percent--done {
  color: #6ee7b7;
}

/* "Nothing is mounted yet" notice -------------------------------------- */
.card--notice {
  border-color: rgba(251, 191, 36, 0.34);
  background: linear-gradient(180deg, rgba(251, 191, 36, 0.07), rgba(8, 12, 22, 0.42));
}

.notice__code {
  margin-top: 14px;
  padding: 14px 16px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: rgba(2, 6, 23, 0.6);
  color: var(--text-muted);
  font-size: 0.78rem;
  line-height: 1.55;
  overflow-x: auto;
  white-space: pre;
}

.notice__actions {
  display: flex;
  align-items: center;
  gap: 14px;
  margin-top: 14px;
  flex-wrap: wrap;
  font-size: 0.86rem;
}

.manual {
  display: flex;
  gap: 12px;
  margin-top: 18px;
  flex-wrap: wrap;
}

.manual .input {
  flex: 1;
  min-width: 260px;
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
  gap: 22px;
}

.tips {
  margin-top: 14px;
  padding-left: 20px;
  display: flex;
  flex-direction: column;
  gap: 9px;
  color: var(--text-muted);
  font-size: 0.92rem;
}

.tips strong {
  color: var(--text);
}
</style>
