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

async function serveSeason(page: Page, rows: unknown[]): Promise<void> {
	await page.route('**/api/episodes', (route) =>
		route.fulfill({
			json: { episodes: rows }
		})
	);
	await page.route('**/api/jobs/**', (route) => route.fulfill({ status: 404, body: '' }));
}

test('the gallery header names the episodes with a badge and no sentence in demo', async ({
	page
}) => {
	await page.goto('/?fixture=1');
	await expect(page.getByRole('heading', { name: 'Your episodes' })).toBeVisible();
	await expect(page.getByText('Only you can see them until you publish.')).toBeVisible();
	await expect(page.getByText('Demo', { exact: true })).toBeVisible();
	await expect(page.getByText('Scripted')).toHaveCount(0);
	await expect(page.getByText('No backend')).toHaveCount(0);
});

test('cards in one row share one height', async ({ page }) => {
	await serveSeason(page, [
		{ id: 'e1', number: 1, title: 'Bread, again', state: 'ready', visibility: 'private' },
		{
			id: 'e2',
			number: 2,
			title: 'The long walk home through the rain with news to share',
			state: 'ready',
			visibility: 'private'
		},
		{ id: 'e3', number: 3, title: 'A quiet week', state: 'ready', visibility: 'private' },
		{
			id: 'e4',
			number: 4,
			title: 'What the neighbours said about the fence and the dog',
			state: 'ready',
			visibility: 'private'
		}
	]);
	await page.goto('/');
	const cards = page.locator('.cards .card');
	await expect(cards.first()).toBeVisible();
	const measured = await cards.evaluateAll((entries) =>
		entries.map((entry) => {
			const rect = (entry as HTMLElement).getBoundingClientRect();
			return { top: rect.top, height: rect.height };
		})
	);
	measured.sort((a, b) => a.top - b.top);
	const grouped: number[][] = [];
	for (const card of measured) {
		const open = grouped.find((group) => Math.abs(group[0] - card.top) <= 2);
		if (open) open.push(card.height);
		else grouped.push([card.top, card.height]);
	}
	for (const group of grouped) {
		const heights = group.slice(1);
		if (heights.length < 2) continue;
		expect(Math.max(...heights) - Math.min(...heights)).toBeLessThanOrEqual(2);
	}
});

test('a covered card shows its cover image and the rest keep the tile', async ({ page }) => {
	await serveSeason(page, [
		{
			id: 'e1',
			number: 1,
			title: 'Take',
			state: 'ready',
			visibility: 'private',
			cover_path: '/api/episodes/e1/cover'
		},
		{ id: 'e2', number: 2, title: 'Second', state: 'ready', visibility: 'private' }
	]);
	await page.goto('/');
	await expect(page.getByAltText('Cover of episode 1')).toHaveAttribute(
		'src',
		'/api/episodes/e1/cover'
	);
	await expect(page.getByText('EP.02')).toBeVisible();
	await expect(page.getByAltText('Cover of episode 2')).toHaveCount(0);
	await expect(page.getByText('Demo', { exact: true })).toHaveCount(0);
});

test('a running card reads its progress in plain words', async ({ page }) => {
	await page.route('**/api/episodes', (route) =>
		route.fulfill({
			json: {
				episodes: [{ id: 'e1', number: 1, title: 'Take', state: 'rendering', visibility: 'private' }]
			}
		})
	);
	await page.route('**/api/episodes/e1', (route) =>
		route.fulfill({
			json: {
				episode: { id: 'e1', number: 1, title: 'Take', state: 'rendering', visibility: 'private' },
				proposals: [],
				transcript_outcome: { job_id: 'job-t', status: 'done', error: '' },
				render_outcome: { job_id: 'job-r', status: 'running', error: '' },
				words: [],
				audio_url: '',
				render_audio_url: ''
			}
		})
	);
	await page.route('**/api/jobs/job-r', (route) =>
		route.fulfill({
			status: 200,
			contentType: 'application/json',
			body: JSON.stringify({
				jobId: 'job-r',
				status: 'running',
				stage: 'rendering',
				current: 3,
				total: 4
			})
		})
	);
	await page.route('**/api/jobs/job-r/events', (route) =>
		route.fulfill({
			status: 200,
			contentType: 'text/event-stream',
			body: 'id: 1\nevent: progress\ndata: {"job_id":"job-r","stage":"rendering","current":3,"total":4}\n\n'
		})
	);
	await page.goto('/');
	await expect(page.getByText('Rendering… 75%')).toBeVisible({ timeout: 10_000 });
	await expect(page.getByText('Working')).toHaveCount(0);
	await expect(page.getByText('survives a reload')).toHaveCount(0);
});

test('a first visit asks for its starter season before listing episodes', async ({ page }) => {
	const order: string[] = [];
	await page.route('**/api/welcome', async (route) => {
		order.push('welcome');
		await route.fulfill({ json: { mode: 'seeded', episodes: 1 } });
	});
	await page.route('**/api/episodes', async (route) => {
		order.push('episodes');
		await route.fulfill({
			json: {
				episodes: [{ id: 'e1', number: 1, title: 'Starter', state: 'ready', visibility: 'private' }]
			}
		});
	});
	await page.route('**/api/jobs/**', (route) => route.fulfill({ status: 404, body: '' }));
	await page.goto('/');
	await expect(page.getByText('Starter')).toBeVisible();
	expect(order[0]).toBe('welcome');
	expect(order.filter((name) => name === 'welcome')).toHaveLength(1);
});

test('the gallery still lists episodes when the season copy is refused', async ({ page }) => {
	await page.route('**/api/welcome', (route) => route.fulfill({ status: 500, body: '' }));
	await page.route('**/api/episodes', (route) =>
		route.fulfill({
			json: {
				episodes: [{ id: 'e1', number: 1, title: 'Starter', state: 'ready', visibility: 'private' }]
			}
		})
	);
	await page.route('**/api/jobs/**', (route) => route.fulfill({ status: 404, body: '' }));
	await page.goto('/');
	await expect(page.getByText('Starter')).toBeVisible();
});
