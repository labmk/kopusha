import { test, expect } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

// Per-data-type ingestion smoke. One test per REQ-DT-NN in REQUIREMENTS.md.
//
// Each test:
//   1. POSTs /api/files/load with the absolute path of a fixture file
//   2. Reads /api/files, finds the loaded entry, asserts records >= 1
//      (EVTX has a separate path because the captured Application.evtx
//      can in principle be empty — the assertion there is on
//      `_source_format`, not row count)
//   3. POSTs /api/query (offset 0, limit 1) and asserts the first row
//      carries the expected `_source_format` value — proves the
//      dispatcher routed to the right adapter and the record post-
//      processing wrote the marker
//   4. POSTs /api/files/unload so subsequent tests start from a
//      known-empty engine state
//
// Tests run sequentially (worker=1 from playwright.config.js).

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const PROJECT_ROOT = path.resolve(__dirname, '..', '..');
const FIX = path.join(PROJECT_ROOT, 'test-fixtures', 'formats');

// Helper: load a file AND make it the only enabled file in the engine.
// `firstRow` queries UNION ALL across every enabled file, so without
// this step it could return a row from a pre-loaded NDJSON instead of
// the just-loaded fixture (the dispatcher's adapter is what we want to
// verify, not the union semantics). The `beforeEach`/`afterAll` hooks
// flip the pre-loaded files back on so other suites are unaffected.
async function load(request, fixture) {
  const fullPath = path.join(FIX, fixture);
  const r = await request.post('/api/files/load', { data: { path: fullPath } });
  expect(r.ok(), `load ${fixture}`).toBeTruthy();
  const list = await request.get('/api/files');
  const data = await list.json();
  // Disable every file that isn't the fixture we just loaded.
  for (const f of data.files) {
    if (f.name !== fixture) {
      await request.post('/api/files/toggle', { data: { id: f.id, enabled: false } });
    }
  }
  const info = data.files.find((f) => f.name === fixture);
  expect(info, `file info for ${fixture}`).toBeTruthy();
  return info;
}

async function firstRow(request) {
  const r = await request.post('/api/query', { data: { offset: 0, limit: 1 } });
  expect(r.ok(), 'query').toBeTruthy();
  const data = await r.json();
  expect(data.rows, 'query rows').toBeDefined();
  return data.rows[0] || null;
}

// Unload only the files THIS spec loaded — by name. The webServer
// pre-loads gateway.ndjson + indexer.ndjson from test-fixtures/ndjson/
// for smoke.spec.js; an indiscriminate unloadAll would wipe those and
// break the other suite when Playwright runs both.
const FIXTURE_NAMES = new Set([
  'sample.ndjson',
  'sample.evtx',
  'xml-row-element.txt',
  'block-keyvalue-dash.txt',
  'line-iso-bracket.log',
  'line-dashdate-level.log',
  'line-dotdate-pidtid.log',
  'line-time-pidtid.log',
  'line-time-dotdate.log',
]);

async function unloadOwnAndReEnable(request) {
  const list = await request.get('/api/files');
  const data = await list.json();
  for (const f of data.files || []) {
    if (FIXTURE_NAMES.has(f.name)) {
      await request.post('/api/files/unload', { data: { id: f.id } });
    } else {
      // Re-enable any pre-loaded files we disabled in load().
      await request.post('/api/files/toggle', { data: { id: f.id, enabled: true } });
    }
  }
}

test.beforeEach(async ({ request }) => {
  await unloadOwnAndReEnable(request);
});

test.afterAll(async ({ request }) => {
  await unloadOwnAndReEnable(request);
});

// REQ-DT-01 — NDJSON via direct DuckDB read_json_auto. Documented
// exception: no `_source_format` is injected (preserving the original
// schema is the whole point of the direct path). We assert (a) the
// file loaded, (b) the row count is non-zero, and (c) the first row
// carries @timestamp because the gateway fixture happens to include
// it — this proves end-to-end query path works without relying on
// adapter-side injection.
test('REQ-DT-01 ndjson', async ({ request }) => {
  const info = await load(request, 'sample.ndjson');
  expect(info.records).toBeGreaterThanOrEqual(1);
  const row = await firstRow(request);
  expect(row).toBeTruthy();
  expect(row['@timestamp']).toBeTruthy();
});

// REQ-DT-02 — EVTX. The fixture is deliberately NOT committed: EVTX is
// binary and cannot be synthesized by test-fixtures/generate.py, and a
// real capture carries the machine name, user SIDs, and installed-
// software traces of whatever host produced it. See REQUIREMENTS.md.
//
// Drop any .evtx at test-fixtures/formats/sample.evtx to enable this
// test locally (`wevtutil epl Application <path>` on Windows).
test('REQ-DT-02 evtx', async ({ request }) => {
  test.skip(
    !fs.existsSync(path.join(FIX, 'sample.evtx')),
    'sample.evtx not present — see REQUIREMENTS.md "Fixtures"',
  );
  const info = await load(request, 'sample.evtx');
  if (info.records === 0) {
    // Quiet event log — adapter routing still verified via /api/files
    // having accepted the file. Skip the row-level assertion.
    test.info().annotations.push({ type: 'note', description: 'EVTX fixture had no events; only dispatcher routing verified.' });
    return;
  }
  const row = await firstRow(request);
  expect(row._source_format).toBe('evtx');
  expect(row['@timestamp']).toBeTruthy();
});

// REQ-DT-03 — XML with autodetected row element.
test('REQ-DT-03 xml-row-element', async ({ request }) => {
  await load(request, 'xml-row-element.txt');
  const row = await firstRow(request);
  expect(row._source_format).toMatch(/^xml:/);
  expect(row['@timestamp']).toBeTruthy();
});

// REQ-DT-04 — block: Key:Value records separated by 20+ dashes.
test('REQ-DT-04 block-keyvalue-dash', async ({ request }) => {
  const info = await load(request, 'block-keyvalue-dash.txt');
  expect(info.records).toBeGreaterThanOrEqual(1);
  const row = await firstRow(request);
  expect(row._source_format).toBe('block:keyvalue-dash-separated');
  expect(row['@timestamp']).toBeTruthy();
});

// REQ-DT-05 — line: ISO date + bracketed level/component/PID.
test('REQ-DT-05 line-iso-bracket', async ({ request }) => {
  const info = await load(request, 'line-iso-bracket.log');
  expect(info.records).toBeGreaterThanOrEqual(1);
  const row = await firstRow(request);
  expect(row._source_format).toBe('line:iso-bracket');
  expect(row['@timestamp']).toBeTruthy();
});

// REQ-DT-06 — line: DD-MM-YYYY + level word.
test('REQ-DT-06 line-dashdate-level', async ({ request }) => {
  const info = await load(request, 'line-dashdate-level.log');
  expect(info.records).toBeGreaterThanOrEqual(1);
  const row = await firstRow(request);
  expect(row._source_format).toBe('line:dashdate-level');
  expect(row['@timestamp']).toBeTruthy();
});

// REQ-DT-07 — line: prefix + DD.MM.YY HH:MM:SS:msec PID/TID.
test('REQ-DT-07 line-dotdate-pidtid', async ({ request }) => {
  const info = await load(request, 'line-dotdate-pidtid.log');
  expect(info.records).toBeGreaterThanOrEqual(1);
  const row = await firstRow(request);
  expect(row._source_format).toBe('line:dotdate-pidtid');
  expect(row['@timestamp']).toBeTruthy();
});

// REQ-DT-08 — line: time-of-day only + PID/TID (date from mtime).
test('REQ-DT-08 line-time-pidtid', async ({ request }) => {
  const info = await load(request, 'line-time-pidtid.log');
  expect(info.records).toBeGreaterThanOrEqual(1);
  const row = await firstRow(request);
  expect(row._source_format).toBe('line:time-pidtid');
  expect(row['@timestamp']).toBeTruthy();
});

// REQ-DT-09 — line: time first, then date.
test('REQ-DT-09 line-time-dotdate', async ({ request }) => {
  const info = await load(request, 'line-time-dotdate.log');
  expect(info.records).toBeGreaterThanOrEqual(1);
  const row = await firstRow(request);
  expect(row._source_format).toBe('line:time-dotdate');
  expect(row['@timestamp']).toBeTruthy();
});
