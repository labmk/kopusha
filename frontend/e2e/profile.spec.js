import { test, expect } from '@playwright/test';

// Field profiling (#18). The point is not the numbers — it is that
// looking at them leads somewhere: see what the data contains, click a
// value, and you are looking at those rows. A panel that only reported
// would be a slower way to read a schema.

async function openProfile(page) {
  await page.goto('/');
  const inputs = page.getByPlaceholder('YYYY-MM-DD HH:MM:SS', { exact: false });
  await inputs.first().fill('2000-01-01 00:00:00');
  await inputs.nth(1).fill('2099-12-31 23:59:59');
  await page.getByRole('button', { name: 'Apply' }).click();
  await expect(page.locator('.log-table tbody tr').first()).toBeVisible({ timeout: 15_000 });

  await page.getByRole('button', { name: 'Fields', exact: true }).click();
  await expect(page.locator('.field-profile')).toBeVisible();
  await expect(page.locator('.fp-field').first()).toBeVisible({ timeout: 15_000 });
}

test('the panel summarises every field', async ({ page }) => {
  await openProfile(page);

  const panel = page.locator('.field-profile');
  await expect(panel.locator('.field-profile-head')).toContainText('over 5,000 rows');

  // One entry per column in the union, each with a cardinality and a
  // fill rate.
  const fields = panel.locator('.fp-field');
  expect(await fields.count()).toBeGreaterThan(3);
  await expect(fields.first().locator('.fp-stats')).toContainText('%');
});

test('expanding a field shows its value distribution', async ({ page }) => {
  await openProfile(page);

  const field = page.locator('.fp-field', { hasText: 'SeverityText' });
  await field.locator('.fp-field-head').click();

  const values = field.locator('.fp-value');
  await expect(values.first()).toBeVisible({ timeout: 10_000 });
  expect(await values.count()).toBeGreaterThan(1);
  // Value, count and share — the share is what makes it a distribution
  // rather than a list.
  await expect(values.first()).toContainText('%');
});

test('clicking a value narrows the result to it', async ({ page }) => {
  await openProfile(page);

  const field = page.locator('.fp-field', { hasText: 'SeverityText' });
  await field.locator('.fp-field-head').click();

  const target = field.locator('.fp-value', { hasText: 'ERROR' }).first();
  await expect(target).toBeVisible({ timeout: 10_000 });
  const label = await target.innerText();
  const expected = Number(label.match(/([\d,]+)\s*·/)[1].replace(/,/g, ''));

  await target.click();

  // The count the panel promised is the count the query returns.
  await expect(page.locator('.status-bar')).toContainText(
    `${expected.toLocaleString('en-US')} records`, { timeout: 15_000 });

  // The profile re-scopes to the rows now on screen, rather than
  // continuing to describe a result the operator has narrowed away.
  await expect(page.locator('.field-profile-head')).toContainText(
    `over ${expected.toLocaleString('en-US')} rows`);

  // And the narrowed view is shareable like any other.
  await expect(page).toHaveURL(/SeverityText/);
});

test('the panel is not computed until it is opened', async ({ page }) => {
  let profileCalls = 0;
  await page.route('**/api/profile', (route) => { profileCalls++; route.continue(); });

  await page.goto('/');
  await expect(page.locator('.log-table tbody tr').first()).toBeVisible({ timeout: 15_000 });
  // A full scan with two aggregates per column is cheap on demand and
  // wasteful on every query.
  expect(profileCalls).toBe(0);

  await page.getByRole('button', { name: 'Fields', exact: true }).click();
  await expect(page.locator('.fp-field').first()).toBeVisible({ timeout: 15_000 });
  expect(profileCalls).toBeGreaterThan(0);
});
