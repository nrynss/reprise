// Review pin. The transcript job reads done as soon as it has scheduled
// the editorial pass, and every finished editorial pass stores at least a
// title proposal. So a live draft with a done transcript pass and no
// stored proposal is a draft whose editorial pass still runs. Its card
// must not offer the editor as a finished draft yet.
import { expect, test } from '@playwright/test';

test('a live draft whose editorial pass has stored nothing does not open the editor', async ({ page }) => {
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
				transcript_outcome: { job_id: 'job-7', status: 'done', error: '' },
				words: [{ text: 'Hello', start: 0, end: 0.5 }],
				audio_url: '/media/user-blob',
				render_audio_url: '',
				render_words: []
			}
		})
	);
	await page.route('**/api/jobs/**', (route) => route.fulfill({ status: 404, body: '' }));
	await page.goto('/');
	await expect(page.getByRole('heading', { name: 'Take' })).toBeVisible();
	await expect(page.getByRole('link', { name: 'Take, draft. Open in the editor.' })).toHaveCount(0);
});
