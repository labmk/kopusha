import { test, expect } from '@playwright/test';

// The release check has worked since 0.1.0 and reported through a small
// link in the status bar, which is a fine place for a fact and a poor
// place for news — it went unnoticed through two releases. These guard
// the surface that replaced it.
//
// The harness runs with --no-update-check, so "an update exists" is
// simulated by intercepting the endpoint. That is the right level: the
// check itself has Go tests, and what regressed here was the UI never
// showing what the check found.

const RELEASE_URL = 'https://github.com/labmk/obs-viewer/releases/tag/v9.9.9';

async function withUpdateAvailable(page, latest = '9.9.9') {
  await page.route('**/api/update', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        current: '0.2.0',
        latest,
        available: true,
        url: RELEASE_URL,
        checked: true,
        enabled: true,
      }),
    })
  );
}

test('an available release is announced, with a link to it', async ({ page }) => {
  await withUpdateAvailable(page);
  await page.goto('/');

  const notice = page.locator('.update-notice');
  await expect(notice).toBeVisible();
  await expect(notice).toContainText('9.9.9 is available');
  await expect(notice).toContainText('You are running 0.2.0');

  const link = notice.getByRole('link');
  await expect(link).toHaveAttribute('href', RELEASE_URL);
  // A new tab, and no referrer — the page the operator is on is their
  // business, not the release page's.
  await expect(link).toHaveAttribute('target', '_blank');
  await expect(link).toHaveAttribute('rel', /noreferrer/);

  // Says plainly that nothing self-installs, so nobody waits for an
  // update that is never coming.
  await expect(notice).toContainText('does not download or install');
});

test('dismissing is remembered per release', async ({ page }) => {
  await withUpdateAvailable(page);
  await page.goto('/');

  const notice = page.locator('.update-notice');
  await expect(notice).toBeVisible();
  await notice.getByRole('button', { name: 'Dismiss' }).click();
  await expect(notice).toHaveCount(0);

  // Dismissing the interruption must not discard the information.
  await expect(page.locator('.status-update')).toBeVisible();

  await page.reload();
  await expect(page.locator('.update-notice')).toHaveCount(0);
  await expect(page.locator('.status-update')).toBeVisible();
});

test('a newer release announces itself again', async ({ page }) => {
  await withUpdateAvailable(page, '9.9.9');
  await page.goto('/');
  await page.locator('.update-notice').getByRole('button', { name: 'Dismiss' }).click();
  await expect(page.locator('.update-notice')).toHaveCount(0);

  // Declining one version says nothing about the next.
  await page.unroute('**/api/update');
  await withUpdateAvailable(page, '9.9.10');
  await page.reload();
  await expect(page.locator('.update-notice')).toContainText('9.9.10 is available');
});

test('nothing is announced when there is no update', async ({ page }) => {
  await page.goto('/');
  await expect(page.locator('.status-bar')).toBeVisible();
  await expect(page.locator('.update-notice')).toHaveCount(0);
});

test('the header links to the project page', async ({ page }) => {
  await page.goto('/');
  const res = await page.request.get('/api/version');
  const { repository } = await res.json();
  expect(repository).toMatch(/^https:\/\//);

  const logo = page.locator('.logo-link');
  await expect(logo).toHaveAttribute('href', repository);
  await expect(logo).toHaveAttribute('target', '_blank');
  await expect(logo).toContainText('obs-viewer');
});
