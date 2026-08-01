import { defineConfig } from 'vite'
import path from 'node:path'
import react from '@vitejs/plugin-react'

// Module files live outside `frontend/` (under `../modules/<name>/frontend/`)
// so they don't have a sibling node_modules to resolve into. Rollup's
// default walk-up never reaches `frontend/node_modules`. Telling Vite
// to use this absolute node_modules path makes imports from module
// JSX resolve the same packages the in-tree frontend uses.
const NODE_MODULES = path.resolve(__dirname, 'node_modules')

export default defineConfig({
  plugins: [react()],
  resolve: {
    // Explicit alias prefixes for every external dep a module file may
    // need. Catches the cases where Vite's prebundling doesn't pick up
    // a path outside the project root. Add an entry here for each
    // package your module imports — without it the build fails to
    // resolve the import even though the package is installed.
    alias: {
      '@radix-ui/react-dialog': path.resolve(NODE_MODULES, '@radix-ui/react-dialog'),
      '@radix-ui/react-popover': path.resolve(NODE_MODULES, '@radix-ui/react-popover'),
      '@tanstack/react-query': path.resolve(NODE_MODULES, '@tanstack/react-query'),
    },
  },
  server: {
    fs: {
      // Allow Vite dev server to read source files outside the
      // frontend directory (i.e. modules/<name>/frontend/*).
      allow: [path.resolve(__dirname, '..')],
    },
    proxy: {
      '/api': 'http://localhost:9200',
    },
  },
  build: {
    outDir: '../static',
    emptyOutDir: true,
  },
})
