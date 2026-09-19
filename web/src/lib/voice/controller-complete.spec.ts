// Playwright proofs for the automatic draft move at take end. The record
// page runs under the mock flag, so the take opens against doubles with
// generated input and no microphone. A route stub answers the completion
// route the way the server state machine does: the first success moves the
// take to draft with one job. These proofs press the standing end control
// and never touch a hand driven handle, so the automatic path from both
// uploads through the draft move fires on its own. This spec runs with the
// mock suite beside the controller:
// npx playwright test -c src/lib/voice/mock.playwright.config.ts controller-complete.spec.ts

import { expect, test } from '@playwright/test';
import type { Page, Route } from '@playwright/test';

interface MockVoice {
	feedBlocks(count: number): void;
	rate(): number;
	userUploadId(): string;
	hostUploadId(): string;
	retryCompletion(): Promise<void>;
	completionState(): string;
}

interface CompletionCall {
	url: string;
	body: unknown;
}

interface CompletionStub {
	calls: CompletionCall[];
	failNext(): void;
	unroute(page: Page): Promise<void>;
}

function draftAnswer(first: boolean): unknown {
	if (first) {
		return { episode_id: 'mock-episode', moved: true, scheduled: true, job_id: 'tj-1', state: 'draft' };
	}
	return { episode_id: 'mock-episode', moved: false, scheduled: false, job_id: 'tj-1', state: 'draft' };
}

async function installCompletionStub(page: Page): Promise<CompletionStub> {
	const calls: CompletionCall[] = [];
	let moved = false;
	let failNext = false;
	const pattern = '**/stems/complete';
	const handler = async (route: Route) => {
		const request = route.request();
		const url = new URL(request.url());
		calls.push({ url: url.pathname, body: request.postDataJSON() });
		if (failNext) {
			failNext = false;
			await route.fulfill({
				status: 500,
				contentType: 'application/json',
				body: JSON.stringify({ error: { code: 'overloaded', message: 'the server is busy' } })
			});
			return;
		}
		const first = !moved;
		moved = true;
		await route.fulfill({
			status: 200,
			contentType: 'application/json',
			body: JSON.stringify(draftAnswer(first))
		});
	};
	await page.route(pattern, handler);
	return {
		calls,
		failNext: () => {
			failNext = true;
		},
		unroute: (target: Page) => target.unroute(pattern, handler)
	};
}

async function startMockTake(page: Page): Promise<void> {
	await page.goto('/record?mock=1');
	await page.getByRole('button', { name: 'Start session' }).focus();
	await page.keyboard.press('Enter');
	await expect(page.getByRole('heading', { name: 'On air' })).toBeVisible();
}

async function readStems(page: Page): Promise<{ userId: string; hostId: string; rate: number }> {
	await page.evaluate((count) => {
		(window as unknown as { __mockVoice: MockVoice }).__mockVoice.feedBlocks(count);
	}, 12);
	return page.evaluate(() => {
		const mock = (window as unknown as { __mockVoice: MockVoice }).__mockVoice;
		return { userId: mock.userUploadId(), hostId: mock.hostUploadId(), rate: mock.rate() };
	});
}

async function endTake(page: Page): Promise<void> {
	await page.getByRole('button', { name: 'End session', exact: true }).click();
	await page.getByRole('button', { name: 'Confirm end session', exact: true }).click();
}

test('ending a mock take posts the stored pair and reaches processing', async ({ page }) => {
	await startMockTake(page);
	const ids = await readStems(page);
	const stub = await installCompletionStub(page);
	await endTake(page);
	await page.waitForURL(/processing/);
	await expect(page.getByRole('heading', { name: 'Processing' })).toBeVisible();
	await expect(page.getByText('Both stems durable')).toBeVisible();
	expect(stub.calls.length).toBe(1);
	expect(stub.calls[0].url).toBe('/api/episodes/mock-episode/stems/complete');
	expect(stub.calls[0].body).toEqual({
		user_media_id: ids.userId,
		host_media_id: ids.hostId,
		user_sample_rate: ids.rate,
		host_sample_rate: 24000
	});
	await stub.unroute(page);
});

test('a failed draft move refuses loudly and the retry reaches processing', async ({ page }) => {
	await startMockTake(page);
	await readStems(page);
	const stub = await installCompletionStub(page);
	stub.failNext();
	await endTake(page);
	await expect(page.getByRole('status')).toContainText('failed');
	await expect(page.getByRole('status')).toContainText('retry');
	const state = await page.evaluate(
		() => (window as unknown as { __mockVoice: MockVoice }).__mockVoice.completionState()
	);
	expect(state).toBe('failed');
	expect(page.url()).toContain('/record');
	expect(stub.calls.length).toBe(1);
	await page.evaluate(() =>
		(window as unknown as { __mockVoice: MockVoice }).__mockVoice.retryCompletion()
	);
	await page.waitForURL(/processing/);
	await expect(page.getByRole('heading', { name: 'Processing' })).toBeVisible();
	expect(stub.calls.length).toBe(2);
	await stub.unroute(page);
});
