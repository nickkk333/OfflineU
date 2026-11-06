<script setup>
// Course sources: a folder on the server, a WebDAV collection or an SMB share.
// All three are browsed through the same /browse API and loaded like a mapped
// folder, so the rest of the UI never has to know where the bytes come from.
// The form lives in SourceForm.vue because the login page uses it too.
import { computed, ref } from 'vue'
import SourceForm from './SourceForm.vue'
import { api } from '../api.js'
import { refreshState, store } from '../store.js'
import { t } from '../i18n.js'
import { useToast } from '../composables/useToast.js'

const toast = useToast()

const showForm = ref(false)
const editing = ref(null)
const testingId = ref('')
const pendingRemove = ref(null)
const removing = ref(false)

function openForm() {
  editing.value = null
  showForm.value = true
}

function closeForm() {
  showForm.value = false
  editing.value = null
}

function editSource(source) {
  editing.value = source
  showForm.value = true
}

async function onSaved() {
  await refreshState()
  closeForm()
}

function targetOf(source) {
  if (!source) return ''
  if (source.kind === 'local') return source.path || ''
  if (source.kind === 'webdav') return source.url || ''
  const root = `\\\\${source.host || ''}\\${source.share || ''}`
  return source.base_path ? `${root}\\${source.base_path.replaceAll('/', '\\')}` : root
}

function kindLabel(kind) {
  return t(`sources.kind.${kind}`)
}

async function testExisting(source) {
  if (testingId.value) return
  testingId.value = source.id
  try {
    await api.testSource({ id: source.id })
    toast.success(t('sources.testOk', { name: source.name }))
  } catch (error) {
    toast.error(error.message)
  } finally {
    testingId.value = ''
  }
}

async function confirmRemove() {
  if (!pendingRemove.value || removing.value) return
  removing.value = true
  try {
    await api.removeSource(pendingRemove.value.id)
    await refreshState()
    toast.info(t('sources.removed'))
    pendingRemove.value = null
  } catch (error) {
    toast.error(error.message)
  } finally {
    removing.value = false
  }
}

const hasSources = computed(() => store.sources.length > 0)
</script>

<template>
  <section class="card sources">
    <div class="sources__head">
      <div>
        <div class="section-title">{{ t('sources.title') }}</div>
        <p class="card__hint">{{ t('sources.hint') }}</p>
      </div>
      <button type="button" class="btn btn--ghost btn--sm" @click="showForm ? closeForm() : openForm()">
        {{ showForm ? t('common.cancel') : t('sources.add') }}
      </button>
    </div>

    <p v-if="!hasSources && !showForm" class="sources__empty faint">{{ t('sources.empty') }}</p>

    <div v-else-if="hasSources" class="sources__list">
      <div v-for="source in store.sources" :key="source.id" class="source-row">
        <span class="badge">{{ kindLabel(source.kind) }}</span>
        <div class="source-row__info">
          <span class="source-row__name">{{ source.name }}</span>
          <span class="source-row__target mono">{{ targetOf(source) }}</span>
        </div>
        <span v-if="source.has_password" class="source-row__lock" :title="t('sources.passwordSaved')">🔒</span>
        <button
          type="button"
          class="btn btn--ghost btn--sm"
          :disabled="Boolean(testingId)"
          @click="testExisting(source)"
        >
          {{ testingId === source.id ? t('sources.testing') : t('sources.test') }}
        </button>
        <button type="button" class="icon-btn" :title="t('sources.edit')" @click="editSource(source)">
          ✎
        </button>
        <button type="button" class="icon-btn" :title="t('sources.remove')" @click="pendingRemove = source">
          ✕
        </button>
      </div>
    </div>

    <SourceForm
      v-if="showForm"
      :key="editing ? editing.id : 'new'"
      :mode="editing ? 'edit' : 'add'"
      :source="editing"
      show-cancel
      @saved="onSaved"
      @cancel="closeForm"
    />
  </section>

  <Teleport to="body">
    <Transition name="confirm">
      <div v-if="pendingRemove" class="confirm-backdrop" @click.self="pendingRemove = null">
        <div class="confirm" role="alertdialog" aria-modal="true" aria-labelledby="source-remove-title">
          <div id="source-remove-title" class="confirm__title">{{ t('sources.removeTitle') }}</div>
          <p class="confirm__text">{{ t('sources.removeConfirm', { name: pendingRemove.name }) }}</p>
          <span class="confirm__path mono">{{ targetOf(pendingRemove) }}</span>
          <div class="confirm__actions">
            <button type="button" class="btn btn--ghost" :disabled="removing" @click="pendingRemove = null">
              {{ t('common.cancel') }}
            </button>
            <button type="button" class="btn btn--danger" :disabled="removing" @click="confirmRemove">
              {{ t('sources.removeAction') }}
            </button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.sources__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.sources__head > div {
  min-width: 0;
}

.sources__empty {
  margin-top: 14px;
}

.sources__list {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin-top: 16px;
}

.source-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: rgba(8, 12, 22, 0.4);
}

.source-row__info {
  display: flex;
  flex-direction: column;
  min-width: 0;
  flex: 1;
}

.source-row__name {
  font-weight: 600;
}

.source-row__target {
  color: var(--text-faint);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.source-row__lock {
  font-size: 0.85rem;
}

/* Remove-source confirmation (same look as the course picker dialog) ------ */
.confirm-backdrop {
  position: fixed;
  inset: 0;
  z-index: 9998;
  display: grid;
  place-items: center;
  padding: 20px;
  background: rgba(2, 6, 23, 0.72);
  backdrop-filter: blur(6px);
}

.confirm {
  width: min(440px, 100%);
  padding: 22px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius);
  background: var(--surface);
  box-shadow: var(--shadow);
}

.confirm__title {
  font-size: 1.05rem;
  font-weight: 700;
}

.confirm__text {
  margin-top: 10px;
  color: var(--text-muted);
  font-size: 0.9rem;
  line-height: 1.6;
}

.confirm__path {
  display: block;
  margin-top: 8px;
  font-size: 0.78rem;
  color: var(--text-faint);
  word-break: break-all;
}

.confirm__actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 18px;
}

.confirm-enter-active,
.confirm-leave-active {
  transition: opacity 180ms ease;
}

.confirm-enter-active .confirm,
.confirm-leave-active .confirm {
  transition: transform 180ms ease;
}

.confirm-enter-from,
.confirm-leave-to {
  opacity: 0;
}

.confirm-enter-from .confirm,
.confirm-leave-to .confirm {
  transform: translateY(10px) scale(0.97);
}

@media (max-width: 640px) {
  .sources__head {
    flex-direction: column;
  }

  .source-row {
    flex-wrap: wrap;
  }

  .source-row__info {
    flex-basis: calc(100% - 80px);
  }
}
</style>
