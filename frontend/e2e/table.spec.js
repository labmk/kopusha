import { test, expect } from '@playwright/test';

// Guards for the two properties that make the result table usable on
// large pages. Both regress silently — a broken virtualiser still shows
// correct data, just slowly — so they need assertions rather than eyes.

async function widenAndWait(page) {
  await page.goto('/');
  const inputs = page.getByPlaceholder('YYYY-MM-DD HH:MM:SS', { exact: false });
  await inputs.first().fill('2000-01-01 00:00:00');
  await inputs.nth(1).fill('2099-12-31 23:59:59');
  await page.getByRole('button', { name: 'Apply' }).click();
  await expect(page.locator('.log-table tbody tr').first()).toBeVisible({ timeout: 15_000 });
}

test('a large page renders a bounded number of rows', async ({ page }) => {
  await widenAndWait(page);

  // Ask for a page far larger than any viewport can show.
  const pageSize = page.locator('select').filter({ hasText: '200' }).first();
  if (await pageSize.count()) {
    await pageSize.selectOption('5000').catch(() => {});
    await page.waitForTimeout(2000);
  }

  // Without windowing this is 5000. The exact number depends on the
  // viewport, so assert the property — bounded — not a magic value.
  const rendered = await page.locator('.log-table tbody tr').count();
  expect(rendered).toBeGreaterThan(0);
  expect(rendered).toBeLessThan(200);
});

test('scrolling keeps the row count bounded and changes the content', async ({ page }) => {
  await widenAndWait(page);

  const firstBefore = await page.locator('.log-table tbody tr td').nth(1).textContent();
  await page.locator('.log-table-container').evaluate((el) => { el.scrollTop = 4000; });
  await page.waitForTimeout(300);

  const rendered = await page.locator('.log-table tbody tr').count();
  expect(rendered).toBeLessThan(200);

  // Different rows are on screen — proves the window moved rather than
  // the container merely scrolling over a fully-rendered list.
  const firstAfter = await page.locator('.log-table tbody tr td').nth(1).textContent();
  expect(firstAfter).not.toBe(firstBefore);
});

test('clicking a row opens the side panel rather than expanding in place', async ({ page }) => {
  await widenAndWait(page);

  await expect(page.locator('.row-detail')).toHaveCount(0);
  await page.locator('.log-table tbody tr').nth(1).click();

  await expect(page.locator('.row-detail')).toBeVisible();
  await expect(page.locator('.log-table tr.row-selected')).toHaveCount(1);
  // The old inline-expansion row must not come back: variable row
  // heights are what made windowing impractical.
  await expect(page.locator('.row-expanded')).toHaveCount(0);

  await page.keyboard.press('Escape');
  await expect(page.locator('.row-detail')).toHaveCount(0);
});

test('j and k move the selection while the panel is open', async ({ page }) => {
  await widenAndWait(page);
  await page.locator('.log-table tbody tr').nth(0).click();
  await expect(page.locator('.row-detail')).toBeVisible();

  const first = await page.locator('.row-detail-title').textContent();
  await page.keyboard.press('j');
  const second = await page.locator('.row-detail-title').textContent();
  expect(second).not.toBe(first);

  await page.keyboard.press('k');
  await expect(page.locator('.row-detail-title')).toHaveText(first);
});
