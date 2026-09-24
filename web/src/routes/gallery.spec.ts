// A live draft whose pass still runs keeps its job progress on the
// gallery card. The transcript pass reads done before the editorial pass
// stores its proposals. So the card opens the editor only once a title
// proposal is stored, and a failed editorial pass says so.
import { expect, test, type Page } from '@playwright/test';

const titled = [{ id: 'title-1', kind: 'title', start_word: 0, end_word: 0, reason: 'Take', decision: '' }];

async function serveDraft(
	page: Page,
	status: string,
	proposals: unknown[] = [],
	editorial?: Record<string, string>
): Promise<void> {
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
				proposals,
				transcript_outcome: { job_id: 'job-7', status, error: '' },
				...(editorial ? { editorial_outcome: editorial } : {}),
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
	await serveDraft(page, 'done', titled, { job_id: 'job-9', status: 'done', error: '' });
	await page.goto('/');
	await expect(page.getByRole('link', { name: 'Take, draft. Open in the editor.' })).toHaveAttribute(
		'href',
		'/episode/e1/edit'
	);
	await expect(page.getByRole('progressbar', { name: 'Job progress for Take' })).toHaveCount(0);
});

test('a live draft whose editorial pass has stored nothing does not open the editor', async ({ page }) => {
	await serveDraft(page, 'done');
	await page.goto('/');
	await expect(page.getByRole('heading', { name: 'Take' })).toBeVisible();
	await expect(page.getByRole('link', { name: 'Take, draft. Open in the editor.' })).toHaveCount(0);
	await expect(page.getByText('The transcript is stored, but the editorial pass never started.')).toBeVisible();
});

test('a live draft whose editorial pass failed says so on its card', async ({ page }) => {
	await serveDraft(page, 'done', [], { job_id: 'job-9', status: 'error', error: 'budget refused' });
	await page.goto('/');
	await expect(page.getByText('The editorial pass failed: budget refused.')).toBeVisible();
	await expect(page.getByRole('link', { name: 'Take, draft. Open in the editor.' })).toHaveCount(0);
});
