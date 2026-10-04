<script setup>
import { ref } from 'vue'
import DirectoryBrowser from '../components/DirectoryBrowser.vue'
import { forgetCourse, loadCourse, store } from '../store.js'
import { useToast } from '../composables/useToast.js'

const toast = useToast()
const manualPath = ref('')
const loading = ref(false)

async function openRecent(path) {
  loading.value = true
  try {
    const payload = await loadCourse(path)
    toast.success(`Loaded "${payload.course_name}"`)
  } catch (error) {
    toast.error(error.message)
  } finally {
    loading.value = false
  }
}

async function loadFromInput() {
  const value = manualPath.value.trim()
  if (!value) {
    toast.error('Please enter the path of a course folder.')
    return
  }
  loading.value = true
  try {
    const payload = await loadCourse(value)
    toast.success(`Loaded "${payload.course_name}"`)
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
    toast.info('Removed from the recent list')
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
</script>

<template>
  <div class="picker">
    <section v-if="store.recentCourses.length" class="card">
      <div class="section-title">🕘 Recent courses</div>
      <p class="card__hint">
        Your previously opened folders are remembered, so you can switch back with one click.
      </p>
      <div class="recent">
        <div v-for="recent in store.recentCourses" :key="recent.path" class="recent__row">
          <span class="recent__icon" aria-hidden="true">📚</span>
          <div class="recent__info">
            <span class="recent__name">{{ recent.name }}</span>
            <span class="recent__path mono">{{ recent.path }}</span>
          </div>
          <div
            v-if="recent.total_lessons"
            class="recent__progress"
            :title="`${recent.completed_lessons || 0} of ${recent.total_lessons} lessons completed`"
          >
            <span class="recent__track" aria-hidden="true">
              <span class="recent__fill" :style="{ width: percentOf(recent) + '%' }"></span>
            </span>
            <span class="recent__percent mono" :class="{ 'recent__percent--done': isFinished(recent) }">
              {{ percentOf(recent) }}%
            </span>
          </div>
          <button type="button" class="btn btn--sm" :disabled="loading" @click="openRecent(recent.path)">
            Open
          </button>
          <button
            type="button"
            class="icon-btn"
            title="Remove from the list"
            @click="forget(recent.path)"
          >
            ✕
          </button>
        </div>
      </div>
    </section>

    <section class="card">
      <div class="section-title">📂 Select a course</div>
      <p class="card__hint">
        Browse to the folder that contains your course and load it. Videos, audio, documents and
        quizzes are detected automatically.
      </p>
      <DirectoryBrowser />
      <div class="manual">
        <input
          v-model="manualPath"
          class="input"
          type="text"
          placeholder="…or paste a folder path, e.g. D:\Courses\Python Tutorial"
          @keydown.enter="loadFromInput"
        />
        <button type="button" class="btn" :disabled="loading" @click="loadFromInput">Load course</button>
      </div>
    </section>

    <section class="grid">
      <div class="card">
        <div class="section-title">🚀 How to use</div>
        <ul class="tips">
          <li><strong>Prepare your files</strong> in a folder structure (one folder per section works great).</li>
          <li><strong>Browse</strong> to the course folder or paste its path above.</li>
          <li><strong>Load it</strong> and start learning — progress is saved automatically.</li>
          <li><strong>Come back anytime</strong>: courses are remembered and the last one reopens after a restart.</li>
          <li><strong>Continuous playback</strong>: a finished video rolls into the next one and your speed carries over.</li>
        </ul>
      </div>

      <div class="card">
        <div class="section-title">🗂️ Supported file types</div>
        <ul class="tips">
          <li><strong>Video</strong> — .mp4, .mkv, .avi, .mov, .webm, .m4v, .flv, .wmv</li>
          <li><strong>Audio</strong> — .mp3, .wav, .m4a, .aac, .ogg, .flac</li>
          <li><strong>Documents</strong> — .txt, .md, .html, .pdf, .docx, .doc, .rtf</li>
          <li><strong>Subtitles</strong> — .srt, .vtt, .ass, .sub, .sbv (converted to WebVTT on the fly)</li>
          <li><strong>Quizzes</strong> — any document whose name contains “quiz”, “exam” or “test”</li>
        </ul>
        <p class="faint" style="margin-top: 12px">
          Keyboard shortcuts in a lesson: space play/pause, ← → skip 10 s, ↑ ↓ volume.
        </p>
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
