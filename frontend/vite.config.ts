import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Proxy /api → Go server so the frontend code only needs same-origin
// fetches and we sidestep CORS in dev. The Go server keeps its own
// CORS headers as a backup for production / different origins.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
})