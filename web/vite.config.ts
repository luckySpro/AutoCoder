import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  server: {
    // 开发时把 API 转发给 `go run ./cmd/autocoder`
    proxy: { '/api': 'http://127.0.0.1:7788' },
  },
})
