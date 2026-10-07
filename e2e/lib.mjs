// Shared helpers for the OfflineU E2E scenarios (api / browser / cast).
import os from 'node:os'

export const results = []

// check records one assertion and prints it right away.
export function check(name, ok, detail = '') {
  results.push({ name, ok: Boolean(ok), detail })
  console.log(`${ok ? '  ok  ' : ' FAIL '} ${name}${detail ? ` -- ${detail}` : ''}`)
}

export function note(message) {
  console.log(`  ..   ${message}`)
}

// waitFor polls probe() until it returns something truthy. A probe that throws
// (connection not up yet) counts as "not ready" instead of aborting the wait.
export async function waitFor(description, probe, { timeout = 20000, interval = 300 } = {}) {
  const deadline = Date.now() + timeout
  let last
  let lastError
  while (Date.now() < deadline) {
    try {
      last = await probe()
      lastError = null
      if (last) return last
    } catch (error) {
      last = null
      lastError = error
    }
    await new Promise((resolve) => setTimeout(resolve, interval))
  }
  const detail = lastError ? `lastError=${lastError.message}` : `last=${JSON.stringify(last)}`
  throw new Error(`timeout waiting for ${description} (${detail})`)
}

// request performs one HTTP call and returns status plus parsed JSON (or text).
export async function request(base, path, { method = 'GET', body } = {}) {
  const response = await fetch(base + path, {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : {},
    body: body ? JSON.stringify(body) : undefined,
  })
  const text = await response.text()
  let json = null
  if (text) {
    try {
      json = JSON.parse(text)
    } catch {
      json = null
    }
  }
  return { status: response.status, ok: response.ok, text, json }
}

// collectLessons flattens the course tree from /api/state.
export function collectLessons(node, out = []) {
  if (!node) return out
  if (Array.isArray(node.lessons)) out.push(...node.lessons)
  for (const child of node.children || []) collectLessons(child, out)
  return out
}

// lessonRoute builds the SPA route of a lesson ("/lesson/Section%201/...").
export function lessonRoute(lesson) {
  return `/lesson/${lesson.url.split('/').map(encodeURIComponent).join('/')}`
}

// lanAddress is the host IP a cast device (or a container) can reach us at.
export function lanAddress() {
  for (const entries of Object.values(os.networkInterfaces())) {
    for (const entry of entries || []) {
      if ((entry.family === 'IPv4' || entry.family === 4) && !entry.internal) return entry.address
    }
  }
  return '127.0.0.1'
}

// summary prints the tally and sets a non-zero exit code on any failure.
export function summary(label) {
  const failed = results.filter((item) => !item.ok)
  console.log(`\n${label}: ${results.length - failed.length}/${results.length} passed`)
  for (const item of failed) console.log(`  FAILED: ${item.name}${item.detail ? ` -- ${item.detail}` : ''}`)
  if (failed.length) process.exitCode = 1
}
