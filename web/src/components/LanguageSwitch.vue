<script setup>
import { computed } from 'vue'
import { LOCALES, locale, t, toggleLocale } from '../i18n.js'
import { refreshState, store } from '../store.js'

// The button always offers the language you are *not* reading right now.
const target = computed(() => LOCALES.find((item) => item.code !== locale.value) || LOCALES[0])

function switchLanguage() {
  toggleLocale()
  // Strings the server generated (the mount hint) follow the Accept-Language
  // header, so pull a fresh copy when they are already on screen.
  if (store.loaded) refreshState()
}
</script>

<template>
  <button
    type="button"
    class="btn btn--ghost btn--sm lang"
    :title="t('lang.switchTo', { label: target.label })"
    @click="switchLanguage()"
  >
    <span aria-hidden="true">🌐</span>
    <span class="mono">{{ target.short }}</span>
  </button>
</template>

<style scoped>
.lang {
  gap: 7px;
  flex: none;
}
</style>
