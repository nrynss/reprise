// Playwright proofs for the sign-in screens. Every proof stubs the
// login routes, so no mail and no server take part. The route stubs
// answer the same envelopes the server sends, and the proofs assert
// the same screens a live run would show. Run beside the route:
// npx playwright test -c src/routes/account/account.playwright.config.ts
import { expect, test, type Page, type Route } from '@playwright/test';

const ADDRESS = 'friend@example.com';

function envelope(code: string): string {
	return JSON.stringify({ error: { code, message: code } });
}

function seasonNav(page: Page) {
	return page.locator('nav[aria-label="Season"]');
}

async function stubCode(page: Page, handler: (route: Route) => Promise<void>): Promise<void> {
	await page.route('**/api/login/code', handler);
}

async function acceptCodes(page: Page): Promise<void> {
	await stubCode(page, async (route) => {
		await route.fulfill({
			status: 202,
			contentType: 'application/json',
			body: '{"ok":true}'
		});
	});
}

async function requestFrom(page: Page): Promise<void> {
	await page.getByLabel('Email address').fill(ADDRESS);
	await page.getByRole('button', { name: 'Send the code' }).click();
}

test('a code request opens the code step, and a wrong code stays', async ({ page }) => {
	await acceptCodes(page);
	await page.route('**/api/login/verify', async (route) => {
		await route.fulfill({
			status: 401,
			contentType: 'application/json',
			body: envelope('invalid_code')
		});
	});
	await page.goto('/account');
	await expect(seasonNav(page).getByRole('link', { name: 'Sign in' })).toBeVisible();
	await requestFrom(page);
	await expect(page.getByText(`A code is on its way to ${ADDRESS}.`)).toBeVisible();
	await expect(page.getByLabel('Six digit code')).toBeVisible();
	await page.getByLabel('Six digit code').fill('000000');
	await page.getByRole('button', { name: 'Check the code' }).click();
	await expect(page.getByText('That code did not match.')).toBeVisible();
	await expect(page.getByLabel('Six digit code')).toBeVisible();
});

test('a conflict offers both diaries, and keeping stays a guest', async ({ page }) => {
	await acceptCodes(page);
	const verifyBodies: unknown[] = [];
	await page.route('**/api/login/verify', async (route) => {
		verifyBodies.push(route.request().postDataJSON());
		await route.fulfill({
			status: 409,
			contentType: 'application/json',
			body: envelope('diary_conflict')
		});
	});
	await page.goto('/account');
	await requestFrom(page);
	await page.getByLabel('Six digit code').fill('123456');
	await page.getByRole('button', { name: 'Check the code' }).click();
	await expect(page.getByRole('heading', { name: 'This device already holds takes' })).toBeVisible();
	await expect(
		page.getByRole('button', { name: "Keep this device's diary" })
	).toBeVisible();
	await expect(page.getByRole('button', { name: 'Switch to your account' })).toBeVisible();
	expect(verifyBodies).toHaveLength(1);
	expect(verifyBodies[0]).toEqual({ email: ADDRESS, code: '123456' });
	await page.getByRole('button', { name: "Keep this device's diary" }).click();
	await expect(page.getByText('This device keeps its takes.')).toBeVisible();
	await expect(page.getByLabel('Email address')).toBeVisible();
	await expect(seasonNav(page).getByRole('link', { name: 'Sign in' })).toBeVisible();
});

test('switching signs in, and signing out starts over', async ({ page }) => {
	await acceptCodes(page);
	await page.route('**/api/login/verify', async (route) => {
		const body = route.request().postDataJSON() as { choice?: string };
		if (body.choice === 'switch') {
			await route.fulfill({
				status: 200,
				contentType: 'application/json',
				body: '{"ok":true}'
			});
		} else {
			await route.fulfill({
				status: 409,
				contentType: 'application/json',
				body: envelope('diary_conflict')
			});
		}
	});
	const signouts: unknown[] = [];
	await page.route('**/api/login/signout', async (route) => {
		signouts.push(route.request().postDataJSON());
		await route.fulfill({
			status: 200,
			contentType: 'application/json',
			body: '{"ok":true}'
		});
	});
	await page.goto('/account');
	await requestFrom(page);
	await page.getByLabel('Six digit code').fill('123456');
	await page.getByRole('button', { name: 'Check the code' }).click();
	await expect(page.getByRole('heading', { name: 'This device already holds takes' })).toBeVisible();
	await page.getByRole('button', { name: 'Switch to your account' }).click();
	await expect(page.getByText(`Signed in as ${ADDRESS}.`)).toBeVisible();
	await expect(seasonNav(page).getByRole('link', { name: 'Account' })).toBeVisible();
	const deletion = page.getByRole('button', { name: 'Delete account' });
	await expect(deletion).toBeVisible();
	await expect(deletion).toBeDisabled();
	await expect(page.getByText('Account deletion arrives with the next update.')).toBeVisible();
	await expect(page.getByRole('link', { name: 'Delete account' })).toHaveCount(0);
	await page.getByRole('button', { name: 'Sign out' }).click();
	await expect(page.getByText('Signed out on this device.')).toBeVisible();
	await expect(seasonNav(page).getByRole('link', { name: 'Sign in' })).toBeVisible();
	expect(signouts).toHaveLength(1);
});

test('a capped resend names the wait and reopens after it', async ({ page }) => {
	let calls = 0;
	await stubCode(page, async (route) => {
		calls += 1;
		if (calls === 2) {
			await route.fulfill({
				status: 429,
				contentType: 'application/json',
				headers: { 'retry-after': '2' },
				body: envelope('send_limited')
			});
		} else {
			await route.fulfill({
				status: 202,
				contentType: 'application/json',
				body: '{"ok":true}'
			});
		}
	});
	await page.goto('/account');
	await requestFrom(page);
	await expect(page.getByText(`A code is on its way to ${ADDRESS}.`)).toBeVisible();
	await page.getByRole('button', { name: 'Send a new code' }).click();
	const waiting = page.getByRole('button', { name: 'Wait 2 seconds' });
	await expect(waiting).toBeDisabled();
	await expect(page.getByText('Too many codes went out.')).toBeVisible();
	const resend = page.getByRole('button', { name: 'Send a new code' });
	await expect(resend).toBeEnabled({ timeout: 10_000 });
	await resend.click();
	await expect(page.getByText(`A code is on its way to ${ADDRESS}.`)).toBeVisible();
	expect(calls).toBe(3);
});
