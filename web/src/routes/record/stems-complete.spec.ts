// Playwright proofs for the stem completion call on the record page. The
// page runs under the mock flag, so the take opens against doubles with
// generated input and no microphone. A spec local stub answers the
// completion route with the server state machine: the first success moves
// the take to draft with one job, and a repeat reports the standing
// outcome. This spec runs with the mock suite beside the route:
// npx playwright test -c src/routes/record/mock.playwright.config.ts stems-complete.spec.ts

import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';

interface StemsHandle {
	complete(): Promise<void>;
	state(): string;
}

interface MockVoice {
	feedBlocks(count: number): void;
	rate(): number;
	userUploadId(): string;
	hostUploadId(): string;
	finishUploads(): Promise<{ userBytes: number; hostBytes: number }>;
}

interface CompletionCall {
	url: string;
	body: unknown;
}

interface CompletionStub {
	calls(): CompletionCall[];
	failNext(): void;
	challengeNext(): void;
}

async function startMockTake(page: Page): Promise<void> {
	await page.goto('/record?mock=1');
	await page.getByRole('button', { name: 'Start session' }).focus();
	await page.keyboard.press('Enter');
	await expect(page.getByRole('heading', { name: 'On air' })).toBeVisible();
}

async function finishBothUploads(page: Page): Promise<{
	userId: string;
	hostId: string;
	rate: number;
}> {
	await page.evaluate((count) => {
		(window as unknown as { __mockVoice: MockVoice }).__mockVoice.feedBlocks(count);
	}, 12);
	await page.evaluate(() =>
		(window as unknown as { __mockVoice: MockVoice }).__mockVoice.finishUploads()
	);
	return page.evaluate(() => {
		const mock = (window as unknown as { __mockVoice: MockVoice }).__mockVoice;
		return { userId: mock.userUploadId(), hostId: mock.hostUploadId(), rate: mock.rate() };
	});
}

async function installCompletionStub(page: Page): Promise<void> {
	await page.evaluate(() => {
		const target = window as unknown as Record<string, unknown>;
		const calls: CompletionCall[] = [];
		let moved = false;
		let failNext = false;
		let challengeNext = false;
		const inner = window.fetch.bind(window);
		window.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
			const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
			if (url.includes('/stems/complete')) {
				const body = init?.body === undefined || init?.body === null ? null : JSON.parse(String(init.body));
				calls.push({ url, body });
				if (challengeNext) {
					challengeNext = false;
					return new Response('<html><body>challenge</body></html>', {
						status: 200,
						headers: { 'content-type': 'text/html; charset=utf-8' }
					});
				}
				if (failNext) {
					failNext = false;
					return new Response(
						JSON.stringify({ error: { code: 'overloaded', message: 'the server is busy' } }),
						{ status: 500, headers: { 'content-type': 'application/json' } }
					);
				}
				const first = !moved;
				moved = true;
				return new Response(
					JSON.stringify(
						first
							? { episode_id: 'mock-episode', moved: true, scheduled: true, job_id: 'tj-1', state: 'draft' }
							: { episode_id: 'mock-episode', moved: false, scheduled: false, job_id: 'tj-1', state: 'draft' }
					),
					{ status: 200, headers: { 'content-type': 'application/json' } }
				);
			}
			return inner(input, init);
		}) as typeof window.fetch;
		target['__completionStub'] = {
			calls: () => calls,
			failNext: () => {
				failNext = true;
			},
			challengeNext: () => {
				challengeNext = true;
			}
		};
	});
}

async function complete(page: Page): Promise<void> {
	await page.evaluate(() => (window as unknown as { __stems: StemsHandle }).__stems.complete());
}

async function stubCalls(page: Page): Promise<CompletionCall[]> {
	return page.evaluate(
		() => (window as unknown as { __completionStub: CompletionStub }).__completionStub.calls()
	);
}

test('both uploads complete into one draft move with one job', async ({ page }) => {
	await startMockTake(page);
	const ids = await finishBothUploads(page);
	await installCompletionStub(page);
	await complete(page);
	await expect(page.getByRole('heading', { name: 'Draft move' })).toBeVisible();
	await expect(page.getByText('Draft ready. Transcript job tj-1 runs now.')).toBeVisible();
	const calls = await stubCalls(page);
	expect(calls.length).toBe(1);
	expect(calls[0].url).toBe('/api/episodes/mock-episode/stems/complete');
	expect(calls[0].body).toEqual({
		user_media_id: ids.userId,
		host_media_id: ids.hostId,
		user_sample_rate: ids.rate,
		host_sample_rate: 24000
	});
});

test('a repeat completion reports the standing outcome', async ({ page }) => {
	await startMockTake(page);
	await finishBothUploads(page);
	await installCompletionStub(page);
	await complete(page);
	await expect(page.getByText('Draft ready. Transcript job tj-1 runs now.')).toBeVisible();
	await complete(page);
	await expect(
		page.getByText('Already a draft. Standing job tj-1 holds the outcome.')
	).toBeVisible();
	const calls = await stubCalls(page);
	expect(calls.length).toBe(2);
});

test('a failed move refuses loudly with a retry', async ({ page }) => {
	await startMockTake(page);
	await finishBothUploads(page);
	await installCompletionStub(page);
	await page.evaluate(() =>
		(window as unknown as { __completionStub: CompletionStub }).__completionStub.failNext()
	);
	await complete(page);
	await expect(page.getByRole('alert')).toContainText('failed');
	await expect(page.getByRole('alert')).toContainText('retry');
	const retry = page.getByRole('button', { name: 'Retry draft move' });
	await expect(retry).toBeEnabled();
	await retry.click();
	await expect(page.getByText('Draft ready. Transcript job tj-1 runs now.')).toBeVisible();
	await expect(page.getByRole('alert')).toHaveCount(0);
	const calls = await stubCalls(page);
	expect(calls.length).toBe(2);
});

test('a challenge page answer names the failed call instead of stalling', async ({ page }) => {
	await startMockTake(page);
	await finishBothUploads(page);
	await installCompletionStub(page);
	await page.evaluate(() =>
		(window as unknown as { __completionStub: CompletionStub }).__completionStub.challengeNext()
	);
	await complete(page);
	await expect(page.getByRole('alert')).toContainText('draft move');
	await expect(page.getByRole('alert')).toContainText('retry');
	expect(page.url()).toContain('/record');
	const retry = page.getByRole('button', { name: 'Retry draft move' });
	await expect(retry).toBeEnabled();
	let calls = await stubCalls(page);
	expect(calls.length).toBe(1);
	await retry.click();
	await expect(page.getByText('Draft ready. Transcript job tj-1 runs now.')).toBeVisible();
	await expect(page.getByRole('alert')).toHaveCount(0);
	calls = await stubCalls(page);
	expect(calls.length).toBe(2);
});
