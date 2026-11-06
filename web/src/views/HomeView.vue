<script setup>
import { computed, onMounted } from 'vue'
import { RouterLink } from 'vue-router'
import { refreshState, store } from '../store.js'
import { t } from '../i18n.js'
import CoursePicker from './CoursePicker.vue'
import DashboardView from './DashboardView.vue'
import LanguageSwitch from '../components/LanguageSwitch.vue'
import CastBar from '../components/CastBar.vue'
import { useCast } from '../composables/useCast.js'

const { cast, refreshCast } = useCast()

// A release reports a number (2.0.3) and gets the usual "v" prefix; a
// development build reports "latest", which would read as "vlatest".
const versionLabel = computed(() => {
  const version = String(store.version || '').trim()
  if (!version) return ''
  return /^\d/.test(version) ? `v${version}` : version
})

onMounted(() => {
  if (!store.loaded) refreshState()
})
</script>

<template>
  <div class="shell home">
    <header class="brand">
      <div class="brand__logo" aria-hidden="true">◈</div>
      <div>
        <h1 class="brand__name">OfflineU</h1>
        <p class="faint">{{ t('app.tagline') }}</p>
      </div>
      <span class="spacer"></span>
      <span v-if="versionLabel" class="badge">{{ versionLabel }}</span>
      <RouterLink to="/login" class="btn btn--ghost btn--sm home__sources" :title="t('login.title')">
        <span aria-hidden="true">🔗</span>
        <span class="home__sources-label">{{ t('login.shortTitle') }}</span>
      </RouterLink>
      <LanguageSwitch />
    </header>

    <CastBar
      v-if="cast"
      class="home__cast"
      :session="cast"
      show-open
      @refresh="refreshCast"
      @ended="refreshCast"
    />

    <div v-if="!store.loaded" class="card empty-state">
      <div class="empty-state__icon"><span class="spinner"></span></div>
      {{ t('home.loadingLibrary') }}
    </div>

    <div v-else-if="store.error" class="card empty-state">
      <div class="empty-state__icon">⚠️</div>
      <p>{{ store.error }}</p>
      <button type="button" class="btn btn--ghost" style="margin-top: 14px" @click="refreshState()">
        {{ t('common.tryAgain') }}
      </button>
    </div>

    <DashboardView v-else-if="store.course" />
    <CoursePicker v-else />
  </div>
</template>

<style scoped>
.home {
  padding-top: 28px;
  display: flex;
  flex-direction: column;
  gap: 22px;
}

.home__cast {
  margin-bottom: -6px;
}

.home__sources {
  gap: 7px;
  flex: none;
  text-decoration: none;
}

.brand {
  display: flex;
  align-items: center;
  gap: 14px;
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
}

.brand__name {
  font-size: 1.45rem;
  font-weight: 700;
  letter-spacing: -0.02em;
}

@media (max-width: 560px) {
  .home {
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

  /* Keep the header on one line on narrow phones: the icon alone is enough. */
  .home__sources-label {
    display: none;
  }

  .home__sources {
    padding: 8px 10px;
  }
}
</style>
