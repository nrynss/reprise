// Playwright pins for the standing end control on the record page. At mount
// the snapshot is preflight, so the live section is not rendered yet and a
// one time lookup cannot reach the live button. These proofs drive the mock
// take, press the live rendered End session button by mouse and by keyboard,
// and expect the pause confirmation. Cancel leaves the take on air. The pause
// proofs read the clock off the mock context, never off wall time. This spec
// runs with the mock suite beside the route:
// npx playwright test -c src/routes/record/mock.playwright.config.ts end-control.spec.ts

import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';

interface PauseHarness {
	feedBlocks(count: number): void;
	clockNow(): number;
	pausedSocketSamples(): number[];
	sessionEndCount(): number;
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

async function clockText(page: Page): Promise<string> {
	const text = await page.getByLabel('Elapsed time').textContent();
	return (text ?? '').trim();
}

async function mockClock(page: Page): Promise<number> {
	return page.evaluate(() => {
		return (window as unknown as { __mockVoice: PauseHarness }).__mockVoice.clockNow();
	});
}

function toSeconds(text: string): number {
	const parts = text.split(':');
	const minutes = Number(parts[0] ?? '0');
	const seconds = Number(parts[1] ?? '0');
	return minutes * 60 + seconds;
}

test('the standing end control arms on mouse click', async ({ page }) => {
	await startMockTake(page);
	const endButton = page.getByRole('button', { name: 'End session', exact: true });
	await expect(endButton).toBeEnabled();
	await endButton.click();
	await expect(page.getByRole('button', { name: 'Confirm end session', exact: true })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Cancel end', exact: true })).toBeVisible();
	await expect(page.getByRole('status')).toContainText('Paused. End this take? Cancel resumes it.');
});

test('the standing end control arms on keyboard Enter', async ({ page }) => {
	await startMockTake(page);
	const endButton = page.getByRole('button', { name: 'End session', exact: true });
	await expect(endButton).toBeEnabled();
	await endButton.focus();
	await expect(endButton).toBeFocused();
	await page.keyboard.press('Enter');
	await expect(page.getByRole('button', { name: 'Confirm end session', exact: true })).toBeVisible();
	await expect(page.getByRole('status')).toContainText('Paused. End this take? Cancel resumes it.');
});

test('cancel keeps the take recording', async ({ page }) => {
	await startMockTake(page);
	await page.getByRole('button', { name: 'End session', exact: true }).click();
	await page.getByRole('button', { name: 'Cancel end', exact: true }).click();
	await expect(page.getByRole('heading', { name: 'On air' })).toBeVisible();
	await expect(page.getByRole('button', { name: 'End session', exact: true })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Confirm end session', exact: true })).toHaveCount(0);
	await expect(page.getByRole('status')).toContainText('On air');
});

test('the paused take freezes the clock and sends only silence', async ({ page }) => {
	await startMockTake(page);
	await page.evaluate(() => {
		(window as unknown as { __mockVoice: PauseHarness }).__mockVoice.feedBlocks(6);
	});
	await page.getByRole('button', { name: 'End session', exact: true }).click();
	await expect(page.getByRole('status')).toContainText('Paused. End this take? Cancel resumes it.');
	// Let one clock tick land, then read the frozen display three ticks apart.
	// The mock context keeps running between the reads, so equal text means
	// the display froze, not the clock.
	await page.waitForTimeout(600);
	const first = await clockText(page);
	const contextAtPause = await mockClock(page);
	await page.waitForTimeout(600);
	expect(await clockText(page)).toBe(first);
	await page.waitForTimeout(600);
	expect(await clockText(page)).toBe(first);
	expect(await mockClock(page)).toBeGreaterThan(contextAtPause);
	// Blocks fed while paused reach the socket as zeros only. No close message
	// leaves, so the provider connection stays open.
	await page.evaluate(() => {
		(window as unknown as { __mockVoice: PauseHarness }).__mockVoice.feedBlocks(4);
	});
	const seen = await page.evaluate(() => {
		const mock = (window as unknown as { __mockVoice: PauseHarness }).__mockVoice;
		return {
			samples: mock.pausedSocketSamples(),
			sessionEnds: mock.sessionEndCount()
		};
	});
	expect(seen.samples.length).toBeGreaterThan(0);
	expect(seen.samples.every((sample) => sample === 0)).toBe(true);
	expect(seen.sessionEnds).toBe(0);
});

test('cancel resumes the clock from where it stopped', async ({ page }) => {
	await startMockTake(page);
	await page.evaluate(() => {
		(window as unknown as { __mockVoice: PauseHarness }).__mockVoice.feedBlocks(6);
	});
	await page.getByRole('button', { name: 'End session', exact: true }).click();
	await expect(page.getByRole('status')).toContainText('Paused. End this take? Cancel resumes it.');
	await page.waitForTimeout(600);
	const frozen = await clockText(page);
	await page.getByRole('button', { name: 'Cancel end', exact: true }).click();
	await expect(page.getByRole('status')).toContainText('On air. The host hears you.');
	// The display moves again from the frozen reading on the mock context.
	let resumed = frozen;
	for (let attempt = 0; attempt < 20 && resumed === frozen; attempt += 1) {
		await page.waitForTimeout(500);
		resumed = await clockText(page);
	}
	expect(resumed).not.toBe(frozen);
	expect(toSeconds(resumed)).toBeGreaterThan(toSeconds(frozen));
});
