// The cast running on a TV, shared by every page: the lesson view follows it,
// the dashboard shows it. One poll for the whole app - the server keeps a
// single session anyway, and two components watching it must not ask twice.
import { onBeforeUnmount, ref } from 'vue'
import { api } from '../api.js'

const POLL_MS = 1500

const cast = ref(null)
let timer = 0
let users = 0

async function refreshCast() {
  try {
    const data = await api.castSession()
    cast.value = data && data.active ? data : null
  } catch {
    cast.value = null
  }
}

export function useCast() {
  users += 1
  if (!timer) {
    refreshCast()
    timer = window.setInterval(refreshCast, POLL_MS)
  }
  onBeforeUnmount(() => {
    users -= 1
    if (users <= 0 && timer) {
      window.clearInterval(timer)
      timer = 0
    }
  })
  return { cast, refreshCast }
}
