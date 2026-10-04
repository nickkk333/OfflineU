// Vite empties dist/ on every build, which would also delete the placeholder
// that keeps the directory in git (the Go embed directive needs it to exist).
// This tiny script recreates it after each build.
import { writeFileSync, mkdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const dist = join(here, '..', 'dist')

mkdirSync(dist, { recursive: true })
writeFileSync(
  join(dist, '.gitkeep'),
  'The compiled Vue frontend is written into this directory by "npm run build".\n' +
    'Everything except this placeholder is git-ignored, but the directory itself\n' +
    'must exist so that the //go:embed directive in main.go can resolve.\n'
)
console.log('web/dist/.gitkeep restored')
