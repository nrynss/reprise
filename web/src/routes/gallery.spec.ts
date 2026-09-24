// A live draft whose pass still runs keeps its job progress on the
// gallery card. Once the pass is done the card opens the editor.
import { expect, test, type Page } from '@playwright/test';

async function serveDraft(page: Page, status: string): Promise<void> {
	await page.route('**/api/episodes', (route) =>
		route.fulfill({
			json: {
				episodes: [{ id: 'e1', number: 1, title: 'Take', state: 'draft', visibility: 'private' }]
			}
		})
	);
	await page.route('**/api/episodes/e1', (route) =>
		route.fulfill({
			json: {
				episode: { id: 'e1', number: 1, title: 'Take', state: 'draft', visibility: 'private' },
				proposals: [],
				transcript_outcome: { job_id: 'job-7', status, error: '' },
				words: [],
				audio_url: '/media/user-blob',
				render_audio_url: ''
			}
		})
	);
	await page.route('**/api/jobs/**', (route) => route.fulfill({ status: 404, body: '' }));
}

test('a live draft with a running pass shows its job progress', async ({ page }) => {
	await serveDraft(page, 'running');
	await page.goto('/');
	await expect(page.getByRole('progressbar', { name: 'Job progress for Take' })).toBeVisible();
	await expect(page.getByRole('link', { name: 'Open the episode' })).toHaveAttribute('href', '/episode/e1');
	await expect(page.getByRole('link', { name: 'Take, draft. Open in the editor.' })).toHaveCount(0);
});

test('a live draft with a finished pass opens the editor', async ({ page }) => {
	await serveDraft(page, 'done');
	await page.goto('/');
	await expect(page.getByRole('link', { name: 'Take, draft. Open in the editor.' })).toHaveAttribute(
		'href',
		'/episode/e1/edit'
	);
	await expect(page.getByRole('progressbar', { name: 'Job progress for Take' })).toHaveCount(0);
});
