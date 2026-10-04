import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// The dev server proxies everything that the Go backend owns, so `npm run dev`
// gives instant hot reload while `go run .` keeps serving the real API.
const backend = 'http://127.0.0.1:5000'

export default defineConfig({
  plugins: [vue()],
  base: '/',
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    assetsDir: 'assets',
    chunkSizeWarningLimit: 900
  },
  server: {
    port: 5173,
    proxy: {
      '/api': backend,
      '/files': backend,
      '/subtitles': backend,
      '/browse': backend,
      '/load_course': backend,
      '/reset_course': backend,
      '/forget_course': backend,
      '/health': backend
    }
  }
})