import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { resolve } from 'path'

// https://vite.dev/config/
export default defineConfig({
  plugins: [
    react()
  ],
  server: {
    port: 5274,
    proxy: {
      '^/api(?:/|$)': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/api(?=\/|$)/, ''),
      },
    },
  },
  css: {
    preprocessorOptions: {
      less: {
        javascriptEnabled: true
      }
    },
  },
  resolve: {
    alias: {
      '@': resolve(__dirname, 'src'),
      '~': resolve(__dirname, './'),
    }
  },
  build: {
    manifest: true,
    rollupOptions: {
      output: {
        sourcemap: false
      }
    }
  }
})
