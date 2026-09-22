// Playwright pins for the gallery link on every record phase. Each proof
// opens one phase the mock take can show, then follows the link to the
// season list. No proof waits on a clock or a person. This spec runs with
// the mock suite beside the route:
// npx playwright test -c src/routes/record/mock.playwright.config.ts gallery-link.spec.ts

import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';

interface MockVoice {
	feedBlocks(count: number): void;
	finishTake(): Promise<void>;
	awaitRetry(): Promise<void>;
}

interface AssignWatch {
	__assignLog: string[];
	__assignSpy: (url: string | URL) => void;
}

async function startMockTake(page: Page): Promise<void> {
	await page.goto('/record?mock=1');
	await page.getByRole('button', { name: 'Start session' }).focus();
	await page.keyboard.press('Enter');
	await expect(page.getByRole('heading', { name: 'On air' })).toBeVisible();
}

async function confirmEnd(page: Page): Promise<void> {
	await page.getByRole('button', { name: 'End session', exact: true }).click();
	await page.getByRole('button', { name: 'Confirm end session', exact: true }).click();
}

async function followGalleryLink(page: Page): Promise<void> {
	const link = page.getByRole('link', { name: 'Back to gallery', exact: true });
	await expect(link).toBeVisible();
	await link.click();
	await expect(page.getByRole('heading', { name: 'The season so far', exact: true })).toBeVisible();
	expect(new URL(page.url()).pathname).toBe('/');
}

// Hold the session open call so the starting phase stays on screen. The
// take reaches that phase before the audio clock resumes, and a resumed
// clock would race the proof into the live phase.
async function holdSessionOpen(page: Page): Promise<void> {
	await page.addInitScript(() => {
		AudioContext.prototype.resume = function (): Promise<void> {
			return new Promise(() => {
				// The clock stays paused, so the phase cannot leave starting.
			});
		};
	});
}

// Hold the draft move so the ending phase stays on screen. A settled move
// leaves for processing, and the proof would no longer be on the record page.
async function holdDraftMove(page: Page): Promise<() => void> {
	let release: () => void = () => {};
	const held = new Promise<void>((resolve) => {
		release = resolve;
	});
	await page.route('**/stems/complete', async (route) => {
		try {
			await held;
			await route.abort();
		} catch {
			// The page left, or the request had already ended.
		}
	});
	return release;
}

const draftMoveBody = {
	episode_id: 'mock-episode',
	moved: true,
	scheduled: true,
	job_id: 'tj-1',
	state: 'draft'
};

// Hold the draft move, then answer 200 with one draft body. Aborting the
// held call would hide a late jump to processing, so this helper fulfills.
// The first call can refuse, which leaves the retry in flight for the hold.
async function holdDraftMoveForFulfill(
	page: Page,
	failFirst = false
): Promise<() => Promise<void>> {
	let seen = 0;
	let release: () => void = () => {};
	const held = new Promise<void>((resolve) => {
		release = resolve;
	});
	let markDone: () => void = () => {};
	const done = new Promise<void>((resolve) => {
		markDone = resolve;
	});
	await page.route('**/stems/complete', async (route) => {
		seen += 1;
		if (failFirst && seen === 1) {
			await route.fulfill({
				status: 500,
				contentType: 'application/json',
				body: JSON.stringify({ error: { code: 'overloaded', message: 'the server is busy' } })
			});
			return;
		}
		try {
			await held;
			await route.fulfill({
				status: 200,
				contentType: 'application/json',
				body: JSON.stringify(draftMoveBody)
			});
		} catch {
			// The page left, or the request had already ended.
		} finally {
			markDone();
		}
	});
	return async () => {
		release();
		await done;
	};
}

// Watch location.assign so a late jump is visible in the same turn it
// happens. The processing URL is recorded before the browser commits it.
async function watchAssign(page: Page): Promise<void> {
	const installed = await page.evaluate(() => {
		const target = window as unknown as AssignWatch;
		const log: string[] = [];
		target.__assignLog = log;
		const original = Location.prototype.assign;
		const spy = function (this: Location, url: string | URL): void {
			log.push(String(url));
			original.call(this, url);
		};
		Location.prototype.assign = spy;
		target.__assignSpy = spy;
		return Location.prototype.assign === spy;
	});
	expect(installed).toBe(true);
}

// Read the path after the move settles. A jump to processing destroys the
// page, and that still counts as leaving the gallery.
async function expectStayedOnGallery(page: Page, wait: 'take' | 'retry' = 'take'): Promise<void> {
	const settled = await page
		.evaluate(async (which) => {
			const target = window as unknown as AssignWatch & { __mockVoice: MockVoice };
			if (which === 'retry') await target.__mockVoice.awaitRetry();
			else await target.__mockVoice.finishTake();
			return {
				path: new URL(window.location.href).pathname,
				assigns: target.__assignLog
			};
		}, wait)
		.catch(async (error: unknown) => {
			const message = error instanceof Error ? error.message : '';
			if (!message.includes('context was destroyed')) throw error;
			await page.waitForURL(/\/processing/);
			return { path: new URL(page.url()).pathname, assigns: [page.url()] };
		});
	expect(settled.path).toBe('/');
	expect(settled.assigns.some((url) => url.includes('/processing'))).toBe(false);
	await expect(page.getByRole('heading', { name: 'The season so far', exact: true })).toBeVisible();
	expect(new URL(page.url()).pathname).toBe('/');
}

async function stubDraftMove(page: Page, ok: boolean): Promise<void> {
	await page.route('**/stems/complete', async (route) => {
		if (!ok) {
			await route.fulfill({
				status: 500,
				contentType: 'application/json',
				body: JSON.stringify({ error: { code: 'overloaded', message: 'the server is busy' } })
			});
			return;
		}
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
	});
}

async function runDraftMove(page: Page): Promise<void> {
	await page.evaluate(() =>
		(window as unknown as { __stems: { complete(): Promise<void> } }).__stems.complete()
	);
}

test('preflight returns to the gallery by the link', async ({ page }) => {
	await page.goto('/record?mock=1');
	const start = page.getByRole('button', { name: 'Start session' });
	await expect(start).toBeEnabled();
	await expect(start).toHaveText('Start session');
	await followGalleryLink(page);
});

test('starting returns to the gallery by the link', async ({ page }) => {
	await holdSessionOpen(page);
	await page.goto('/record?mock=1');
	await page.getByRole('button', { name: 'Start session' }).focus();
	await page.keyboard.press('Enter');
	const start = page.getByRole('button', { name: 'Start session' });
	await expect(start).toBeDisabled();
	await expect(start).toHaveText('Opening…');
	await expect(page.getByRole('heading', { name: 'On air' })).toHaveCount(0);
	await followGalleryLink(page);
});

test('the live take returns to the gallery by the link', async ({ page }) => {
	await startMockTake(page);
	await followGalleryLink(page);
});

test('ending returns to the gallery by the link', async ({ page }) => {
	await startMockTake(page);
	await page.evaluate((count) => {
		(window as unknown as { __mockVoice: MockVoice }).__mockVoice.feedBlocks(count);
	}, 12);
	const release = await holdDraftMove(page);
	try {
		await confirmEnd(page);
		await expect(page.getByRole('heading', { name: 'Record', exact: true })).toBeVisible();
		await expect(page.getByRole('heading', { name: 'Live session', exact: true })).toBeVisible();
		await expect(page.getByRole('status')).toHaveText('Moving the take to draft.');
		await expect(page.getByRole('heading', { name: 'Draft move retry' })).toHaveCount(0);
		await followGalleryLink(page);
	} finally {
		release();
	}
});

test('a draft move that finishes after the gallery link stays there', async ({ page }) => {
	await startMockTake(page);
	await page.evaluate((count) => {
		(window as unknown as { __mockVoice: MockVoice }).__mockVoice.feedBlocks(count);
	}, 12);
	await watchAssign(page);
	const fulfill = await holdDraftMoveForFulfill(page);
	await confirmEnd(page);
	await expect(page.getByRole('heading', { name: 'Record', exact: true })).toBeVisible();
	await expect(page.getByRole('status')).toHaveText('Moving the take to draft.');
	await followGalleryLink(page);
	await fulfill();
	await expectStayedOnGallery(page);
});

test('a retried draft move that finishes after the gallery link stays there', async ({ page }) => {
	await startMockTake(page);
	await page.evaluate((count) => {
		(window as unknown as { __mockVoice: MockVoice }).__mockVoice.feedBlocks(count);
	}, 12);
	await watchAssign(page);
	const fulfill = await holdDraftMoveForFulfill(page, true);
	await confirmEnd(page);
	await expect(page.getByRole('heading', { name: 'Draft move retry' })).toBeVisible();
	await page.getByRole('button', { name: 'Retry draft move' }).click();
	await expect(page.getByRole('status')).toHaveText('Moving the take to draft.');
	await followGalleryLink(page);
	await fulfill();
	await expectStayedOnGallery(page, 'retry');
});

test('a refused ending returns to the gallery by the link', async ({ page }) => {
	await startMockTake(page);
	await page.evaluate((count) => {
		(window as unknown as { __mockVoice: MockVoice }).__mockVoice.feedBlocks(count);
	}, 12);
	await stubDraftMove(page, false);
	await confirmEnd(page);
	await expect(page.getByRole('heading', { name: 'Draft move retry' })).toBeVisible();
	await expect(page.getByRole('status')).toContainText('failed');
	await expect(page.getByRole('button', { name: 'Retry draft move' })).toBeVisible();
	await followGalleryLink(page);
});

test('recovery returns to the gallery by the link', async ({ page }) => {
	await page.goto('/record?mock=1&resume=1');
	await expect(page.getByRole('heading', { name: 'Recovered upload' })).toBeVisible();
	await followGalleryLink(page);
});

test('a finished draft move returns to the gallery by the link', async ({ page }) => {
	await startMockTake(page);
	await stubDraftMove(page, true);
	await runDraftMove(page);
	await expect(page.getByRole('heading', { name: 'On air' })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Draft move' })).toBeVisible();
	await expect(page.getByText('Draft ready. Transcript job tj-1 runs now.')).toBeVisible();
	await followGalleryLink(page);
});

test('a refused draft move returns to the gallery by the link', async ({ page }) => {
	await startMockTake(page);
	await stubDraftMove(page, false);
	await runDraftMove(page);
	await expect(page.getByRole('heading', { name: 'On air' })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Draft move' })).toBeVisible();
	await expect(page.getByRole('alert')).toContainText('failed');
	await expect(page.getByRole('button', { name: 'Retry draft move' })).toBeVisible();
	await followGalleryLink(page);
});
