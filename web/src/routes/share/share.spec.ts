// Playwright proofs for the share and privacy pages. The share page
// renders its fixture states through the fixture flag, so every proof
// below needs no server. Run beside the routes:
// npx playwright test -c src/routes/share/share.playwright.config.ts
import { expect, test } from '@playwright/test';

const PUBLISHED = '/share/fixture-token?fixture=published';
const PLAIN = '/share/fixture-token?fixture=plain';
const REVOKED = '/share/fixture-token?fixture=revoked';

const FOOTER_LINE =
	'Made with Reprise, a podcast of your own life, hosted by someone who remembers.';

test('a published fixture plays the render under its cover', async ({ page }) => {
	await page.goto(PUBLISHED);
	await expect(page.getByRole('heading', { name: 'Three weeks of almost' })).toBeVisible();
	await expect(page.getByText('EP.04')).toBeVisible();
	await expect(page.getByText('by Mara')).toBeVisible();
	const cover = page.getByRole('img', { name: 'Cover of episode 4' });
	await expect(cover).toBeVisible();
	const player = page.getByRole('region', { name: 'Shared episode' }).getByLabel('Play Three weeks of almost');
	await expect(player).toBeVisible();
	await expect(player).toHaveAttribute('src', '/media/audio-fixture');
	await expect(page.getByText(FOOTER_LINE)).toBeVisible();
	const home = page.getByRole('link', { name: 'Start your own' });
	await expect(home).toBeVisible();
	await expect(home).toHaveAttribute('href', '/');
	await expect(page.getByText(/private|public/i)).toHaveCount(0);
});

test('the published fixture carries link preview tags', async ({ page }) => {
	await page.goto(PUBLISHED);
	const head = page.locator('head');
	await expect(head.locator('meta[property="og:title"]')).toHaveAttribute(
		'content',
		'Three weeks of almost'
	);
	await expect(head.locator('meta[property="og:description"]')).toHaveAttribute(
		'content',
		'A shared episode from Reprise.'
	);
	const image = await head.locator('meta[property="og:image"]').getAttribute('content');
	if (image === null) throw new Error('The share page carries no preview image.');
	expect(image).toContain('/api/share/fixture-token/cover');
	expect(new URL(image).origin).not.toBe('');
	await expect(head.locator('meta[property="og:type"]')).toHaveAttribute('content', 'website');
	await expect(head.locator('meta[name="twitter:card"]')).toHaveAttribute(
		'content',
		'summary_large_image'
	);
});

test('a nameless fixture shows no author line', async ({ page }) => {
	await page.goto(PLAIN);
	await expect(page.getByRole('heading', { name: 'Three weeks of almost' })).toBeVisible();
	await expect(page.getByText('by Mara')).toHaveCount(0);
	const player = page.getByRole('region', { name: 'Shared episode' }).getByLabel('Play Three weeks of almost');
	await expect(player).toBeVisible();
	await expect(page.getByText(/private|public/i)).toHaveCount(0);
});

test('a revoked fixture names the dead link and the way home', async ({ page }) => {
	await page.goto(REVOKED);
	await expect(
		page.getByRole('heading', { name: 'This episode is no longer shared.' })
	).toBeVisible();
	await expect(page.getByRole('img')).toHaveCount(0);
	await expect(page.locator('audio')).toHaveCount(0);
	const home = page.getByRole('link', { name: 'Start your own' });
	await expect(home).toBeVisible();
	await expect(home).toHaveAttribute('href', '/');
	await expect(page.getByText(/private|public/i)).toHaveCount(0);
});

for (const width of [375, 768, 1280, 1920]) {
	test(`the share layout holds at ${width}px`, async ({ page }) => {
		await page.setViewportSize({ width, height: 800 });
		await page.goto(PUBLISHED);
		const scroll = await page.evaluate(() => ({
			scroll: document.documentElement.scrollWidth,
			inner: window.innerWidth
		}));
		expect(scroll.scroll, `sideways scroll at ${width}px`).toBeLessThanOrEqual(
			scroll.inner + 1
		);
		const box = await page.locator('main').boundingBox();
		if (box === null) throw new Error('The page reported no layout.');
		expect(box.width, `main width at ${width}px`).toBeLessThanOrEqual(577);
	});
}

test('the privacy page states kept, deleted, and soft deletion', async ({ page }) => {
	await page.goto('/privacy');
	await expect(page.getByRole('heading', { name: 'What Reprise keeps, and for how long' })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Private by default' })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'What erasing removes' })).toBeVisible();
	await expect(page.getByText('backups can hold a copy for a time')).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Guest recordings' })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Sessions end explicitly' })).toBeVisible();
});
