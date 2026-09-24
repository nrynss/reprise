// Playwright proofs for the welcome screen. Every proof below runs on
// scripted fixtures with no backend. Run beside the route:
// npx playwright test -c src/routes/welcome/welcome.playwright.config.ts
import { expect, test, type Page } from '@playwright/test';

const EMPTY = '/welcome';
const SEEDED = '/welcome?seed=1';

async function welcomeState(page: Page): Promise<{
	mode: string;
	playing: boolean;
	position: number;
	source: string | null;
}> {
	return page.evaluate(() => {
		const target = window as unknown as {
			__welcome?: {
				mode: () => string;
				playing: () => boolean;
				position: () => number;
				source: () => string | null;
			};
		};
		return {
			mode: target.__welcome?.mode() ?? 'missing',
			playing: target.__welcome?.playing() ?? false,
			position: target.__welcome?.position() ?? -1,
			source: target.__welcome?.source() ?? null
		};
	});
}

test('an empty catalog offers one button to record', async ({ page }) => {
	await page.goto(EMPTY);
	await expect(page.getByRole('heading', { name: 'Reprise' })).toBeVisible();
	const record = page.getByRole('link', { name: 'Record your first episode' });
	await expect(record).toBeVisible();
	await expect(record).toHaveAttribute('href', '/record');
	await expect(page.getByRole('button', { name: /thirty seconds/ })).toHaveCount(0);
});

test('a new guest reaches a live session in two clicks', async ({ page }) => {
	await page.goto(EMPTY);
	await page.getByRole('link', { name: 'Record your first episode' }).click();
	await expect(page).toHaveURL(/\/record/);
	await expect(page.getByRole('button', { name: 'Start session' })).toBeVisible();
});

test('the seeded teaser plays after the first gesture', async ({ page }) => {
	await page.goto(SEEDED);
	await expect(page.getByRole('heading', { name: 'Hear what remembering sounds like' })).toBeVisible();

	await page.getByRole('button', { name: 'Play thirty seconds of episode 4' }).click();
	await expect
		.poll(async () => (await welcomeState(page)).playing, { timeout: 10_000 })
		.toBe(true);
	const state = await welcomeState(page);
	expect(state.mode).toBe('seeded');
	expect(state.source).not.toBeNull();
	expect(state.position).toBeGreaterThanOrEqual(0);
	await expect(page.getByRole('link', { name: 'Record episode 5' })).toBeVisible();
});

test('the welcome screen passes both gates', async ({ page }) => {
	for (const route of [`${EMPTY}?gate=1`, `${SEEDED}&gate=1`]) {
		await page.goto(route);
		await expect(page.locator('#gate-status')).toHaveText(
			'Gates passed: accessibility and contrast.',
			{ timeout: 20_000 }
		);
	}
});
