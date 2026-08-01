import { defineConfig, devices } from '@playwright/test';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// End-to-end tests run against the real Go binary (so DuckDB does real
// ingest of the fixture NDJSONs). Build it once with `./build.sh` from
// the project root, then `npm run test:e2e` here.
//
// The webServer block spawns `dist/obs_viewer.exe` from the obs-viewer
// project root, pointed at `test-fixtures/ndjson` so two NDJSONs are
// pre-loaded. Port 9201 is used to avoid clobbering a developer's
// running instance on the default 9200.
const PROJECT_ROOT = path.resolve(__dirname, '..');
const BIN = process.platform === 'win32'
  ? path.join(PROJECT_ROOT, 'dist', 'obs_viewer.exe')
  : path.join(PROJECT_ROOT, 'dist', 'obs_viewer');

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: 1,
  reporter: process.env.CI ? 'github' : 'list',
  timeout: 30_000,
  use: {
    baseURL: 'http://127.0.0.1:9201',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
  ],
  webServer: {
    // --no-update-check keeps the suite hermetic. Without it every e2e run
    // would hit the GitHub releases API, making the tests depend on the
    // network and on an unauthenticated rate limit shared by the runner.
    command: `"${BIN}" --port 9201 --no-browser --no-update-check --dir "${path.join(PROJECT_ROOT, 'test-fixtures', 'ndjson')}"`,
    url: 'http://127.0.0.1:9201/api/version',
    reuseExistingServer: !process.env.CI,
    stdout: 'pipe',
    stderr: 'pipe',
    timeout: 30_000,
  },
});
