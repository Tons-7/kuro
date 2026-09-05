import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwind from '@tailwindcss/vite'

// The built bundle is embedded into the Go binary, so it must be self-contained
// and served from the root. In development everything under /api is proxied to
// the running server instead.
export default defineConfig({
  plugins: [react(), tailwind()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // hls.js and the subtitle renderer are only reachable from the player
    // route, so lazy loading already keeps them out of the first paint.
    // The two large chunks are deliberate and load on demand: hls.js with the
    // player, and the Anime4K shader source only if upscaling is switched on.
    // Neither is in the first paint, so the warning is not actionable.
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
