// Frontend OpenAPI client generation.
//
// Source of truth is the swagger.json that `swag init` writes from the
// Go handler annotations. The generated TS client + types land in
// src/api/generated/ and are tree-shakable; existing hand-written
// `api.*` calls keep working until they're migrated component-by-component.
//
// Run:
//   npm run gen:api
//
// Wired into the build via the prebuild script so a normal `npm run build`
// regenerates the client before Vite bundles.
export default {
  input: '../internal/server/docs/swagger.json',
  output: {
    path: 'src/api/generated',
    clean: true,
  },
  plugins: [
    '@hey-api/client-fetch',
    '@hey-api/typescript',
    '@hey-api/sdk',
  ],
};
