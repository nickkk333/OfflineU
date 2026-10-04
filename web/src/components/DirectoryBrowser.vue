<script setup>
import { computed, onMounted, ref } from 'vue'
import { api } from '../api.js'
import { loadCourse, store } from '../store.js'
import { t } from '../i18n.js'
import { useToast } from '../composables/useToast.js'

const toast = useToast()

const currentPath = ref('')
const currentDisplay = ref('')
const parentPath = ref(null)
const directories = ref([])
const rootsDisplay = ref([])
const loading = ref(false)
const loadingCourse = ref(false)
const error = ref('')

// Shortcuts back to the mounted folders, shown in the toolbar. The folder you
// are currently in is skipped so the row never repeats the path next to it.
const rootLinks = computed(() =>
  store.roots
    .map((path, index) => ({
      path,
      label: rootsDisplay.value[index] || store.rootsDisplay[index] || path
    }))
    .filter((root) => root.label !== (currentDisplay.value || currentPath.value))
)

// An empty listing means something different depending on the mapping state:
// "nothing is mounted" and "mapped, but the folder is empty" must not read the
// same, so the message follows what the server reported about the folder.
const emptyText = computed(() => {
  if (!store.needsMount) return t('browser.empty')
  return store.mountIssue === 'empty' ? t('browser.emptyMapped') : t('browser.emptyNeedsMount')
})

async function navigate(path) {
  loading.value = true
  error.value = ''
  try {
    const payload = await api.browse(path || '')
    currentPath.value = payload.current_path
    currentDisplay.value = payload.current_display_path || payload.current_path || ''
    parentPath.value = payload.parent_path
    rootsDisplay.value = payload.roots_display || []
    directories.value = payload.directories || []
  } catch (cause) {
    error.value = cause.message
    directories.value = []
  } finally {
    loading.value = false
  }
}

async function useAsCourse(path) {
  if (!path) return
  loadingCourse.value = true
  try {
    const payload = await loadCourse(path)
    toast.success(t('toast.loaded', { name: payload.course_name }))
  } catch (cause) {
    toast.error(cause.message)
  } finally {
    loadingCourse.value = false
  }
}

onMounted(() => navigate(''))
</script>

<template>
  <div class="browser">
    <div class="browser__bar">
      <button
        type="button"
        class="btn btn--ghost btn--sm"
        :disabled="!parentPath"
        @click="navigate(parentPath)"
      >
        {{ t('common.up') }}
      </button>
      <button type="button" class="btn btn--ghost btn--sm" @click="navigate(currentPath)">
        {{ t('common.refresh') }}
      </button>
      <template v-for="root in rootLinks" :key="root.path">
        <span class="browser__divider" aria-hidden="true"></span>
        <button
          type="button"
          class="browser__root mono"
          :title="t('common.goTo', { label: root.label })"
          @click="navigate(root.path)"
        >
          {{ root.label }}
        </button>
      </template>
      <span class="browser__path mono">{{ currentDisplay || currentPath || t('common.loading') }}</span>
    </div>

    <div class="browser__list">
      <p v-if="loading" class="browser__state"><span class="spinner"></span> {{ t('browser.listing') }}</p>
      <p v-else-if="error" class="browser__state browser__state--error">{{ error }}</p>
      <p v-else-if="!directories.length" class="browser__state">
        {{ emptyText }}
      </p>
      <template v-else>
        <div
          v-for="directory in directories"
          :key="directory.path"
          class="dir"
          :class="{ 'dir--candidate': directory.is_course_candidate }"
        >
          <button type="button" class="dir__open" @click="navigate(directory.path)">
            <span aria-hidden="true">📁</span>
            <span class="dir__name">{{ directory.name }}</span>
          </button>
          <span v-if="directory.media_files" class="badge">{{ t('browser.mediaCount', { count: directory.media_files }) }}</span>
          <span v-else-if="directory.is_course_candidate" class="badge badge--success">{{ t('browser.courseBadge') }}</span>
          <button
            v-if="directory.is_course_candidate"
            type="button"
            class="btn btn--sm"
            :disabled="loadingCourse"
            @click="useAsCourse(directory.path)"
          >
            {{ t('browser.useAsCourse') }}
          </button>
        </div>
      </template>
    </div>

    <div class="browser__actions">
      <button
        type="button"
        class="btn"
        :disabled="!currentPath || loadingCourse"
        @click="useAsCourse(currentPath)"
      >
        {{ t('browser.useCurrent') }}
      </button>
      <span class="faint">{{ t('browser.actionsHint') }}</span>
    </div>
  </div>
</template>

<style scoped>
.browser {
  border: 1px solid var(--border);
  border-radius: var(--radius);
  overflow: hidden;
  background: rgba(8, 12, 22, 0.42);
}

.browser__bar {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 14px;
  border-bottom: 1px solid var(--border);
  flex-wrap: wrap;
}

.browser__path {
  color: var(--accent-3);
}

.browser__divider {
  width: 1px;
  height: 18px;
  background: var(--border);
}

.browser__root {
  background: none;
  border: none;
  padding: 0;
  cursor: pointer;
  color: var(--accent-3);
  font-size: 0.85rem;
  transition: color var(--transition);
}

.browser__root:hover {
  color: var(--accent);
  text-decoration: underline;
}

.browser__list {
  max-height: 320px;
  overflow-y: auto;
}

.browser__state {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 22px 16px;
  color: var(--text-muted);
  font-size: 0.9rem;
}

.browser__state--error {
  color: var(--danger);
}

.dir {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 14px;
  border-bottom: 1px solid rgba(148, 163, 184, 0.08);
  transition: background var(--transition);
}

.dir:hover {
  background: rgba(255, 255, 255, 0.04);
}

.dir--candidate {
  background: rgba(52, 211, 153, 0.06);
}

.dir__open {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: 1;
  min-width: 0;
  background: none;
  border: none;
  padding: 3px 0;
  cursor: pointer;
  text-align: left;
}

.dir__name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dir__open:hover .dir__name {
  color: var(--accent-3);
}

.browser__actions {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 13px 14px;
  flex-wrap: wrap;
}
</style>
