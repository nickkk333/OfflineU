<script setup>
// The WebDAV / SMB / local-folder form shared by the course-source manager and
// the login page. `mode` decides which API saves it:
//   add   -> POST /api/sources
//   edit  -> PUT  /api/sources   (id stays, an empty password keeps the old one)
//   login -> POST /api/sources/connect (verify + reuse + optionally open course)
import { computed, reactive, ref, watch } from 'vue'
import { api } from '../api.js'
import { t } from '../i18n.js'
import { useToast } from '../composables/useToast.js'

const props = defineProps({
  mode: { type: String, default: 'add' },
  // SourceInfo to edit; the API never returns passwords, so the field starts
  // empty and the server keeps the stored one.
  source: { type: Object, default: null },
  initialKind: { type: String, default: 'webdav' },
  autoLoad: { type: Boolean, default: false },
  showCancel: { type: Boolean, default: false }
})
const emit = defineEmits(['saved', 'cancel'])

const toast = useToast()
const KINDS = ['local', 'webdav', 'smb']

function emptyForm(kind) {
  return {
    name: '',
    kind,
    path: '',
    url: '',
    host: '',
    port: '',
    share: '',
    base_path: '',
    username: '',
    password: '',
    domain: '',
    insecure_tls: false
  }
}

function formFor(source, kind) {
  if (!source) return emptyForm(kind)
  return {
    ...emptyForm(source.kind || kind),
    name: source.name || '',
    kind: source.kind || kind,
    path: source.path || '',
    url: source.url || '',
    host: source.host || '',
    port: source.port ? String(source.port) : '',
    share: source.share || '',
    base_path: source.base_path || '',
    username: source.username || '',
    domain: source.domain || '',
    insecure_tls: Boolean(source.insecure_tls)
  }
}

const form = reactive(formFor(props.source, props.initialKind))
const testing = ref(false)
const saving = ref(false)
const formError = ref('')
const autoLoad = ref(props.autoLoad)
watch(() => props.autoLoad, (value) => {
  autoLoad.value = value
})

const isEdit = computed(() => props.mode === 'edit')
const isLogin = computed(() => props.mode === 'login')
const passwordPlaceholder = computed(() =>
  isEdit.value ? t('sources.passwordKeep') : isLogin.value ? t('login.passwordPlaceholder') : ''
)
const submitLabel = computed(() => {
  if (saving.value) return t('sources.saving')
  if (isLogin.value) return t('login.connect')
  if (isEdit.value) return t('sources.saveChanges')
  return t('sources.save')
})

// Only fields belonging to the chosen kind are sent: a stale WebDAV URL from
// another tab must never confuse source validation.
function formPayload() {
  const payload = {
    name: form.name.trim(),
    kind: form.kind
  }
  if (isEdit.value) payload.id = props.source.id
  if (form.kind === 'local') {
    payload.path = form.path.trim()
    return payload
  }
  payload.username = form.username.trim()
  payload.password = form.password
  if (form.kind === 'webdav') {
    payload.url = form.url.trim()
    payload.insecure_tls = Boolean(form.insecure_tls)
    return payload
  }
  payload.host = form.host.trim()
  payload.share = form.share.trim()
  payload.base_path = form.base_path.trim()
  payload.domain = form.domain.trim()
  if (form.port !== '' && form.port !== null) {
    const port = Number(form.port)
    if (Number.isFinite(port)) payload.port = port
  }
  return payload
}

async function testForm() {
  if (testing.value || saving.value) return
  testing.value = true
  formError.value = ''
  try {
    await api.testSource(formPayload())
    toast.success(t('sources.testOk', { name: form.name.trim() || t(`sources.kind.${form.kind}`) }))
  } catch (error) {
    formError.value = error.message
    toast.error(error.message)
  } finally {
    testing.value = false
  }
}

async function saveForm() {
  if (saving.value || testing.value) return
  saving.value = true
  formError.value = ''
  try {
    const payload = formPayload()
    let result
    if (isLogin.value) {
      result = await api.connectSource(payload, autoLoad.value)
    } else if (isEdit.value) {
      result = await api.updateSource(payload)
      result.action = 'updated'
    } else {
      result = await api.addSource(payload)
      result.action = 'created'
    }
    const name = result.source?.name || form.name.trim() || t(`sources.kind.${form.kind}`)
    if (isLogin.value) toast.success(t('login.connected', { name }))
    else if (isEdit.value) toast.success(t('sources.updated', { name }))
    else toast.success(t('sources.added', { name }))
    form.password = ''
    emit('saved', {
      info: result.source,
      action: result.action,
      courseName: result.course_name || ''
    })
  } catch (error) {
    formError.value = error.message
    toast.error(error.message)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <form class="source-form" @submit.prevent="saveForm">
    <div class="source-form__grid">
      <label class="field">
        <span class="field__label">{{ t('sources.kindLabel') }}</span>
        <select v-model="form.kind" class="input" :disabled="isEdit">
          <option v-for="kind in KINDS" :key="kind" :value="kind">{{ t(`sources.kind.${kind}`) }}</option>
        </select>
      </label>
      <label class="field">
        <span class="field__label">{{ t('sources.name') }}</span>
        <input v-model="form.name" class="input" type="text" :placeholder="t('sources.namePlaceholder')" />
      </label>

      <template v-if="form.kind === 'local'">
        <label class="field field--wide">
          <span class="field__label">{{ t('sources.path') }}</span>
          <input v-model="form.path" class="input" type="text" :placeholder="t('sources.pathPlaceholder')" />
        </label>
      </template>

      <template v-else-if="form.kind === 'webdav'">
        <label class="field field--wide">
          <span class="field__label">{{ t('sources.url') }}</span>
          <input v-model="form.url" class="input" type="url" inputmode="url" :placeholder="t('sources.urlPlaceholder')" />
        </label>
        <label class="field">
          <span class="field__label">{{ t('sources.username') }}</span>
          <input v-model="form.username" class="input" type="text" autocomplete="username" />
        </label>
        <label class="field">
          <span class="field__label">{{ t('sources.password') }}</span>
          <input
            v-model="form.password"
            class="input"
            type="password"
            autocomplete="new-password"
            :placeholder="passwordPlaceholder"
          />
        </label>
        <label class="field field--check field--wide">
          <input v-model="form.insecure_tls" type="checkbox" />
          <span>{{ t('sources.insecureTLS') }}</span>
        </label>
      </template>

      <template v-else>
        <label class="field">
          <span class="field__label">{{ t('sources.host') }}</span>
          <input v-model="form.host" class="input" type="text" :placeholder="t('sources.hostPlaceholder')" />
        </label>
        <label class="field field--port">
          <span class="field__label">{{ t('sources.port') }}</span>
          <input v-model="form.port" class="input" type="number" inputmode="numeric" min="1" max="65535" placeholder="445" />
        </label>
        <label class="field">
          <span class="field__label">{{ t('sources.share') }}</span>
          <input v-model="form.share" class="input" type="text" :placeholder="t('sources.sharePlaceholder')" />
        </label>
        <label class="field">
          <span class="field__label">{{ t('sources.basePath') }}</span>
          <input v-model="form.base_path" class="input" type="text" :placeholder="t('sources.basePathPlaceholder')" />
        </label>
        <label class="field">
          <span class="field__label">{{ t('sources.username') }}</span>
          <input v-model="form.username" class="input" type="text" autocomplete="username" />
        </label>
        <label class="field">
          <span class="field__label">{{ t('sources.password') }}</span>
          <input
            v-model="form.password"
            class="input"
            type="password"
            autocomplete="new-password"
            :placeholder="passwordPlaceholder"
          />
        </label>
        <label class="field">
          <span class="field__label">{{ t('sources.domain') }}</span>
          <input v-model="form.domain" class="input" type="text" :placeholder="t('sources.domainPlaceholder')" />
        </label>
      </template>
    </div>

    <label v-if="isLogin" class="field field--check source-form__autoload">
      <input v-model="autoLoad" type="checkbox" />
      <span>{{ t('login.openAsCourse') }}</span>
    </label>

    <p v-if="formError" class="source-form__error">{{ formError }}</p>

    <div class="source-form__actions">
      <button type="button" class="btn btn--ghost btn--sm" :disabled="testing || saving" @click="testForm">
        {{ testing ? t('sources.testing') : t('sources.test') }}
      </button>
      <button type="submit" class="btn btn--sm" :disabled="saving || testing">
        {{ submitLabel }}
      </button>
      <button
        v-if="showCancel"
        type="button"
        class="btn btn--ghost btn--sm"
        :disabled="saving || testing"
        @click="emit('cancel')"
      >
        {{ t('common.cancel') }}
      </button>
      <span class="faint source-form__hint">{{ t('sources.saveHint') }}</span>
    </div>
  </form>
</template>

<style scoped>
.source-form {
  margin-top: 18px;
  padding-top: 18px;
  border-top: 1px solid var(--border);
}

.source-form__grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}

.field--wide {
  grid-column: 1 / -1;
}

.field--port {
  max-width: 140px;
}

.field__label {
  font-size: 0.8rem;
  font-weight: 600;
  color: var(--text-muted);
}

.field--check {
  flex-direction: row;
  align-items: center;
  gap: 9px;
  font-size: 0.88rem;
  color: var(--text-muted);
}

.field--check input {
  width: 18px;
  height: 18px;
  accent-color: var(--accent);
  flex: none;
}

.source-form__autoload {
  margin-top: 16px;
}

.source-form__error {
  margin-top: 12px;
  color: var(--danger);
  font-size: 0.86rem;
}

.source-form__actions {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 16px;
  flex-wrap: wrap;
}

@media (max-width: 640px) {
  .source-form__grid {
    grid-template-columns: 1fr;
  }

  .field--port {
    max-width: none;
  }

  .source-form__actions .btn {
    flex: 1 1 auto;
  }

  .source-form__hint {
    flex-basis: 100%;
  }
}
</style>
