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
      },
      // Vite tests this regex against req.url (query string included), so the
      // real callback /signin-oidc?code=...&state=... must match too.
      '^/signin-oidc(?:[/?]|$)': {
        target: 'http://localhost:8080',
        changeOrigin: true,
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
