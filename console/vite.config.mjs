// SPDX-License-Identifier: Apache-2.0
import { defineConfig } from 'vite';
import UnoCSS from 'unocss/vite';
import { presetWind3 } from 'unocss';
export default defineConfig({
  // Vite's esbuild JSX transform avoids Babel's non-allowlisted data license and
  // keeps all build-time dependencies under the same permissive license gate.
  base: '/console/', esbuild: { jsx: 'automatic' }, plugins: [UnoCSS({ presets: [presetWind3()] })],
  // Keep the browser's Host/Origin pair intact for the gateway's same-origin
  // console protection. Only the destination socket changes in development.
  server: { port: 5173, proxy: { '/api': { target: process.env.NMG_DEV_PROXY ?? 'http://127.0.0.1:8789', changeOrigin: false }, '/nomifun': { target: process.env.NMG_DEV_PROXY ?? 'http://127.0.0.1:8789', changeOrigin: false } } },
  build: { sourcemap: false, chunkSizeWarningLimit: 1500, rollupOptions: {
    onwarn(warning, warn) {
      // This entry is entirely client-rendered. These dependency RSC markers
      // are unused here; keep all other bundler diagnostics visible.
      if (warning.code === 'MODULE_LEVEL_DIRECTIVE' && warning.message.includes('use client') && /node_modules[\\/](lucide-react|framer-motion)[\\/]/.test(warning.id ?? '')) return;
      warn(warning);
    }
  } }
});
