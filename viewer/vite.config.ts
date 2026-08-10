/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// Assets are served from the Go binary's embed.FS at the server root, and
// loaded with relative paths so the same bundle works from a file:// URL if a
// self-contained export is ever wanted.
export default defineConfig({
  base: './',
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': new URL('./src', import.meta.url).pathname },
  },
  test: {
    // Everything under test is pure: the derivation from a run file and the
    // zoom arithmetic. Neither needs a DOM, and asking for one would make the
    // suite slower and its failures less direct.
    environment: 'node',
    include: ['src/**/*.test.ts'],
  },
})
