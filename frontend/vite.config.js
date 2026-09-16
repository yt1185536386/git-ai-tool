import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  server: {
    // 固定端口，供 wails dev 的 frontend:dev:serverUrl 自动探测
    port: 5173,
    strictPort: true
  },
  build: {
    outDir: 'dist'
  }
})
