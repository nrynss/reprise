// Playwright proofs for the cap wiring on the record page. The page runs
// under the mock flag, so the take opens against doubles with generated
// input and no microphone. The mock take drives the cap through a harness
// clock the run moves by hand, so neither proof below waits on a real
// duration. This spec runs with the mock suite beside the route:
// npx playwright test -c src/routes/record/mock.playwright.config.ts cap.spec.ts

import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';

interface CapHarness {
	sessionEndCount(): number;
	capAdvance(ms: number): void;
	capJump(ms: number): void;
	capInfo(): { warning: boolean; text: string; remainingSeconds: number };
}

async function startMockTake(page: Page): Promise<void> {
	await page.goto('/record?mock=1');
	await page.getByRole('button', { name: 'Start session' }).focus();
	await page.keyboard.press('Enter');
	await expect(page.getByRole('heading', { name: 'On air' })).toBeVisible();
}

async function capInfo(page: Page): Promise<{
	warning: boolean;
	text: string;
	remainingSeconds: number;
}> {
	return page.evaluate(() => {
		const mock = (window as unknown as { __mockVoice: CapHarness }).__mockVoice;
		return mock.capInfo();
	});
}

test('the timer ends the take past the cap with nobody pressing end', async ({ page }) => {
	await startMockTake(page);
	const opened = await capInfo(page);
	expect(opened.warning).toBe(false);
	expect(opened.remainingSeconds).toBeGreaterThan(61);

	// Move to one minute and one second left. No warning yet.
	await page.evaluate((ms) => {
		(window as unknown as { __mockVoice: CapHarness }).__mockVoice.capAdvance(ms);
	}, (opened.remainingSeconds - 61) * 1000);
	const quiet = await capInfo(page);
	expect(quiet.warning).toBe(false);

	// One more second reaches the warn point. The screen announces the end
	// in a live region while the end control stays keyboard reachable.
	await page.evaluate(() => {
		(window as unknown as { __mockVoice: CapHarness }).__mockVoice.capAdvance(1000);
	});
	const warned = await capInfo(page);
	expect(warned.warning).toBe(true);
	expect(warned.remainingSeconds).toBe(60);
	await expect(page.getByRole('alert')).toContainText('60 seconds');
	const endButton = page.getByRole('button', { name: 'End session', exact: true });
	await expect(endButton).toBeEnabled();
	await endButton.focus();
	await expect(endButton).toBeFocused();
	await expect(page.getByRole('button', { name: 'End session now', exact: true })).toBeVisible();

	// Past the cap the timer ends the take on its own. Nobody pressed end.
	const ends = await page.evaluate(async () => {
		const mock = (window as unknown as { __mockVoice: CapHarness }).__mockVoice;
		mock.capAdvance(61_000);
		await new Promise((resolve) => setTimeout(resolve, 0));
		await new Promise((resolve) => setTimeout(resolve, 0));
		return mock.sessionEndCount();
	});
	expect(ends).toBe(1);
	await page.waitForURL(/processing/);
	await expect(page.getByRole('heading', { name: 'Processing' })).toBeVisible();
});

test('a suspended page past the cap ends on wake', async ({ page }) => {
	await startMockTake(page);
	const opened = await capInfo(page);

	// Jump past the cap with no timer running, the way a sleeping laptop
	// leaves the page. Nothing ends until the tab wakes.
	const jumped = await page.evaluate((ms) => {
		const mock = (window as unknown as { __mockVoice: CapHarness }).__mockVoice;
		mock.capJump(ms);
		return { ends: mock.sessionEndCount(), warning: mock.capInfo().warning };
	}, opened.remainingSeconds * 1000 + 100_000);
	expect(jumped.ends).toBe(0);
	expect(jumped.warning).toBe(false);

	// The visibility change reaches the controller, which wakes the cap.
	await page.evaluate(() => {
		document.dispatchEvent(new Event('visibilitychange'));
	});
	const ends = await page.evaluate(async () => {
		const mock = (window as unknown as { __mockVoice: CapHarness }).__mockVoice;
		await new Promise((resolve) => setTimeout(resolve, 0));
		await new Promise((resolve) => setTimeout(resolve, 0));
		return mock.sessionEndCount();
	});
	expect(ends).toBe(1);
	await page.waitForURL(/processing/);
	await expect(page.getByRole('heading', { name: 'Processing' })).toBeVisible();
});
