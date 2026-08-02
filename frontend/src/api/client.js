const BASE = '';

async function request(path, options = {}) {
  const resp = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...options,
  });
  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ error: resp.statusText }));
    const e = new Error(err.error || 'Request failed');
    // Carry the status through. Most callers only want the message, but
    // a few need to branch — a 409 from the rule save means "already
    // exists", which the UI resolves by offering to replace rather than
    // by reporting a failure.
    e.status = resp.status;
    e.conflict = resp.status === 409;
    throw e;
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

  // Why did this file parse the way it did — every adapter's score and
  // reason, the first line as the parser saw it, and any encoding trait
  // that breaks matching invisibly. Read-only; loads nothing.
  explainFile: (path) =>
    request('/api/files/explain', {
      method: 'POST',
      body: JSON.stringify({ path }),
    }),

  getRules: () => request('/api/rules'),

  suggestRule: (sample) =>
    request('/api/rules/suggest', {
      method: 'POST',
      body: JSON.stringify({ sample }),
    }),

  previewRule: (rule, sample) =>
    request('/api/rules/preview', {
      method: 'POST',
      body: JSON.stringify({ rule, sample }),
    }),

  // Resolves to {status, path, file, rules}. Rejects with a `.conflict`
  // flag when a rule of that name already exists, which is the one
  // failure the caller can resolve by asking whether to replace.
  saveRule: (rule, overwrite = false) =>
    request('/api/rules/save', {
      method: 'POST',
      body: JSON.stringify({ rule, overwrite }),
    }),

  // Modules add their own namespaced helpers here, mirroring the
  // /api/<module>/* routes their backend registers. See docs/MODULES.md.
};
