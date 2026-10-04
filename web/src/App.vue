<script setup>
import ToastStack from './components/ToastStack.vue'
</script>

<template>
  <div class="app">
    <RouterView v-slot="{ Component }">
      <Transition name="view" mode="out-in">
        <component :is="Component" />
      </Transition>
    </RouterView>
    <ToastStack />
  </div>
</template>

<style scoped>
.app {
  min-height: 100vh;
  padding-bottom: 60px;
}

.view-enter-active,
.view-leave-active {
  transition: opacity 200ms ease, transform 200ms ease;
}

.view-enter-from {
  opacity: 0;
  transform: translateY(8px);
}

.view-leave-to {
  opacity: 0;
  transform: translateY(-6px);
}
</style>
