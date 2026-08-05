import { test, expect } from '@playwright/test';

// The samples have shipped since 0.3.1 with nothing pointing at them,
// and the empty state is where having no data is the whole problem.
// The suite shares one server process across spec files, so this test
// has to put the harness back exactly as it found it. Anything it
// unloads, it reloads.
test('the empty state offers the shipped samples and loads them', async ({ page }) => {
  await page.goto('/');

  const before = await page.request.get('/api/files').then((r) => r.json());
  const original = (before.files || []).map((f) => f.path);

  try {
    for (const f of before.files || []) {
      await page.request.post('/api/files/unload', { data: { id: f.id } });
    }
    await page.reload();
    await expect(page.locator('.empty-state')).toBeVisible();

    const samples = await page.request.get('/api/samples').then((r) => r.json());
    expect(samples.available).toBe(true);
    expect(samples.files.length).toBeGreaterThan(0);
    // README.md ships for a person to read, not for the engine to parse.
    expect(samples.files.map((f) => f.name)).not.toContain('README.md');

    const button = page.getByRole('button', { name: /Try the samples/ });
    await expect(button).toBeVisible();
    await button.click();

    // Every parseable sample lands in the file panel, queried as one table.
    await expect(page.locator('.status-bar')).toContainText(/[1-9]\d* records/, { timeout: 20000 });

    // Unioning nine formats gives a wide, sparse table. Columns that
    // carry values have to come before ones that are empty for most
    // rows, or the first screen is blank cells and the message is
    // pushed out of sight.
    const headers = await page.locator('.log-table th, [role="columnheader"]')
      .allTextContents()
      .then((t) => t.map((x) => x.trim()).filter(Boolean));
    const messageAt = headers.findIndex((h) => /^message$/i.test(h));
    const sparseAt = headers.findIndex((h) => /^(AppDomain|EventSource|Machine)$/i.test(h));
    expect(messageAt).toBeGreaterThanOrEqual(0);
    if (sparseAt >= 0) {
      expect(messageAt).toBeLessThan(sparseAt);
    }

    // unmatched.log is in the folder precisely because no rule matches
    // it, so the report has to name it rather than swallow the failure.
    await expect(page.locator('.notice-bar')).toContainText('unmatched.log');
  } finally {
    const after = await page.request.get('/api/files').then((r) => r.json());
    for (const f of after.files || []) {
      await page.request.post('/api/files/unload', { data: { id: f.id } });
    }
    for (const path of original) {
      await page.request.post('/api/files/load', { data: { path } });
    }
  }
});

test('the controls reference opens from the header and from "?"', async ({ page }) => {
  await page.goto('/');

  await page.getByRole('button', { name: 'What everything does' }).click();
  const dialog = page.getByRole('dialog', { name: 'What everything does' });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText('Getting data in');
  await expect(dialog).toContainText('Keyboard');
  // The address bar is how a view is shared now that Copy link is gone,
  // so the reference has to say so.
  await expect(dialog).toContainText('address bar');

  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);

  await page.keyboard.press('?');
  await expect(page.getByRole('dialog', { name: 'What everything does' })).toBeVisible();
});

// "?" must stay typeable into a filter, or the shortcut costs more than
// it gives.
test('"?" typed into an input does not open the reference', async ({ page }) => {
  await page.goto('/');
  const search = page.getByPlaceholder('Search all fields...');
  await search.click();
  await search.type('who?');
  await expect(page.getByRole('dialog', { name: 'What everything does' })).toHaveCount(0);
  await expect(search).toHaveValue('who?');
});
