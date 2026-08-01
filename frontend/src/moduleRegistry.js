// Frontend module registry — maps module ID (as reported by
// /api/modules) to the React component that renders its tab content.
//
// Why static? Vite tree-shakes unused imports per entry, but it cannot
// statically discover "modules ship their own bundles" without help.
// Listing modules here keeps the build self-contained while still
// letting the *runtime* decide which tabs to show (a missing
// [section] in obs_viewer.conf removes the manifest entry → the tab
// is not rendered regardless of what's in this file).
//
// No modules ship with obs-viewer by default. To add one:
//   1. Place its tab component under modules/<name>/frontend/.
//   2. Add one import + one map entry below.
//   3. Have the backend module surface a `Tab` field in its manifest.
//   4. Add an alias for any external dep it imports in vite.config.js —
//      module JSX lives outside the Vite project root, so Rollup's
//      node_modules walk-up never reaches frontend/node_modules.
//
// See docs/MODULES.md for the full contract.

export const moduleComponents = {};
