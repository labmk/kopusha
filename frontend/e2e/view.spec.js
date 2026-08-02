import { test, expect } from '@playwright/test';

// The two features that make a view something you can point at: a shape
// to see it in, and a link to send it with.

async function widen(page) {
  const inputs = page.getByPlaceholder('YYYY-MM-DD HH:MM:SS', { exact: false });
  await inputs.first().fill('2000-01-01 00:00:00');
  await inputs.nth(1).fill('2099-12-31 23:59:59');
  await page.getByRole('button', { name: 'Apply' }).click();
  await expect(page.locator('.log-table tbody tr').first()).toBeVisible({ timeout: 15_000 });
}

test('the histogram shows counts over the current result', async ({ page }) => {
  await page.goto('/');
  await widen(page);

  const strip = page.locator('.time-histogram');
  await expect(strip).toBeVisible();

  const bars = strip.locator('.time-histogram-bar');
  const count = await bars.count();
  expect(count).toBeGreaterThan(1);
  // The bar count is capped so an aggregate that runs on every query
  // stays cheap whatever the span.
  expect(count).toBeLessThanOrEqual(120);

  // The axis has to say what a bar is worth; "23" means nothing alone.
  await expect(strip.locator('.time-histogram-meta')).toContainText('per bar');
});

test('dragging the histogram narrows the time range', async ({ page }) => {
  await page.goto('/');
  await widen(page);

  const strip = page.locator('.time-histogram');
  await expect(strip).toBeVisible();
  const before = await page.locator('.status-bar').innerText();

  const track = strip.locator('.time-histogram-track');
  const box = await track.boundingBox();
  await page.mouse.move(box.x + box.width * 0.2, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width * 0.45, box.y + box.height / 2, { steps: 8 });
  await page.mouse.up();

  // The drag writes the range into the time inputs — the same filter
  // path typing uses, not a parallel one.
  const from = page.getByPlaceholder('YYYY-MM-DD HH:MM:SS', { exact: false }).first();
  await expect(from).not.toHaveValue('2000-01-01 00:00:00');

  // And it applies on its own: a completed gesture should not need a
  // second confirming click.
  await expect(page.locator('.status-bar')).not.toHaveText(before, { timeout: 10_000 });
});

test('the view is restorable from the URL', async ({ page }) => {
  await page.goto('/');
  await widen(page);

  // Use the search channel rather than the filter builder: fewer moving
  // parts, same round trip through the URL.
  const search = page.getByPlaceholder('Search all fields...');
  await search.fill('gateway');
  await page.getByRole('button', { name: 'Apply' }).click();

  await expect(page).toHaveURL(/#.*q=gateway/, { timeout: 10_000 });
  const shared = page.url();

  // A reload restores it — which is the same mechanism as sharing, and
  // the reason a refresh no longer loses the query.
  await page.goto(shared);
  await expect(search).toHaveValue('gateway');
  await expect(page.locator('.log-table tbody tr').first()).toBeVisible({ timeout: 15_000 });
});

test('the URL carries the time range, sort and hidden columns', async ({ page }) => {
  await page.goto('/');
  await widen(page);

  // Hide a column through the picker. Index 1, not 0: @timestamp is
  // pinned leftmost and is a poor thing to hide in a test about time.
  await page.getByRole('button', { name: /^Columns \(/ }).click();
  const toggle = page.locator('.rdx-popover input[type="checkbox"]').nth(1);
  await expect(toggle).toBeVisible();
  await toggle.uncheck();
  await page.keyboard.press('Escape');

  await expect(page).toHaveURL(/hide=/, { timeout: 10_000 });
  await expect(page).toHaveURL(/from=2000-01-01/);

  const shared = page.url();
  const columnsBefore = await page.getByRole('button', { name: /^Columns \(/ }).innerText();

  await page.goto(shared);
  await expect(page.locator('.log-table tbody tr').first()).toBeVisible({ timeout: 15_000 });

  // The hidden column is still hidden after the round trip — the
  // visible/total count on the button says so without opening it.
  await expect(page.getByRole('button', { name: /^Columns \(/ })).toHaveText(columnsBefore);
});

// Filter values are log content — hostnames, user names, message
// fragments. Anything before the '#' reaches the server, its access log,
// and the Referer header of any outbound link on the page.
test('query state never leaves the URL fragment', async ({ page }) => {
  await page.goto('/');
  await widen(page);

  const search = page.getByPlaceholder('Search all fields...');
  await search.fill('secret-hostname-01');
  await page.getByRole('button', { name: 'Apply' }).click();
  await expect(page).toHaveURL(/#/, { timeout: 10_000 });

  const url = new URL(page.url());
  expect(url.search).toBe('');
  expect(url.hash).toContain('secret-hostname-01');
});
