// Playwright proofs for the Google sign-in pages. The start and the
// switch choice navigate to the server route on click, so the proofs
// stub that route and watch the navigation land. The flags render
// from the query alone, so no provider and no server take part. Run
// beside the route:
// npx playwright test -c src/routes/account/google/google.playwright.config.ts
import { expect, test } from '@playwright/test';

test('the start page walks to the sign-in route on click', async ({ page }) => {
	await page.route('**/api/login/google/start', async (route) => {
		await route.fulfill({ status: 200, contentType: 'text/plain', body: 'started' });
	});
	await page.goto('/account/google');
	await expect(page.getByRole('heading', { name: 'Sign in with Google' })).toBeVisible();
	await page.getByRole('button', { name: 'Continue with Google' }).click();
	await page.waitForURL('**/api/login/google/start');
	await expect(page.getByText('started')).toBeVisible();
	await expect(page.getByRole('link', { name: 'Back to account' })).toHaveCount(0);
});

test('the start page links back to the account screen', async ({ page }) => {
	await page.goto('/account/google');
	const back = page.getByRole('link', { name: 'Back to account' });
	await expect(back).toBeVisible();
	expect(await back.getAttribute('href')).toBe('/account');
});

test('the done flag confirms the sign-in', async ({ page }) => {
	await page.goto('/account/google/callback?done=1');
	await expect(page.getByText('Signed in with Google on this device.')).toBeVisible();
	await expect(page.getByRole('link', { name: 'View your account' })).toBeVisible();
});

test('the conflict flag offers both diaries, and switching retries the route', async ({
	page
}) => {
	await page.route('**/api/login/google/callback**', async (route) => {
		await route.fulfill({ status: 200, contentType: 'text/plain', body: 'switched' });
	});
	await page.goto('/account/google/callback?conflict=1&state=abc-123');
	await expect(
		page.getByRole('heading', { name: 'This device already holds takes' })
	).toBeVisible();
	const keep = page.getByRole('link', { name: "Keep this device's diary" });
	await expect(keep).toBeVisible();
	expect(await keep.getAttribute('href')).toBe('/account');
	await page.getByRole('button', { name: 'Switch to your account' }).click();
	await page.waitForURL('**/api/login/google/callback?state=abc-123&choice=switch');
	await expect(page.getByText('switched')).toBeVisible();
});

test('the failure flag offers a fresh attempt', async ({ page }) => {
	await page.goto('/account/google/callback?error=signin_failed');
	await expect(page.getByText('Google sign-in did not work.')).toBeVisible();
	const retry = page.getByRole('link', { name: 'Try Google sign-in again' });
	await expect(retry).toBeVisible();
	expect(await retry.getAttribute('href')).toBe('/account/google');
});
