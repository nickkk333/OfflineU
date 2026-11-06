<script setup>
// "Login" in OfflineU means connecting to a course source: there are no user
// accounts, the WebDAV / SMB / folder credentials belong to the server. This
// page is the mobile-friendly entry point used directly in the browser and
// linked from the course picker and dashboard.
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import SourceForm from '../components/SourceForm.vue'
import LanguageSwitch from '../components/LanguageSwitch.vue'
import { api } from '../api.js'
import { refreshState, store } from '../store.js'
import { t } from '../i18n.js'
import { useToast } from '../composables/useToast.js'

const router = useRouter()
const toast = useToast()
const KINDS = ['webdav', 'smb', 'local']
const kind = ref('webdav')
const opening = ref('')

onMounted(() => {
  if (!store.loaded) refreshState()
})

async function onConnected() {
  try {
    // The source (and optionally the course) changed on the server; the home
    // view reads the same store, so it must see the new state first.
    await refreshState()
  } finally {
    await router.push('/')
  }
}

async function openExisting(source) {
  if (opening.value) return
  opening.value = source.id
  try {
    await api.testSource({ id: source.id })
    await refreshState()
    await router.push('/')
  } catch (error) {
    toast.error(error.message)
  } finally {
    opening.value = ''
  }
}

function skip() {
  router.push('/')
}

function targetOf(source) {
  if (source.kind === 'local') return source.path || ''
  if (source.kind === 'webdav') return source.url || ''
  return `\\\\${source.host || ''}\\${source.share || ''}`
}
</script>

<template>
  <div class="shell login">
    <header class="brand">
      <div class="brand__logo" aria-hidden="true">◈</div>
      <div class="brand__text">
        <h1 class="brand__name">OfflineU</h1>
        <p class="faint">{{ t('app.tagline') }}</p>
      </div>
      <span class="spacer"></span>
      <LanguageSwitch />
    </header>

    <div class="login__grid">
      <section class="card login__card">
        <div class="section-title">{{ t('login.title') }}</div>
        <p class="card__hint">{{ t('login.hint') }}</p>

        <div class="login__kinds" role="tablist" :aria-label="t('sources.kindLabel')">
          <button
            v-for="item in KINDS"
            :key="item"
            type="button"
            role="tab"
            class="btn btn--ghost btn--sm login__kind"
            :class="{ 'login__kind--active': kind === item }"
            :aria-selected="kind === item"
            @click="kind = item"
          >
            {{ t(`sources.kind.${item}`) }}
          </button>
        </div>

        <SourceForm :key="kind" mode="login" :initial-kind="kind" @saved="onConnected" />

        <p class="login__note faint">{{ t('login.note') }}</p>
      </section>

      <aside class="card login__aside">
        <div class="section-title">{{ t('login.existingTitle') }}</div>
        <p class="card__hint">{{ t('login.existingHint') }}</p>
        <p v-if="!store.sources.length" class="faint login__empty">{{ t('sources.empty') }}</p>
        <div v-else class="login__sources">
          <div v-for="source in store.sources" :key="source.id" class="login__source">
            <span class="badge">{{ t(`sources.kind.${source.kind}`) }}</span>
            <div class="login__source-info">
              <span class="login__source-name">{{ source.name }}</span>
              <span class="login__source-target mono">{{ targetOf(source) }}</span>
            </div>
            <button
              type="button"
              class="btn btn--ghost btn--sm"
              :disabled="Boolean(opening)"
              @click="openExisting(source)"
            >
              {{ opening === source.id ? t('sources.testing') : t('common.open') }}
            </button>
          </div>
        </div>
        <button type="button" class="btn btn--ghost login__skip" @click="skip">
          {{ t('login.skip') }}
        </button>
      </aside>
    </div>
  </div>
</template>

<style scoped>
.login {
  max-width: 1000px;
  padding-top: 28px;
  display: flex;
  flex-direction: column;
  gap: 22px;
}

.brand {
  display: flex;
  align-items: center;
  gap: 14px;
}

.brand__text {
  min-width: 0;
}

.brand__logo {
  display: grid;
  place-items: center;
  width: 46px;
  height: 46px;
  border-radius: 14px;
  background: var(--gradient);
  color: #fff;
  font-size: 1.35rem;
  box-shadow: 0 12px 30px rgba(99, 102, 241, 0.4);
  flex: none;
}

.brand__name {
  font-size: 1.45rem;
  font-weight: 700;
  letter-spacing: -0.02em;
}

.login__grid {
  display: grid;
  grid-template-columns: minmax(0, 1.4fr) minmax(260px, 1fr);
  gap: 18px;
  align-items: start;
}

.login__kinds {
  display: flex;
  gap: 8px;
  margin-top: 18px;
  flex-wrap: wrap;
}

.login__kind--active {
  border-color: var(--accent);
  color: var(--text);
  background: rgba(99, 102, 241, 0.16);
}

.login__note {
  margin-top: 16px;
  line-height: 1.6;
}

.login__empty {
  margin-top: 14px;
}

.login__sources {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin-top: 16px;
}

.login__source {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: rgba(8, 12, 22, 0.4);
}

.login__source-info {
  display: flex;
  flex-direction: column;
  min-width: 0;
  flex: 1;
}

.login__source-name {
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.login__source-target {
  color: var(--text-faint);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.login__skip {
  width: 100%;
  margin-top: 18px;
}

@media (max-width: 860px) {
  .login__grid {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 560px) {
  .login {
    padding-top: max(16px, env(safe-area-inset-top, 0px));
    gap: 16px;
  }

  .brand {
    gap: 10px;
    flex-wrap: wrap;
  }

  .brand__logo {
    width: 38px;
    height: 38px;
    border-radius: 11px;
    font-size: 1.1rem;
  }

  .brand__name {
    font-size: 1.2rem;
  }

  .brand .faint {
    font-size: 0.76rem;
    line-height: 1.4;
  }

  .login__kinds {
    gap: 6px;
  }

  .login__kind {
    flex: 1 1 auto;
  }

  .login__source {
    flex-wrap: wrap;
  }

  .login__source-info {
    flex-basis: calc(100% - 90px);
  }
}

@media (max-width: 380px) {
  .login__source-info {
    flex-basis: 100%;
  }
}
</style>
