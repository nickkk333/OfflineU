<script setup>
import { onMounted, ref } from 'vue'
import { api } from '../api.js'
import { loadCourse, store } from '../store.js'
import { useToast } from '../composables/useToast.js'

const toast = useToast()

const currentPath = ref('')
const parentPath = ref(null)
const directories = ref([])
const loading = ref(false)
const loadingCourse = ref(false)
const error = ref('')

async function navigate(path) {
  loading.value = true
  error.value = ''
  try {
    const payload = await api.browse(path || '')
    currentPath.value = payload.current_path
    parentPath.value = payload.parent_path
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
    toast.success(`Loaded "${payload.course_name}"`)
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
        ↑ Up
      </button>
      <button type="button" class="btn btn--ghost btn--sm" @click="navigate(currentPath)">
        ⟳ Refresh
      </button>
      <span class="browser__path mono">{{ currentPath || 'Loading…' }}</span>
    </div>

    <div class="browser__roots" v-if="store.roots.length">
      <span class="faint">Allowed roots</span>
      <button
        v-for="root in store.roots"
        :key="root"
        type="button"
        class="browser__root mono"
        @click="navigate(root)"
      >
        {{ root }}
      </button>
    </div>

    <div class="browser__list">
      <p v-if="loading" class="browser__state"><span class="spinner"></span> Listing folders…</p>
      <p v-else-if="error" class="browser__state browser__state--error">{{ error }}</p>
      <p v-else-if="!directories.length" class="browser__state">
        No sub-folders here. Use this folder as the course, or go up one level.
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
          <span v-if="directory.media_files" class="badge">{{ directory.media_files }} media</span>
          <span v-else-if="directory.is_course_candidate" class="badge badge--success">course</span>
          <button
            v-if="directory.is_course_candidate"
            type="button"
            class="btn btn--sm"
            :disabled="loadingCourse"
            @click="useAsCourse(directory.path)"
          >
            Use as course
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
        Use this folder as course
      </button>
      <span class="faint">Folders that contain videos or audio are highlighted and can be loaded directly.</span>
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

.browser__roots {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 14px;
  border-bottom: 1px solid var(--border);
  flex-wrap: wrap;
  font-size: 0.82rem;
}

.browser__root {
  border: 1px solid var(--border);
  background: var(--surface-strong);
  border-radius: 999px;
  padding: 3px 12px;
  cursor: pointer;
  color: var(--text-muted);
  transition: all var(--transition);
}

.browser__root:hover {
  color: var(--text);
  border-color: var(--border-strong);
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
