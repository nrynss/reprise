// Playwright proofs for the gallery, the episode view, and the thread
// panel. Every proof below runs on scripted fixtures with no backend,
// and the mock render job streams through the real job follower. Run
// beside the routes:
// npx playwright test -c src/routes/threads/threads.playwright.config.ts
import { expect, test, type Page } from '@playwright/test';

const GALLERY = '/?fixture=1';
const THREADS = '/threads?fixture=1';

async function episodeState(page: Page): Promise<{ position: number; playing: boolean }> {
	return page.evaluate(() => {
		const target = window as unknown as {
			__episode?: { position: () => number; playing: () => boolean };
		};
		return {
			position: target.__episode?.position() ?? -1,
			playing: target.__episode?.playing() ?? false
		};
	});
}

test('a thread quote opens the episode with the playhead at its quote', async ({ page }) => {
	await page.goto(THREADS);
	await expect(page.getByRole('heading', { name: 'What keeps coming up' })).toBeVisible();

	await page.getByRole('link', { name: /ask June whose job the fence is now/ }).click();

	await expect(page).toHaveURL(/\/episode\/ep-2\?.*t=20/);
	await expect(page.getByRole('heading', { name: 'Rent, and what it costs to stay' })).toBeVisible();
	await expect(page.getByRole('status', { name: 'Playback position' })).toHaveText(
		'0:20 of 1:10'
	);

	await page.getByRole('button', { name: 'Play Rent, and what it costs to stay' }).click();
	await expect.poll(async () => (await episodeState(page)).playing, { timeout: 10_000 }).toBe(true);
	const state = await episodeState(page);
	expect(state.position).toBeGreaterThanOrEqual(19.5);
});

test('gallery progress never moves backwards across a reload', async ({ page }) => {
	await page.goto(GALLERY);
	const bar = page.getByRole('progressbar', { name: /Render progress/ });
	await expect(bar).toBeVisible();
	await expect
		.poll(async () => Number(await bar.getAttribute('aria-valuenow')), { timeout: 10_000 })
		.toBeGreaterThanOrEqual(50);
	const before = Number(await bar.getAttribute('aria-valuenow'));

	await page.reload();
	const afterBar = page.getByRole('progressbar', { name: /Render progress/ });
	await expect(afterBar).toBeVisible();
	const after = Number(await afterBar.getAttribute('aria-valuenow'));
	expect(after).toBeGreaterThanOrEqual(before);
});

test('the gallery lists episodes newest first with states', async ({ page }) => {
	await page.goto(GALLERY);
	const list = page.getByRole('list', { name: 'Episodes, newest first' });
	await expect(list).toBeVisible();
	const titles = await list.getByRole('heading', { level: 2 }).allTextContents();
	expect(titles[0]).toBe('The only place nobody needs anything');
	expect(titles[titles.length - 1]).toBe('The garden was ours first');
	await expect(page.getByText('Rendering · 75%', { exact: false })).toBeVisible({ timeout: 10_000 });
});

test('the episode transcript seeks on word click', async ({ page }) => {
	await page.goto('/episode/ep-4?fixture=1');
	await expect(page.getByRole('heading', { name: 'Three weeks of almost' })).toBeVisible();
	await page.getByRole('button', { name: 'Nothing. Activate to seek.' }).click();
	await expect(page.getByRole('status', { name: 'Playback position' })).toHaveText('0:14 of 2:00');
});

test('the episode renders its release states without backend actions', async ({ page }) => {
	await page.goto('/episode/ep-4?fixture=1');
	await expect(page.getByText('Private', { exact: true }).first()).toBeVisible();
	await page.getByRole('button', { name: 'Publish…' }).click();
	await expect(page.getByText('Fixture link:', { exact: false })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Revoke link' })).toBeVisible();

	await page.getByRole('button', { name: 'Erase this episode' }).click();
	await expect(page.getByRole('button', { name: 'Confirm erase' })).toBeVisible();
	await page.getByRole('button', { name: 'Confirm erase' }).click();
	await expect(page.getByText('The erase endpoint refused, so the fixture episode stays.')).toBeVisible();
});

test('the season screens pass both gates', async ({ page }) => {
	for (const route of [`${GALLERY}&gate=1`, '/episode/ep-4?fixture=1&gate=1', `${THREADS}&gate=1`]) {
		await page.goto(route);
		await expect(page.locator('#gate-status')).toHaveText(
			'Gates passed: accessibility and contrast.',
			{ timeout: 20_000 }
		);
	}
});
