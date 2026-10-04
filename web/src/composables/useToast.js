import { reactive, readonly } from 'vue'

const state = reactive({ items: [] })
let nextId = 1

// Toasts are queued from anywhere in the app; <ToastStack /> renders them.
export function useToast() {
  function push(message, type = 'info', timeout = 3200) {
    const id = nextId++
    state.items.push({ id, message, type })
    if (timeout > 0) {
      window.setTimeout(() => dismiss(id), timeout)
    }
    return id
  }

  function dismiss(id) {
    const index = state.items.findIndex((item) => item.id === id)
    if (index !== -1) state.items.splice(index, 1)
  }

  return {
    toasts: readonly(state),
    push,
    success: (message) => push(message, 'success'),
    error: (message) => push(message, 'error', 4600),
    info: (message) => push(message, 'info'),
    dismiss
  }
}
