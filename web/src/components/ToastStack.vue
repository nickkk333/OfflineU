<script setup>
import { useToast } from '../composables/useToast.js'

const { toasts, dismiss } = useToast()

const ICONS = { success: '✓', error: '!', info: 'i' }
</script>

<template>
  <Teleport to="body">
    <div class="toasts">
      <TransitionGroup name="toast">
        <div
          v-for="toast in toasts.items"
          :key="toast.id"
          class="toast"
          :class="`toast--${toast.type}`"
          role="status"
          @click="dismiss(toast.id)"
        >
          <span class="toast__icon">{{ ICONS[toast.type] || ICONS.info }}</span>
          <span class="toast__text">{{ toast.message }}</span>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<style scoped>
.toasts {
  position: fixed;
  top: 18px;
  right: 18px;
  z-index: 9999;
  display: flex;
  flex-direction: column;
  gap: 10px;
  max-width: min(380px, calc(100vw - 36px));
}

.toast {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 13px 16px;
  border-radius: 14px;
  border: 1px solid var(--border);
  background: rgba(17, 26, 46, 0.92);
  backdrop-filter: blur(14px);
  box-shadow: var(--shadow);
  cursor: pointer;
  font-size: 0.9rem;
}

.toast__icon {
  display: grid;
  place-items: center;
  width: 22px;
  height: 22px;
  flex: none;
  border-radius: 999px;
  font-size: 0.78rem;
  font-weight: 700;
  color: #0b1020;
  background: var(--accent-3);
}

.toast--success {
  border-color: rgba(52, 211, 153, 0.4);
}

.toast--success .toast__icon {
  background: var(--success);
}

.toast--error {
  border-color: rgba(248, 113, 113, 0.45);
}

.toast--error .toast__icon {
  background: var(--danger);
}

.toast-enter-active,
.toast-leave-active {
  transition: all 260ms cubic-bezier(0.4, 0, 0.2, 1);
}

.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateX(30px) scale(0.96);
}
</style>
