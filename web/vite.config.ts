import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  // 相对路径：既能内嵌到单文件程序，也能作为静态页面直接打开
  base: './',
  plugins: [vue()],
  server: {
    // 开发时把 API 转发给 `go run ./cmd/autocoder`
    proxy: { '/api': 'http://127.0.0.1:7788' },
  },
})
