// Playwright proofs for the share and privacy pages. Both run on
// fixtures and static copy, so every proof below needs no backend.
// The share page renders its fixture states through the fixture flag,
// and the privacy page renders copy with no fetch at all. Run beside
// the routes:
// npx playwright test -c src/routes/share/share.playwright.config.ts
import { expect, test } from '@playwright/test';

const PUBLISHED = '/share/fixture-token?fixture=published';
const REVOKED = '/share/fixture-token?fixture=revoked';

test('a published fixture plays the render under its cover', async ({ page }) => {
	await page.goto(PUBLISHED);
	await expect(page.getByRole('heading', { name: 'Three weeks of almost' })).toBeVisible();
	await expect(page.getByText('EP.04')).toBeVisible();
	const cover = page.getByRole('img', { name: 'Cover of episode 4' });
	await expect(cover).toBeVisible();
	const player = page.getByRole('region', { name: 'Shared episode' }).getByLabel('Play Three weeks of almost');
	await expect(player).toBeVisible();
	await expect(player).toHaveAttribute('src', '/media/audio-fixture');
	await expect(page.getByText('Only the finished episode is public')).toBeVisible();
});

test('a revoked fixture opens nothing and leaks no player', async ({ page }) => {
	await page.goto(REVOKED);
	await expect(page.getByRole('heading', { name: 'This link opens nothing' })).toBeVisible();
	await expect(page.getByRole('img')).toHaveCount(0);
	await expect(page.locator('audio')).toHaveCount(0);
});

test('the privacy page states kept, deleted, and soft deletion', async ({ page }) => {
	await page.goto('/privacy');
	await expect(page.getByRole('heading', { name: 'What Reprise keeps, and for how long' })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Private by default' })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'What erasing removes' })).toBeVisible();
	await expect(page.getByText('the speech provider deletes softly')).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Guest recordings' })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Sessions end explicitly' })).toBeVisible();
});
