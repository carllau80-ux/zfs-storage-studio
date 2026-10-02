import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    proxy: { '/api': 'http://127.0.0.1:8080' }
  },
  build: {
    chunkSizeWarningLimit: 2000,
    rollupOptions: { output: { inlineDynamicImports: true } } // 单包输出,避免动态 import 兼容问题
  }
})
