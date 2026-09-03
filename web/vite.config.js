import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// Vite 配置
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url))
    }
  },
  server: {
    port: 5173,
    // 开发期代理到 Go 后端，避免跨域
    proxy: {
      '/api': { target: 'http://localhost:9191', changeOrigin: true },
      '/ws': { target: 'ws://localhost:9191', ws: true }
    }
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // 代码分割：UI 库与业务代码分离，业务迭代不失效浏览器缓存
    rollupOptions: {
      output: {
        manualChunks: {
          'element-plus': ['element-plus', '@element-plus/icons-vue'],
          'vendor': ['vue', 'vue-router', 'axios']
        }
      }
    }
  }
})