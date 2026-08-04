import { test, expect } from '@playwright/test';

// Smoke tests for the core viewer flow. They run against a real Go
// backend with two NDJSON files pre-loaded from `test-fixtures/ndjson/`
// (set up by playwright.config.js webServer). DuckDB is in-memory so no
// state survives across runs.

// Fixtures are synthetic and their timestamps come from
// test-fixtures/generate.py, so no test should hardcode the fixture
// year — regenerating with a different BASE would silently empty every
// result set. Widen to a range that can't plausibly exclude anything.
async function widenTimeRange(page) {
  const inputs = page.getByPlaceholder('YYYY-MM-DD HH:MM:SS', { exact: false });
  await inputs.first().fill('2000-01-01 00:00:00');
  await inputs.nth(1).fill('2099-12-31 23:59:59');
  await page.getByRole('button', { name: 'Apply' }).click();
}

test('app loads with version and pre-loaded files', async ({ page }) => {
  await page.goto('/');
  // Header carries a v-string sourced from /api/version — proves the
  // backend healthcheck path is alive end-to-end.
  await expect(page.locator('.app-header')).toContainText(/v\d+\.\d+/);
  // Status bar reports two files active (matches the --dir pre-load).
  await expect(page.locator('.status-bar')).toContainText('2/2 files active');
});

test('clean slate: time fields empty, no filter pills on load', async ({ page }) => {
  await page.goto('/');
  // Both time inputs render their placeholder, no value.
  const inputs = page.getByPlaceholder('YYYY-MM-DD HH:MM:SS');
  await expect(inputs).toHaveCount(2);
  await expect(inputs.first()).toHaveValue('');
  await expect(inputs.nth(1)).toHaveValue('');
  // No filter pills, no search text.
  await expect(page.locator('.filter-pill')).toHaveCount(0);
  await expect(page.getByPlaceholder('Search all fields...')).toHaveValue('');
});

test('query text dialog round-trips a filter from visual builder', async ({ page }) => {
  await page.goto('/');
  // Wait for fields to populate from /api/fields.
  await page.waitForResponse((r) => r.url().endsWith('/api/fields') && r.ok());

  // Add a search term so the text form has something to serialize.
  await page.getByPlaceholder('Search all fields...').fill('error');

  // Open the text dialog and confirm the serialized form contains the
  // search term as a pipeline DSL line filter.
  await page.getByRole('button', { name: 'Text' }).click();
  const textarea = page.locator('.rdx-content textarea');
  await expect(textarea).toBeVisible();
  await expect(textarea).toContainText('{}');
  await expect(textarea).toContainText('|= "error"');

  // Edit the text — add a label filter, then Apply. The visual builder
  // should reflect the new filter pill after parse.
  const original = await textarea.inputValue();
  const edited = original.replace(
    '|= "error"',
    '|= "error"\n  | json\n  | severity = "warn"'
  );
  await textarea.fill(edited);
  await page.getByRole('button', { name: 'Apply', exact: true }).click();

  // Dialog closes, the new filter pill appears in the QueryBuilder.
  await expect(page.locator('.filter-pill').filter({ hasText: 'severity' })).toBeVisible();
  await expect(page.locator('.filter-pill').filter({ hasText: '"warn"' })).toBeVisible();
});

test('query text dialog reports a parse error and keeps the dialog open', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Text' }).click();
  const textarea = page.locator('.rdx-content textarea');
  await expect(textarea).toBeVisible();
  // Put gibberish that doesn't match any line shape.
  await textarea.fill('{}\n  | this is not a valid line\n');
  await page.getByRole('button', { name: 'Apply', exact: true }).click();
  // Dialog stays open and shows the error.
  await expect(textarea).toBeVisible();
  await expect(page.locator('.rdx-content .obs-error')).toBeVisible();
});

test('renders rows from pre-loaded fixtures', async ({ page }) => {
  await page.goto('/');
  // The app starts with an empty time range (clean slate), so widen it
  // before expecting rows.
  await widenTimeRange(page);
  // At least one record renders — the gateway fixture has 2500 rows.
  await expect(page.locator('.log-table tbody tr').first()).toBeVisible({ timeout: 10_000 });
});

test('column picker (Radix Popover) opens and toggles a column', async ({ page }) => {
  await page.goto('/');
  await widenTimeRange(page);
  await expect(page.locator('.log-table tbody tr').first()).toBeVisible({ timeout: 10_000 });

  // Open the column picker — was the spot with the hardcoded-offset
  // dropdown; now a Radix Popover anchored to the trigger button.
  await page.getByRole('button', { name: /^Columns \(/ }).click();
  // The picker lands in a portal — find a checkbox in it and click it,
  // then assert the popover is open (visible).
  const popover = page.locator('.rdx-popover').filter({ hasText: 'Toggle columns' });
  await expect(popover).toBeVisible();
  const firstCheckbox = popover.locator('input[type="checkbox"]').first();
  await firstCheckbox.uncheck();
  // Close picker; the column count text in the trigger updates.
  await page.keyboard.press('Escape');
  await expect(page.getByRole('button', { name: /^Columns \(/ })).toBeVisible();
});

test('exists operator narrows to rows where field is present', async ({ page }) => {
  await page.goto('/');
  await widenTimeRange(page);
  await expect(page.locator('.log-table tbody tr').first()).toBeVisible({ timeout: 10_000 });

  // Add an "exists" filter for the @timestamp field — every row in the
  // fixtures has it, so the result count is unchanged but the filter
  // pill confirms the valueless operator round-trips through the SQL
  // builder.
  await page.getByRole('button', { name: '+ Filter' }).click();
  // FieldPicker auto-opens on add-filter mount (0.5.11+); no need to
  // click the trigger. Scope text selection to .rdx-popover so it
  // doesn't collide with the top-bar Sort dropdown.
  await page.getByPlaceholder('filter fields...').fill('@timestamp');
  await page.locator('.rdx-popover').getByText('@timestamp', { exact: true }).click();
  // Operator dropdown — the first one inside the add-filter row.
  await page.locator('select').filter({ hasText: 'contains' }).first().selectOption('exists');
  // Value input should be hidden now.
  await expect(page.getByPlaceholder('value (* = wildcard)')).toHaveCount(0);
  await page.getByRole('button', { name: 'Add', exact: true }).click();
  await page.getByRole('button', { name: 'Apply' }).click();
  // Pill rendered without a quoted value, and rows still present.
  await expect(page.locator('.filter-pill').filter({ hasText: 'exists' })).toBeVisible();
  await expect(page.locator('.log-table tbody tr').first()).toBeVisible({ timeout: 10_000 });
});

test('status bar shows idle timeout', async ({ page }) => {
  await page.goto('/');
  const statusBar = page.locator('.status-bar');
  // No modules ship by default, so the module list is empty; the idle
  // timeout is always surfaced. 180s default → "3m" via formatTimeout.
  await expect(statusBar).toContainText('idle timeout:');
  await expect(statusBar).toContainText(/\d+h \d+m|\d+m|\d+d/);
});

test('no update notice when the check is disabled', async ({ page }) => {
  // The harness runs with --no-update-check, so /api/update reports
  // enabled=false. The status bar must stay silent rather than implying
  // "up to date" from an absence of information.
  const res = await page.request.get('/api/update');
  expect(res.ok()).toBeTruthy();
  const body = await res.json();
  expect(body.enabled).toBe(false);
  expect(body.available).toBe(false);

  await page.goto('/');
  await expect(page.locator('.status-bar')).toBeVisible();
  await expect(page.locator('.status-update')).toHaveCount(0);
});

test('text dialog ingests single-line assistant output and auto-corrects .X. regex', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Text' }).click();
  const textarea = page.locator('.rdx-content textarea').first();
  await expect(textarea).toBeVisible();

  // The two mistakes LLM-generated pipeline DSL makes most often, in one paste:
  // the whole query collapsed onto a single line, and `.X.` written where
  // `.*X.*` was meant. Both should be silently corrected on Apply.
  await textarea.fill(
    '{} |= "pool" |= "storage" | json | SeverityText = "error" | nodeinfo.type =~ ".edge." | service.name = "gateway"'
  );
  await page.getByRole('button', { name: 'Apply', exact: true }).click();

  // Dialog closes, the pills populate the visual builder.
  await expect(textarea).not.toBeVisible();

  // Three label filters: SeverityText, nodeinfo.type, service.name.
  // The nodeinfo.type pill must show the corrected .*edge.* — proves
  // the auto-fix ran.
  const pills = page.locator('.filter-pill');
  await expect(pills).toHaveCount(3);
  await expect(page.locator('.filter-pill').filter({ hasText: 'SeverityText' })).toBeVisible();
  await expect(page.locator('.filter-pill').filter({ hasText: 'nodeinfo.type' })).toContainText('.*edge.*');
  await expect(page.locator('.filter-pill').filter({ hasText: 'service.name' })).toBeVisible();

  // The transient notice should mention the auto-correction.
  await expect(page.getByText('auto-corrected', { exact: false })).toBeVisible();
});

test('@timestamp is always the leftmost column, even when sort field changes', async ({ page }) => {
  await page.goto('/');
  await widenTimeRange(page);
  await expect(page.locator('.log-table tbody tr').first()).toBeVisible({ timeout: 10_000 });

  // First data column header (skip the chevron column at index 0).
  const firstHeader = page.locator('.log-table thead th').nth(1);
  await expect(firstHeader).toContainText('@timestamp');

  // Switch the engine's sort field to ObservedTimestamp. Pre-0.5.12 the
  // priority list put `timestampField` first, so this would move
  // @timestamp into "rest" and ObservedTimestamp would become column 1.
  // Now @timestamp stays first; ObservedTimestamp slots in second.
  await page.locator('select').filter({ hasText: '@timestamp' }).first().selectOption('ObservedTimestamp');
  await expect(firstHeader).toContainText('@timestamp');
});

test('+ Filter opens FieldPicker first and surfaces value suggestions', async ({ page }) => {
  await page.goto('/');
  await page.waitForResponse((r) => r.url().endsWith('/api/fields') && r.ok());

  // Click "+ Filter". The FieldPicker popover should open IMMEDIATELY
  // (autoOpen) — operator types into the search input to filter the
  // field list. Pre-0.5.11 the value input got focus and the operator
  // had to click the picker first.
  await page.getByRole('button', { name: '+ Filter' }).click();
  await expect(page.locator('.rdx-popover')).toBeVisible();
  await expect(page.getByPlaceholder('filter fields...')).toBeFocused();

  // Pick a known low-cardinality field. nodeinfo.type comes from the
  // pre-loaded NDJSON struct discovery (F56).
  await page.getByPlaceholder('filter fields...').fill('nodeinfo.type');
  await page.locator('.rdx-popover').getByText('nodeinfo.type', { exact: true }).click();
  await expect(page.locator('.rdx-popover')).not.toBeVisible();

  // Switch operator to `is` so the value field expects an exact match —
  // datalist suggestions are most useful here.
  await page.locator('select').filter({ hasText: 'contains' }).first().selectOption('is');

  // A <datalist> should now be rendered next to the value input,
  // populated from /api/field-samples for nodeinfo.type.
  const valueInput = page.getByPlaceholder('value (* = wildcard)');
  await expect(valueInput).toBeVisible();
  const listId = await valueInput.getAttribute('list');
  expect(listId).toBe('vals-add-nodeinfo.type');
  // Wait for the fetch to land — sample values populate as <option>
  // elements inside the datalist. Browser doesn't render datalist
  // options visibly until interaction, but the DOM is queryable.
  // Attribute selector avoids the literal-dot-in-ID escaping problem
  // (CSS treats `.` as a class separator; CSS.escape isn't available
  // in Playwright's Node-side selector parser).
  const datalistSel = 'datalist[id="vals-add-nodeinfo.type"] option';
  await expect(page.locator(datalistSel).first()).toBeAttached();
  const optionValues = await page.locator(datalistSel).evaluateAll((els) => els.map((e) => e.value));
  expect(optionValues).toContain('edge');
});

test('struct sub-field filters resolve against the union (nodeinfo.type)', async ({ request }) => {
  // Both pre-loaded fixtures carry `nodeinfo` as STRUCT(type VARCHAR,
  // id VARCHAR), so `nodeinfo.type` only resolves if struct sub-path
  // discovery worked. `service.name` separates the two files:
  // gateway.ndjson is all "gateway", indexer.ndjson all "indexer".
  //
  // Locks in: STRUCT sub-paths discovered at LoadFile, projected via
  // to_json() in the union, resolved as json_extract_string in WHERE.
  const fields = await request.get('/api/fields').then((r) => r.json());
  expect(fields.fields).toContain('nodeinfo.type');
  expect(fields.fields).toContain('service.name');

  const samples = await request.get('/api/field-samples?fields=nodeinfo.type,service.name').then((r) => r.json());
  // Both structs must surface real values (the empty-array signal that
  // the field was too high-cardinality should NOT trigger here).
  expect(samples['nodeinfo.type'].length).toBeGreaterThan(0);
  expect(samples['nodeinfo.type']).toContain('edge');

  const q = await request.post('/api/query', {
    data: {
      filters: [{ field: 'nodeinfo.type', operator: 'contains', value: 'edge', logic: 'and' }],
      offset: 0,
      limit: 1,
    },
  }).then((r) => r.json());
  expect(q.error).toBeUndefined();
  expect(q.total_count).toBeGreaterThan(0);
});

test('AND/OR between filters toggles with one click', async ({ page }) => {
  await page.goto('/');
  await page.waitForResponse((r) => r.url().endsWith('/api/fields') && r.ok());

  // Add two filters so the connector word appears.
  const addOne = async (field, value) => {
    await page.getByRole('button', { name: '+ Filter' }).click();
    await page.getByPlaceholder('filter fields...').fill(field);
    await page.locator('.rdx-popover').getByText(field, { exact: true }).click();
    await page.getByPlaceholder('value (* = wildcard)').fill(value);
    await page.getByRole('button', { name: 'Add', exact: true }).click();
  };
  await addOne('SeverityText', 'error');
  await addOne('nodeinfo.type', 'edge');

  // The connector between pill 1 and pill 2 starts as AND.
  const connector = page.getByRole('button', { name: /^(and|or)$/ });
  await expect(connector).toBeVisible();
  await expect(connector).toHaveText('and');

  // One click flips to OR; another back to AND.
  await connector.click();
  await expect(connector).toHaveText('or');
  await connector.click();
  await expect(connector).toHaveText('and');
});

test('is_one_of operator: multi-select chip UI + IN-clause query', async ({ page }) => {
  await page.goto('/');
  await page.waitForResponse((r) => r.url().endsWith('/api/fields') && r.ok());

  await page.getByRole('button', { name: '+ Filter' }).click();
  await page.getByPlaceholder('filter fields...').fill('nodeinfo.type');
  await page.locator('.rdx-popover').getByText('nodeinfo.type', { exact: true }).click();
  await page.locator('select').filter({ hasText: 'contains' }).first().selectOption('is_one_of');

  // MultiValueInput renders chips + a free input. Use the stable
  // data-testid since the placeholder disappears once any chip is
  // added.
  const input = page.getByTestId('multivalue-input');
  await expect(input).toBeVisible();
  await input.fill('edge');
  await input.press('Enter');
  await input.fill('storage');
  await input.press('Enter');

  // Verify both chips render — match the labels via the
  // multivalue-chip-label data-testid (chip text includes the × button
  // which would otherwise foil an exact-text match).
  const chipLabels = page.getByTestId('multivalue-chip-label');
  await expect(chipLabels.filter({ hasText: 'edge' })).toBeVisible();
  await expect(chipLabels.filter({ hasText: 'storage' })).toBeVisible();

  // Commit the filter (the "Add" button is the explicit path here —
  // Enter-on-empty calls onCommit which triggers addFilter, but
  // clicking Add is more obvious in the test trace).
  await page.getByRole('button', { name: 'Add', exact: true }).click();
  await page.getByRole('button', { name: 'Apply' }).click();
  await expect(page.locator('.log-table tbody tr').first()).toBeVisible({ timeout: 10_000 });
});

test('multi-select: backspace removes last chip when input empty', async ({ page }) => {
  await page.goto('/');
  await page.waitForResponse((r) => r.url().endsWith('/api/fields') && r.ok());
  await page.getByRole('button', { name: '+ Filter' }).click();
  await page.getByPlaceholder('filter fields...').fill('SeverityText');
  await page.locator('.rdx-popover').getByText('SeverityText', { exact: true }).click();
  await page.locator('select').filter({ hasText: 'contains' }).first().selectOption('is_one_of');
  const input = page.getByTestId('multivalue-input');
  await input.fill('error');
  await input.press('Enter');
  await input.fill('warn');
  await input.press('Enter');
  // Backspace on empty input removes "warn" first.
  const chipLabels = page.getByTestId('multivalue-chip-label');
  await expect(chipLabels.filter({ hasText: 'warn' })).toBeVisible();
  await input.press('Backspace');
  await expect(chipLabels.filter({ hasText: 'warn' })).toHaveCount(0);
  await expect(chipLabels.filter({ hasText: 'error' })).toBeVisible();
});

test('/api/shutdown is reachable and reports the grace period', async ({ request }) => {
  // Calls the endpoint but the WebServer Playwright manages will be
  // re-spawned on the next test, so we don't actually let it exit.
  // The handler responds before sleeping, so the synchronous POST
  // gets back the {status, grace_seconds} body and we abort here.
  // (Playwright's webServer config sets reuseExistingServer:false
  // in CI, so a killed server would auto-restart anyway.)
  const r = await request.post('/api/shutdown', { data: '' });
  expect(r.ok()).toBeTruthy();
  const body = await r.json();
  expect(body.status).toBe('shutting_down');
  expect(body.grace_seconds).toBeGreaterThan(0);
  // Touch /api/version to cancel the shutdown (activity bumps after
  // the asOf snapshot). Then wait out the grace + a margin and
  // confirm the server is still alive — proves the cancellation path.
  await request.get('/api/version');
  await new Promise((res) => setTimeout(res, (body.grace_seconds * 1000) + 500));
  const stillAlive = await request.get('/api/version');
  expect(stillAlive.ok(), 'server should still be alive after cancelled shutdown').toBeTruthy();
});

test('healthcheck status shows "no connection" when backend is unreachable', async ({ page }) => {
  // Intercept BEFORE navigation so the very first /api/version probe
  // fails — TanStack Query then drops into the 5s "broken" cadence
  // (vs. the 30s healthy cadence we'd otherwise have to wait through)
  // and the offline pill renders quickly.
  await page.route('**/api/version', (route) => route.abort());
  await page.goto('/');
  await expect(page.locator('.status-offline')).toBeVisible({ timeout: 10_000 });
});

// The header carried a tab strip with a single option, styled as an
// active pill and centred like a title. It only earns its space once a
// module supplies a second tab.
test('no tab strip is shown when nothing can be switched to', async ({ page }) => {
  await page.goto('/');
  await expect(page.locator('.app-header')).toBeVisible();
  await expect(page.locator('.tab-switcher')).toHaveCount(0);
  // And the label it used to carry is gone from the product entirely.
  await expect(page.locator('body')).not.toContainText('OBS Viewer');
});

// Display preferences are set once and then left alone, so they live
// behind the gear rather than in the row of things pressed constantly.
test('display settings live behind the gear and dismiss properly', async ({ page }) => {
  await page.goto('/');
  const gear = page.getByRole('button', { name: 'Display settings' });
  await expect(page.locator('.settings-popover')).toHaveCount(0);

  await gear.click();
  const pop = page.locator('.settings-popover');
  await expect(pop).toBeVisible();
  await expect(pop).toContainText('Timezone');
  await expect(pop).toContainText('Time format');
  await expect(pop).toContainText('Theme');

  await page.keyboard.press('Escape');
  await expect(pop).toHaveCount(0);

  await gear.click();
  await expect(pop).toBeVisible();
  await page.locator('.status-bar').click({ position: { x: 5, y: 5 } });
  await expect(pop).toHaveCount(0);
});

// Copy link is gone: writeState keeps the address bar current, so
// selecting the URL is the same act with one less button.
test('the query state reaches the address bar without a button', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('button', { name: 'Copy link' })).toHaveCount(0);
  await page.getByRole('button', { name: '24h' }).click();
  await expect.poll(() => page.url()).toMatch(/#/);
});

// Every checkbox has to say what it is. The per-file ones sat beside a
// div rather than a label, so they announced only their state.
test('checkboxes carry accessible names', async ({ page }) => {
  await page.goto('/');
  const unnamed = await page.evaluate(() =>
    [...document.querySelectorAll('input[type=checkbox]')]
      .filter((el) => !el.getAttribute('aria-label') && (!el.labels || el.labels.length === 0))
      .length
  );
  expect(unnamed).toBe(0);
});
