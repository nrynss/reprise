// Review pin. A live draft whose transcript pass still runs keeps its
// job progress on the gallery card.
import { expect, test } from '@playwright/test';

test('a live draft with a running pass shows its job progress', async ({ page }) => {
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
				transcript_outcome: { job_id: 'job-7', status: 'running', error: '' },
				words: [],
				audio_url: '/media/user-blob',
				render_audio_url: ''
			}
		})
	);
	await page.route('**/api/jobs/**', (route) => route.fulfill({ status: 404, body: '' }));
	await page.goto('/');
	await expect(page.getByRole('progressbar', { name: 'Job progress for Take' })).toBeVisible();
});
