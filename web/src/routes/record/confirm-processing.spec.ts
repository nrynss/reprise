// Confirm ends the take and opens processing while the upload is still
// running. Cancel leaves the take on air. A tab close during the ask still
// ends the session. A reload during that upload posts the resumed pair.
// Run it with the mock suite beside the route.

import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';

interface Harness {
	feedBlocks(count: number): void;
	sessionEndCount(): number;
	httpEndCount(): number;
	userUploadId(): string | undefined;
	hostUploadId(): string | undefined;
	rate(): number;
}

async function startMockTake(page: Page): Promise<void> {
	await page.goto('/record?mock=1');
	// The start control listens through a page effect, so the take waits for
	// the mock handle that effect installs before pressing Enter. A press that
	// lands first activates a button with no listener and the take never opens.
	await page.waitForFunction(() => {
		const handle = (window as unknown as { __stems?: { complete?: unknown } }).__stems;
		return typeof handle?.complete === 'function';
	});
	await page.getByRole('button', { name: 'Start session' }).focus();
	await page.keyboard.press('Enter');
	await expect(page.getByRole('heading', { name: 'On air' })).toBeVisible();
}

async function installJobFixtures(page: Page): Promise<void> {
	await page.route('**/api/episodes/**', async (route) => {
		const url = route.request().url();
		if (url.includes('/stems/complete')) {
			await route.fulfill({
				status: 200,
				contentType: 'application/json',
				body: JSON.stringify({
					episode_id: 'mock-episode',
					moved: true,
					scheduled: true,
					job_id: 'tj-1',
					state: 'draft'
				})
			});
			return;
		}
		await route.fulfill({
			status: 200,
			contentType: 'application/json',
			body: JSON.stringify({
				episode: {
					id: 'mock-episode',
					number: 1,
					title: 'Untitled episode',
					state: 'draft',
					visibility: 'private'
				},
				proposals: [],
				words: [],
				transcript_outcome: { job_id: 'tj-1', status: 'running', error: '' },
				editorial_outcome: { job_id: 'ej-1', status: 'running', error: '' }
			})
		});
	});
}

test('cancel leaves the take recording', async ({ page }) => {
	await startMockTake(page);
	await page.getByRole('button', { name: 'End session', exact: true }).click();
	await page.getByRole('button', { name: 'Cancel end', exact: true }).click();
	const ends = await page.evaluate(() => {
		const mock = (window as unknown as { __mockVoice: Harness }).__mockVoice;
		mock.feedBlocks(4);
		return mock.sessionEndCount();
	});
	expect(ends).toBe(0);
	await expect(page.getByRole('heading', { name: 'On air' })).toBeVisible();
	await expect(page.getByRole('button', { name: 'End session', exact: true })).toBeEnabled();
});

test('closing the tab during confirmation still ends the session', async ({ page }) => {
	await startMockTake(page);
	await page.getByRole('button', { name: 'End session', exact: true }).click();
	await expect(page.getByRole('button', { name: 'Cancel end', exact: true })).toBeVisible();
	const counts = await page.evaluate(() => {
		window.dispatchEvent(new Event('pagehide'));
		const mock = (window as unknown as { __mockVoice: Harness }).__mockVoice;
		return { sessionEnds: mock.sessionEndCount(), httpEnds: mock.httpEndCount() };
	});
	expect(counts.sessionEnds).toBe(1);
	expect(counts.httpEnds).toBe(1);
});

test('confirm opens processing before a throttled upload finishes', async ({ page }) => {
	test.setTimeout(30_000);
	await startMockTake(page);
	await page.evaluate(() => {
		(window as unknown as { __mockVoice: Harness }).__mockVoice.feedBlocks(8);
	});
	await page.evaluate(() => {
		const previous = window.fetch.bind(window);
		window.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
			const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
			if (url.includes('/api/uploads/') && url.includes('/chunks/')) {
				await new Promise((resolve) => setTimeout(resolve, 4000));
			}
			return previous(input, init);
		}) as typeof window.fetch;
	});
	await installJobFixtures(page);
	await page.getByRole('button', { name: 'End session', exact: true }).click();
	const started = Date.now();
	await page.getByRole('button', { name: 'Confirm end session', exact: true }).click({ noWaitAfter: true });
	await page.waitForURL(/\/processing/, { timeout: 1000 });
	expect(Date.now() - started).toBeLessThan(1000);
	await expect(page.getByRole('heading', { name: 'Processing' })).toBeVisible();
	await expect(page.getByRole('link', { name: 'Gallery', exact: true })).toBeVisible();
	await expect(page.getByText('Uploading the take.')).toBeVisible();
	await expect(page.getByText('Both stems durable')).toHaveCount(0);
	await expect(page.getByText('done: Transcript ready.', { exact: true })).toBeVisible({ timeout: 15_000 });
	await expect(page.getByText('done: Proposals ready.', { exact: true })).toBeVisible();
	await expect(page.getByText('done: The draft is ready.', { exact: true })).toBeVisible();
	await expect(page.getByRole('link', { name: 'Open the episode', exact: true })).toBeVisible();
	await expect(page.getByRole('link', { name: 'Gallery', exact: true })).toBeVisible();
});

test('a reload during the upload posts the resumed stem pair', async ({ page }) => {
	test.setTimeout(30_000);
	await startMockTake(page);
	const ids = await page.evaluate(() => {
		const mock = (window as unknown as { __mockVoice: Harness }).__mockVoice;
		return {
			userId: mock.userUploadId() ?? '',
			hostId: mock.hostUploadId() ?? '',
			rate: mock.rate()
		};
	});
	expect(ids.userId).not.toBe('');
	expect(ids.hostId).not.toBe('');
	expect(ids.userId).not.toBe(ids.hostId);
	await page.evaluate(() => {
		(window as unknown as { __mockVoice: Harness }).__mockVoice.feedBlocks(8);
	});
	await page.evaluate(() => {
		const previous = window.fetch.bind(window);
		window.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
			const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
			if (url.includes('/api/uploads/') && url.includes('/chunks/')) {
				await new Promise((resolve) => setTimeout(resolve, 4000));
			}
			return previous(input, init);
		}) as typeof window.fetch;
	});
	const posts: Array<Record<string, unknown>> = [];
	await page.route('**/api/episodes/**', async (route) => {
		const url = route.request().url();
		if (url.includes('/stems/complete')) {
			posts.push(route.request().postDataJSON() as Record<string, unknown>);
			await route.fulfill({
				status: 200,
				contentType: 'application/json',
				body: JSON.stringify({
					episode_id: 'mock-episode',
					moved: true,
					scheduled: true,
					job_id: 'tj-1',
					state: 'draft'
				})
			});
			return;
		}
		const ready = posts.length > 0;
		await route.fulfill({
			status: 200,
			contentType: 'application/json',
			body: JSON.stringify({
				episode: {
					id: 'mock-episode',
					number: 1,
					title: 'Untitled episode',
					state: 'draft',
					visibility: 'private'
				},
				proposals: [],
				words: [],
				transcript_outcome: ready ? { job_id: 'tj-1', status: 'running', error: '' } : null,
				editorial_outcome: ready ? { job_id: 'ej-1', status: 'running', error: '' } : null
			})
		});
	});
	await page.getByRole('button', { name: 'End session', exact: true }).click();
	await page.getByRole('button', { name: 'Confirm end session', exact: true }).click({ noWaitAfter: true });
	await page.waitForURL(/\/processing/, { timeout: 1000 });
	await expect(page.getByText('Uploading the take.')).toBeVisible();
	const before = posts.length;
	await page.reload();
	await expect.poll(() => posts.length, { timeout: 15_000 }).toBeGreaterThan(before);
	const body = posts[posts.length - 1] ?? {};
	expect(body['user_media_id']).toBe(ids.userId);
	expect(body['host_media_id']).toBe(ids.hostId);
	expect(body['user_sample_rate']).toBe(Math.round(ids.rate));
	expect(body['host_sample_rate']).toBe(24000);
	await expect(page.getByText('done: The draft is ready.', { exact: true })).toBeVisible({ timeout: 15_000 });
	await expect(page.getByText('waiting:')).toHaveCount(0);
	await expect(page.getByRole('link', { name: 'Open the episode', exact: true })).toBeVisible();
	await expect(page.getByRole('link', { name: 'Gallery', exact: true })).toBeVisible();
});
