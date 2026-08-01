const BASE = '';

async function request(path, options = {}) {
  const resp = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...options,
  });
  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ error: resp.statusText }));
    throw new Error(err.error || 'Request failed');
  }
  return resp.json();
}

export const api = {
  getVersion: () => request('/api/version'),

  getFiles: () => request('/api/files'),

  loadFile: (path) =>
    request('/api/files/load', {
      method: 'POST',
      body: JSON.stringify({ path }),
    }),

  unloadFile: (id) =>
    request('/api/files/unload', {
      method: 'POST',
      body: JSON.stringify({ id }),
    }),

  toggleFile: (id, enabled) =>
    request('/api/files/toggle', {
      method: 'POST',
      body: JSON.stringify({ id, enabled }),
    }),

  browse: (path) =>
    request(`/api/browse?path=${encodeURIComponent(path || '')}`),

  query: (params) =>
    request('/api/query', {
      method: 'POST',
      body: JSON.stringify(params),
    }),

  getFields: () => request('/api/fields'),

  getTimeRange: () => request('/api/timerange'),

  getTimestampField: () => request('/api/timestamp-field'),

  setTimestampField: (field) =>
    request('/api/timestamp-field', {
      method: 'POST',
      body: JSON.stringify({ field }),
    }),

  exportData: (query, outputPath) =>
    request('/api/export', {
      method: 'POST',
      body: JSON.stringify({ query, output_path: outputPath }),
    }),

  selfCopy: (targetDir) =>
    request('/api/export/self-copy', {
      method: 'POST',
      body: JSON.stringify({ target_dir: targetDir }),
    }),

  getSettings: () => request('/api/settings'),

  saveSettings: (settings) =>
    request('/api/settings', {
      method: 'POST',
      body: JSON.stringify(settings),
    }),

  loadDir: (path) =>
    request('/api/files/load-dir', {
      method: 'POST',
      body: JSON.stringify({ path }),
    }),

  // Modules add their own namespaced helpers here, mirroring the
  // /api/<module>/* routes their backend registers. See docs/MODULES.md.
};
