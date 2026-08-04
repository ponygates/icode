import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  base: './',
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    rollupOptions: {
      output: {
        manualChunks: {
          react: ['react', 'react-dom', 'react-router-dom', 'zustand', '@tanstack/react-virtual'],
          i18n: ['i18next', 'react-i18next'],
          markdown: ['highlight.js'],
        },
      },
    },
  },
  server: {
    port: 5173,
    strictPort: true,
  },
});
