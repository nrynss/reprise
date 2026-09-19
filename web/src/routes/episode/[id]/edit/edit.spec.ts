// Playwright proofs for the editor screen. The page runs on the scripted
// draft, so every proof below needs no backend and no real clock. This
// spec runs with the suite beside the route:
// npx playwright test -c src/routes/episode/[id]/edit/edit.playwright.config.ts
import { expect, test } from '@playwright/test';

const DRAFT = '/episode/draft-1/edit?fixture=1';

test('reverting a cut writes its decision row', async ({ page }) => {
	await page.goto(DRAFT);
	await expect(page.getByRole('heading', { name: 'The only place nobody needs anything' })).toBeVisible();
	await expect(page.getByRole('status', { name: 'Applied cuts' })).toHaveText('3 cuts applied');

	await page.getByRole('button', { name: 'Revert cut: False start at the top of the answer.' }).click();

	await expect(page.getByRole('status', { name: 'Applied cuts' })).toHaveText('2 cuts applied');
	const decisions = page.getByRole('region', { name: 'Decisions' });
	await expect(decisions.getByText('Reverted: False start at the top of the answer. (prop-cut-1)')).toBeVisible();
});

test('reverting each non-cut proposal persists its decision row', async ({ page }) => {
	await page.goto(DRAFT);
	await expect(page.getByRole('heading', { name: 'The only place nobody needs anything' })).toBeVisible();

	await page.getByRole('button', { name: 'Revert cold open proposal' }).click();
	await expect(page.getByText('Cold open reverted. The episode starts at the top.')).toBeVisible();

	await page.getByRole('button', { name: 'Revert title proposal' }).click();
	await expect(page.getByRole('heading', { name: 'Episode draft-1' })).toBeVisible();

	await page.getByRole('button', { name: 'Revert show notes proposal' }).click();
	await expect(page.getByText('Show notes reverted. Nothing stands in their place.')).toBeVisible();

	const callback = page.getByRole('button', { name: 'Revert callback proposal' });
	await callback.focus();
	await expect(callback).toBeFocused();
	await page.keyboard.press('Enter');
	await expect(page.getByText('Callback reverted and cleared from the next opening.')).toBeVisible();

	const decisions = page.getByRole('region', { name: 'Decisions' });
	await expect(decisions.getByText('(prop-cold-open)')).toBeVisible();
	await expect(decisions.getByText('(prop-title)')).toBeVisible();
	await expect(decisions.getByText('(prop-notes)')).toBeVisible();
	await expect(decisions.getByText('(prop-callback)')).toBeVisible();
});

test('a keyboard-only run completes the edit and marks done', async ({ page }) => {
	await page.goto(DRAFT);
	await expect(page.getByRole('status', { name: 'Applied cuts' })).toHaveText('3 cuts applied');

	const revert = page.getByRole('button', { name: 'Revert cut: Bus timetable tangent that goes nowhere.' });
	await revert.focus();
	await expect(revert).toBeFocused();
	await page.keyboard.press('Enter');
	await expect(page.getByRole('status', { name: 'Applied cuts' })).toHaveText('2 cuts applied');

	const preview = page.getByRole('button', { name: 'Preview the cold open' });
	await preview.focus();
	await page.keyboard.press('Enter');

	const done = page.getByRole('button', { name: 'Mark episode done' });
	await done.focus();
	await page.keyboard.press('Enter');
	const confirm = page.getByRole('button', { name: 'Confirm mark done' });
	await expect(confirm).toBeVisible();
	await confirm.focus();
	await page.keyboard.press('Enter');
	await expect(page.getByText('Render running: fixture render.')).toBeVisible();
});

test('clicking a word seeks the readout', async ({ page }) => {
	await page.goto(DRAFT);
	await expect(page.getByRole('status', { name: 'Applied cuts' })).toHaveText('3 cuts applied');
	await page.getByRole('button', { name: 'shop. Activate to seek.' }).click();
	await expect(page.getByRole('status', { name: 'Playback position' })).toHaveText('0:03 of 0:24');
});

test('the screen passes both gates', async ({ page }) => {
	await page.goto(`${DRAFT}&gate=1`);
	await expect(page.locator('#gate-status')).toHaveText('Gates passed: accessibility and contrast.', {
		timeout: 20_000
	});
});
