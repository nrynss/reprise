// The host voice picker on the record page. The guest picks one of three
// voices before Start, the pick survives a reload, and the mint carries it.
// npx playwright test -c src/routes/record/mock.playwright.config.ts src/routes/record/voice-picker.spec.ts

import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';

const VOICES = ['Anna British', 'George American', 'Eve American'];

async function openMockRecord(page: Page): Promise<void> {
	await page.goto('/record?mock=1');
	// The start control listens through a page effect, so the take waits for
	// the mock handle that effect installs before pressing Enter. A press that
	// lands first activates a button with no listener and the take never opens.
	await page.waitForFunction(() => {
		const handle = (window as unknown as { __stems?: { complete?: unknown } }).__stems;
		return typeof handle?.complete === 'function';
	});
}

async function radioNames(page: Page): Promise<string[]> {
	const texts = await page.getByRole('radio').allInnerTexts();
	return texts.map((text) => text.replace(/\s+/g, ' ').trim());
}

test('the picker offers three voices in order and keeps the pick', async ({ page }) => {
	await openMockRecord(page);
	const group = page.getByRole('radiogroup', { name: 'Host voice' });
	await expect(group).toBeVisible();
	await expect(page.getByText('Host voice', { exact: true }).first()).toBeVisible();
	expect(await radioNames(page)).toEqual(VOICES);

	const anna = page.getByRole('radio', { name: 'Anna British' });
	const eve = page.getByRole('radio', { name: 'Eve American' });
	await expect(anna).toBeChecked();
	await expect(eve).not.toBeChecked();

	await eve.click();
	await expect(eve).toBeChecked();
	await expect(anna).not.toBeChecked();

	await page.getByRole('button', { name: 'Start session' }).focus();
	await page.keyboard.press('Enter');
	await expect(page.getByRole('heading', { name: 'On air' })).toBeVisible();
	await expect(group).toHaveCount(0);

	await page.goto('/record?mock=1');
	await expect(page.getByRole('radio', { name: 'Eve American' })).toBeChecked();
});

test('the picker fits a 375 px wide phone with no sideways scroll', async ({ page }) => {
	await page.setViewportSize({ width: 375, height: 800 });
	await openMockRecord(page);
	const scroll = await page.evaluate(() => ({
		scrollWidth: document.documentElement.scrollWidth,
		clientWidth: document.documentElement.clientWidth
	}));
	expect(scroll.scrollWidth).toBeLessThanOrEqual(scroll.clientWidth);
	for (const name of VOICES) {
		const box = await page.getByRole('radio', { name }).boundingBox();
		expect(box).not.toBeNull();
		expect(box?.x ?? -1).toBeGreaterThanOrEqual(0);
		expect((box?.x ?? 376) + (box?.width ?? 376)).toBeLessThanOrEqual(375);
	}
});

test('the mint carries the picked voice and a refusal stops before any socket', async ({
	page
}) => {
	const requests: Array<{ method: string; body: string }> = [];
	const sockets: string[] = [];
	page.on('websocket', (socket) => sockets.push(socket.url()));
	await page.route('**/api/sessions', async (route) => {
		const request = route.request();
		requests.push({ method: request.method(), body: request.postData() ?? '' });
		await route.fulfill({
			status: 503,
			contentType: 'application/json',
			body: JSON.stringify({ code: 'sessions_paused', message: 'live sessions are paused' })
		});
	});
	await page.goto('/record');
	await page.getByRole('radio', { name: 'George American' }).click();
	await expect(page.getByRole('radio', { name: 'George American' })).toBeChecked();
	await page.getByRole('button', { name: 'Start session' }).click();
	await expect
		.poll(() => requests.length, { timeout: 10_000 })
		.toBe(1);
	expect(requests[0]?.method).toBe('POST');
	expect(JSON.parse(requests[0]?.body ?? '')).toEqual({ voice: 'george' });
	// The 503 refuses the mint, so the take stays preflight and no socket opens.
	await expect(page.getByRole('heading', { name: 'Record' })).toBeVisible();
	await expect(page.getByRole('radiogroup', { name: 'Host voice' })).toBeVisible();
	expect(sockets).toEqual([]);
	expect(requests.length).toBe(1);
});
