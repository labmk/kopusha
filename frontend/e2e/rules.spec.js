import { test, expect } from '@playwright/test';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const FIXTURES = path.resolve(__dirname, '../../test-fixtures/formats');

// The failure-to-rule path: a file nothing recognizes has to explain
// itself, and that explanation has to open the builder.
//
// These tests deliberately stop short of saving. A save writes into
// dist/parsers.d next to the binary under test, which would leak into
// later runs and into a developer's working copy; the write path is
// covered by Go tests at both the manager and HTTP layers instead.

async function openBrowserAt(page, dir) {
  await page.goto('/');
  const add = page.getByRole('button', { name: '+ Add' });
  if (await add.count()) {
    await add.click();
  } else {
    await page.getByRole('button', { name: 'Open Files' }).click();
  }
  // The dialog opens on the last-used directory, which is whatever the
  // previous run left in settings. Wait for that first listing to land
  // before typing, or the response overwrites the path box mid-edit.
  await expect(page.locator('.browse-entry').first()).toBeVisible();

  const pathBox = page.getByPlaceholder('Enter path...');
  await pathBox.fill(dir);
  await pathBox.press('Enter');
  // Assert on where we actually are, not on "some entry rendered" —
  // the previous directory's entries are still on screen until the new
  // listing replaces them.
  await expect(page.locator('.browse-path')).toHaveText(dir);
}

test('a file that matches no rule explains itself', async ({ page }) => {
  await openBrowserAt(page, FIXTURES);

  await page.locator('.browse-entry', { hasText: 'unmatched.log' }).click();
  await page.getByRole('button', { name: 'Load Selected' }).click();

  const diag = page.locator('.parse-diagnosis');
  await expect(diag).toBeVisible({ timeout: 15_000 });

  // Every adapter reports, including the ones that declined — those are
  // the point. A verdict with no reason is the failure this guards.
  const rows = diag.locator('.diag-table tr');
  await expect(rows).toHaveCount(6);
  for (const name of ['ndjson', 'parquet', 'evtx', 'block', 'line', 'xml']) {
    // Match the adapter cell exactly: the reason column is prose, and
    // "line" appears inside the block adapter's reason too.
    const row = diag.locator('.diag-table tr').filter({
      has: page.locator('.diag-adapter', { hasText: new RegExp(`^${name}$`) }),
    });
    await expect(row).toHaveCount(1);
    await expect(row.locator('.diag-reason')).not.toHaveText('—');
  }

  // And the line the parser actually saw.
  await expect(diag.locator('.diag-first-line pre')).toContainText('queue depth 2347');
});

test('the diagnosis opens the rule builder with the failing line', async ({ page }) => {
  await openBrowserAt(page, FIXTURES);
  await page.locator('.browse-entry', { hasText: 'unmatched.log' }).click();
  await page.getByRole('button', { name: 'Load Selected' }).click();
  await expect(page.locator('.parse-diagnosis')).toBeVisible({ timeout: 15_000 });

  await page.getByRole('button', { name: 'Build a rule from this line' }).click();

  const builder = page.locator('.rule-builder');
  await expect(builder).toBeVisible();

  // Seeded with the line, and analysed without being asked.
  await expect(builder.locator('.rule-sample')).toContainText('queue depth 2347');
  const pattern = builder.locator('textarea.input-mono').nth(1);
  await expect(pattern).toHaveValue(/\(\?P<ts>/, { timeout: 10_000 });
});

test('the builder previews rows through the real parser', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Parser rules' }).click();

  const builder = page.locator('.rule-builder');
  await expect(builder).toBeVisible();

  await builder.locator('.rule-sample').fill(
    '2026-03-18T06:00:00 gateway[4179]: queue depth 2347\n' +
    '2026-03-18T06:00:04 gateway[8802]: rebalanced shard 7\n' +
    '2026-03-18T06:00:09 gateway[4179]: queue depth 2412'
  );
  await builder.getByRole('button', { name: 'Analyse' }).click();

  // The preview is what makes the whole thing trustworthy: it runs the
  // candidate rule through the same adapter a load would use.
  const table = builder.locator('.rule-preview-table');
  await expect(table).toBeVisible({ timeout: 10_000 });
  await expect(table.locator('tbody tr')).toHaveCount(3);
  await expect(table.locator('thead th').first()).toHaveText('ts');
  await expect(builder.locator('.rule-tally')).toContainText('3 parsed');

  // A timestamp the layout rejects must be called out, not left to be
  // discovered later when a time filter returns nothing.
  const layout = builder.locator('input.input-mono').first();
  await layout.fill('02/01/2006 15:04:05');
  await expect(builder.locator('.rule-tally-bad')).toContainText('without a timestamp', { timeout: 10_000 });
});

test('renaming a field rewrites the capture group', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Parser rules' }).click();
  const builder = page.locator('.rule-builder');

  await builder.locator('.rule-sample').fill(
    '2026-03-18T06:00:00 gateway[4179]: queue depth 2347\n' +
    '2026-03-18T06:00:04 gateway[8802]: rebalanced shard 7'
  );
  await builder.getByRole('button', { name: 'Analyse' }).click();

  const fieldName = builder.locator('.rule-field-name').first();
  await expect(fieldName).toBeVisible({ timeout: 10_000 });
  await fieldName.fill('pid');
  await fieldName.blur();

  const pattern = builder.locator('textarea.input-mono').nth(1);
  await expect(pattern).toHaveValue(/\(\?P<pid>/);
  await expect(builder.locator('.rule-preview-table thead')).toContainText('pid', { timeout: 10_000 });
});

// The engine has written Parquet since 0.2.0, but the dialog only ever
// offered an .ndjson filename, so the capability was unreachable unless
// you knew to type the extension yourself.
test('the export dialog offers every format the engine writes', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Export' }).click();

  const format = page.locator('#export-format');
  await expect(format).toBeVisible();
  await expect(format.locator('option')).toHaveCount(2);

  const pathInput = page.locator('input[placeholder^="/path/to/output"]');
  await expect(pathInput).toHaveValue(/\.ndjson$/);

  await format.selectOption('parquet');
  await expect(pathInput).toHaveValue(/\.parquet$/);

  // And back, without accumulating extensions.
  await format.selectOption('ndjson');
  await expect(pathInput).toHaveValue(/\.ndjson$/);
});

// The dialog used to hold two answers to "where does this go?" and
// show the one it did not use: navigating the folder browser changed
// nothing unless "Select this folder" was clicked, so exporting from a
// folder you were looking at wrote somewhere else, silently.
test('the export target follows the folder being browsed', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Export' }).click();

  const pathInput = page.locator('input[placeholder^="/path/to/output"]');
  await expect(pathInput).toHaveValue(/.+/);
  const before = await pathInput.inputValue();

  await page.getByRole('button', { name: 'Browse' }).click();
  const browsePath = page.locator('.browse-path span').first();
  await expect(browsePath).toHaveText(/.+/);

  // Step into the first subdirectory offered, without confirming.
  const startedAt = (await browsePath.textContent()).trim();
  const firstDir = page.locator('.browse-entry.is-dir').first();
  await expect(firstDir).toBeVisible();
  await firstDir.click();
  await expect(browsePath).not.toHaveText(startedAt);

  const shown = (await browsePath.textContent()).trim();

  // The path — which is what Export sends — now names that folder, and
  // the filename survived the move.
  await expect(pathInput).toHaveValue(new RegExp(`^${shown.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}[/\\\\]`));
  await expect(pathInput).toHaveValue(new RegExp(`${before.split(/[/\\]/).pop().replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}$`));
});
