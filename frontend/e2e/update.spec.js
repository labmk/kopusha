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

const RELEASE_URL = 'https://github.com/labmk/kopusha/releases/tag/v9.9.9';

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

  // The notice offers to install it, and says what pressing the button
  // does before it is pressed.
  await expect(notice.getByRole('button', { name: 'Update' })).toBeVisible();
  await expect(notice).toContainText('checks its build provenance before writing');
});

test('preparing an update shows what it will do before writing anything', async ({ page }) => {
  await withUpdateAvailable(page);
  await page.route('**/api/update/prepare', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        from: '0.2.0',
        to: '9.9.9',
        attestation: { commit: 'abcdef0123456789abcdef' },
        rules: {
          frozen: false,
          changes: [
            { name: '10-shipped.yaml', action: 'replace' },
            { name: '20-edited.yaml', action: 'keep', reason: 'you edited it' },
          ],
        },
      }),
    })
  );
  await page.goto('/');

  const notice = page.locator('.update-notice');
  await notice.getByRole('button', { name: 'Update' }).click();

  // The commit is named, so the operator can go and read it.
  await expect(notice).toContainText('abcdef012345');
  // The rule they edited is listed with the reason it survives — this is
  // the whole reason the plan is shown rather than just applied.
  await expect(notice).toContainText('20-edited.yaml');
  await expect(notice).toContainText('you edited it');
  await expect(notice).toContainText('config files are never replaced');
  await expect(notice).toContainText('Nothing has been written yet');
  await expect(notice.getByRole('button', { name: 'Install and restart' })).toBeVisible();
});

test('a refused download is reported and installs nothing', async ({ page }) => {
  await withUpdateAvailable(page);
  await page.route('**/api/update/prepare', (route) =>
    route.fulfill({
      status: 502,
      contentType: 'application/json',
      body: JSON.stringify({
        error: 'refusing to install: no build attestation exists for these bytes',
      }),
    })
  );
  await page.goto('/');

  const notice = page.locator('.update-notice');
  await notice.getByRole('button', { name: 'Update' }).click();

  await expect(notice).toContainText('no build attestation exists');
  // Back to offering, not stuck mid-flight, and never offering to install.
  await expect(notice.getByRole('button', { name: 'Update' })).toBeEnabled();
  await expect(notice.getByRole('button', { name: 'Install and restart' })).toHaveCount(0);
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
  await expect(logo).toContainText('kopusha');
});
