import { defineConfig, loadEnv } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '');
  const basePath = env.VITE_BASE_PATH || '/';
  
  return {
    plugins: [react()],
    base: basePath, // Reads from VITE_BASE_PATH env var for asset paths
    build: {
      outDir: 'dist',
      assetsDir: 'assets',
    },
    server: {
      port: 5174,
      proxy: {
        // Proxy API requests to NornicDB server
        '/api': {
          target: 'http://localhost:7475',
          changeOrigin: true,
          rewrite: (path) => path.replace(/^\/api/, ''),
        },
        '/db': {
          target: 'http://localhost:7475',
          changeOrigin: true,
        },
        '/auth': {
          target: 'http://localhost:7475',
          changeOrigin: true,
        },
        '/nornicdb': {
          target: 'http://localhost:7475',
          changeOrigin: true,
        },
        '/admin': {
          target: 'http://localhost:7475',
          changeOrigin: true,
        },
      },
    },
  };
});
