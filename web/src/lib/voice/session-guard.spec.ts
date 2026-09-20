// Playwright proofs that the voice layer names its failed calls and freezes
// the take clock. The record page runs under the mock flag, so the take opens
// against doubles with generated input and no microphone. The mint proof
// instead presses start on the live page. It never reaches the microphone
// because the challenged mint answers first. The mock fetch hook
// serves the challenge page from inside the page, because the harness answers
// uploads and session end without touching the network. This spec runs with
// the mock suite beside the controller:
// npx playwright test -c src/lib/voice/mock.playwright.config.ts session-guard.spec.ts

import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';

interface GuardHarness {
	feedBlocks(count: number): void;
	challengeUrl(part: string): void;
	clearChallenges(): void;
	clockRunning(): boolean;
	completionState(): string;
	retryCompletion(): Promise<void>;
	finishTake(): Promise<void>;
}

async function startMockTake(page: Page): Promise<void> {
	await page.goto('/record?mock=1');
	await page.getByRole('button', { name: 'Start session' }).focus();
	await page.keyboard.press('Enter');
	await expect(page.getByRole('heading', { name: 'On air' })).toBeVisible();
}

async function installCompletionStub(page: Page): Promise<void> {
	await page.route('**/stems/complete', async (route) => {
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

async function endTake(page: Page): Promise<void> {
	await page.getByRole('button', { name: 'End session', exact: true }).click();
	await page.getByRole('button', { name: 'Confirm end session', exact: true }).click();
}

async function clockRunning(page: Page): Promise<boolean> {
	return page.evaluate(
		() => (window as unknown as { __mockVoice: GuardHarness }).__mockVoice.clockRunning()
	);
}

test('a challenged chunk upload names the stem upload and freezes the clock', async ({
	page
}) => {
	await startMockTake(page);
	await installCompletionStub(page);
	expect(await clockRunning(page)).toBe(true);
	await page.evaluate(() => {
		(window as unknown as { __mockVoice: GuardHarness }).__mockVoice.challengeUrl('/chunks/');
	});
	await page.evaluate((count) => {
		(window as unknown as { __mockVoice: GuardHarness }).__mockVoice.feedBlocks(count);
	}, 12);
	await endTake(page);
	await expect(page.getByRole('status')).toContainText('stem upload');
	expect(page.url()).toContain('/record');
	await expect(page.getByRole('button', { name: 'Retry draft move' })).toBeVisible();
	const state = await page.evaluate(
		() => (window as unknown as { __mockVoice: GuardHarness }).__mockVoice.completionState()
	);
	expect(state).toBe('failed');
	expect(await clockRunning(page)).toBe(false);
});

test('a challenged session end names the call and the retry finishes the take', async ({
	page
}) => {
	await startMockTake(page);
	await installCompletionStub(page);
	await page.evaluate((count) => {
		(window as unknown as { __mockVoice: GuardHarness }).__mockVoice.feedBlocks(count);
	}, 12);
	await page.evaluate(() => {
		(window as unknown as { __mockVoice: GuardHarness }).__mockVoice.challengeUrl('/api/sessions/');
	});
	await endTake(page);
	await expect(page.getByRole('status')).toContainText('session end');
	expect(page.url()).toContain('/record');
	await expect(page.getByRole('button', { name: 'Retry draft move' })).toBeVisible();
	expect(await clockRunning(page)).toBe(false);
	await page.evaluate(() => {
		(window as unknown as { __mockVoice: GuardHarness }).__mockVoice.clearChallenges();
	});
	await page.evaluate(() => {
		void (window as unknown as { __mockVoice: GuardHarness }).__mockVoice.retryCompletion();
	});
	await page.waitForURL(/processing/);
	await expect(page.getByRole('heading', { name: 'Processing' })).toBeVisible();
});

test('a challenged mint names the call and leaves the start control ready', async ({ page }) => {
	await page.route('**/api/sessions', async (route) => {
		if (route.request().method() === 'POST') {
			await route.fulfill({
				status: 200,
				contentType: 'text/html',
				body: '<html><body>challenge</body></html>'
			});
		} else {
			await route.continue();
		}
	});
	await page.goto('/record');
	await page.getByRole('button', { name: 'Start session' }).click();
	await expect(page.getByRole('status')).toContainText('session mint');
	await expect(page.getByRole('button', { name: 'Start session' })).toBeEnabled();
});
