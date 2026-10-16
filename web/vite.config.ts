import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwind from '@tailwindcss/vite'

// Embedded in the Go binary, so self-contained and served from the root; dev proxies /api to the server.
export default defineConfig({
  plugins: [react(), tailwind()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // The two large chunks (hls.js, Anime4K shaders) load on demand, outside the first paint.
    chunkSizeWarningLimit: 3600,
  },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://127.0.0.1:4321',
      '/callback': 'http://127.0.0.1:4321',
      '/mal': 'http://127.0.0.1:4321',
    },
  },
})
