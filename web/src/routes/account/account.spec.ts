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
	const deletion = page.getByRole('link', { name: 'Delete account' });
	await expect(deletion).toBeVisible();
	await expect(deletion).toHaveAttribute('href', '/account/delete');
	await expect(page.getByRole('button', { name: 'Delete account' })).toHaveCount(0);
	await deletion.click();
	await expect(page.getByRole('heading', { name: 'Delete your account' })).toBeVisible();
	await page.goto('/account');
	await page.getByRole('button', { name: 'Sign out' }).click();
	await expect(page.getByText('Signed out on this device.')).toBeVisible();
	await expect(seasonNav(page).getByRole('link', { name: 'Sign in' })).toBeVisible();
	expect(signouts).toHaveLength(1);
});

test('the account page shares the gallery shell and pill buttons', async ({ page }) => {
	await page.setViewportSize({ width: 1280, height: 800 });
	await page.goto('/account');
	await expect(page.getByText('Season one')).toHaveCount(0);
	const shell = page.locator('main');
	await expect(shell).toBeVisible();
	const box = await shell.boundingBox();
	const viewport = page.viewportSize();
	if (box === null || viewport === null) throw new Error('The page reported no layout.');
	expect(box.width).toBeLessThanOrEqual(1025);
	expect(box.width).toBeGreaterThan(1000);
	const left = box.x;
	const right = viewport.width - (box.x + box.width);
	expect(Math.abs(left - right)).toBeLessThanOrEqual(2);
	const send = page.getByRole('button', { name: 'Send the code' });
	await expect(send).toBeVisible();
	expect(await send.evaluate((element) => getComputedStyle(element).backgroundColor)).toBe(
		'rgb(232, 163, 61)'
	);
	expect(await send.evaluate((element) => getComputedStyle(element).borderRadius)).toBe('100px');
	const accountLink = seasonNav(page).getByRole('link', { name: 'Sign in' });
	await expect(accountLink).toBeVisible();
	expect(await accountLink.evaluate((element) => getComputedStyle(element).backgroundColor)).toBe(
		'rgb(232, 163, 61)'
	);
});

for (const width of [375, 768, 1280, 1920]) {
	test(`the account layout holds at ${width}px`, async ({ page }) => {
		await page.setViewportSize({ width, height: 800 });
		await page.goto('/account');
		const scroll = await page.evaluate(() => ({
			scroll: document.documentElement.scrollWidth,
			inner: window.innerWidth
		}));
		expect(scroll.scroll, `sideways scroll at ${width}px`).toBeLessThanOrEqual(
			scroll.inner + 1
		);
		const box = await page.locator('main').boundingBox();
		if (box === null) throw new Error('The page reported no layout.');
		expect(box.width, `main width at ${width}px`).toBeLessThanOrEqual(1025);
		if (width === 375) {
			const heights = await page.locator('button, .button').evaluateAll((controls) =>
				controls.map((entry) => (entry as HTMLElement).getBoundingClientRect().height)
			);
			for (const height of heights) {
				expect(height, `tap target at ${width}px`).toBeGreaterThanOrEqual(43.5);
			}
		}
	});
}

test('a signed in device names its shared episodes', async ({ page }) => {
	await acceptCodes(page);
	await page.route('**/api/login/verify', async (route) => {
		await route.fulfill({
			status: 200,
			contentType: 'application/json',
			body: '{"ok":true}'
		});
	});
	const writes: unknown[] = [];
	await page.route('**/api/account/name', async (route) => {
		expect(route.request().method()).toBe('PUT');
		writes.push(route.request().postDataJSON());
		await route.fulfill({
			status: 200,
			contentType: 'application/json',
			body: JSON.stringify({ email: ADDRESS, display_name: 'Mara' })
		});
	});
	await page.route('**/api/account', async (route) => {
		await route.fulfill({
			status: 200,
			contentType: 'application/json',
			body: JSON.stringify({ email: ADDRESS, display_name: '' })
		});
	});
	await page.goto('/account');
	await requestFrom(page);
	await page.getByLabel('Six digit code').fill('123456');
	await page.getByRole('button', { name: 'Check the code' }).click();
	const name = page.getByLabel('Name on shared episodes');
	await expect(name).toBeVisible();
	await name.fill('Mara');
	await page.getByRole('button', { name: 'Save the name' }).click();
	await expect(page.getByText('Name saved.')).toBeVisible();
	expect(writes).toEqual([{ display_name: 'Mara' }]);
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
