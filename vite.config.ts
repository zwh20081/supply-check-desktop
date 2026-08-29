import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  clearScreen: false,
  server: {
    strictPort: true,
    host: '127.0.0.1',
    watch: {
      // cargo 正在写 target/ 里的 dll，Windows 上文件带独占锁，
      // Vite 的 watcher 一碰就抛 EBUSY 并整个崩掉。Rust 侧的重编译由
      // tauri dev 自己的 watcher 负责，这里不需要看。
      ignored: ['**/src-tauri/**', '**/sidecar/**'],
    },
  },
  envPrefix: ['VITE_', 'TAURI_ENV_*'],
  build: {
    target: process.env.TAURI_ENV_PLATFORM === 'windows' ? 'chrome105' : 'safari13',
    minify: process.env.TAURI_ENV_DEBUG ? false : 'esbuild',
    sourcemap: Boolean(process.env.TAURI_ENV_DEBUG),
  },
});
