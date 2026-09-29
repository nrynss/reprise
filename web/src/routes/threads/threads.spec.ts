// Playwright proofs for the gallery, the episode view, and the thread
// panel. Fixture proofs run behind the fixture flag with generated
// audio and the mock render job. Live proofs stub the season API the
// wired handlers answer and assert the same screens on real rows.
// npx playwright test -c src/routes/threads/threads.playwright.config.ts
import { expect, test, type Page, type Route } from '@playwright/test';

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

interface LiveRow {
	id: string;
	number: number;
	title: string;
	state: string;
	visibility: string;
}

function seasonBody(rows: LiveRow[]): string {
	return JSON.stringify({ episodes: rows });
}

function detailBody(row: LiveRow, outcome: unknown, proposals: unknown[] = []): string {
	return JSON.stringify({
		episode: row,
		proposals,
		...(outcome === null ? {} : { transcript_outcome: outcome })
	});
}

async function stubEpisodeApi(
	page: Page,
	rows: LiveRow[],
	details: Record<string, { outcome: unknown; proposals?: unknown[] }>
): Promise<void> {
	await page.route(/\/api\/episodes(\/.*)?$/, (route: Route) => {
		const url = new URL(route.request().url());
		if (url.pathname === '/api/episodes') {
			void route.fulfill({
				status: 200,
				contentType: 'application/json',
				body: seasonBody(rows)
			});
			return;
		}
		const id = url.pathname.split('/').pop() ?? '';
		const detail = details[id];
		if (!detail) {
			void route.fulfill({ status: 404, contentType: 'application/json', body: '{}' });
			return;
		}
		const row = rows.find((candidate) => candidate.id === id);
		if (!row) {
			void route.fulfill({ status: 404, contentType: 'application/json', body: '{}' });
			return;
		}
		void route.fulfill({
			status: 200,
			contentType: 'application/json',
			body: detailBody(row, detail.outcome, detail.proposals ?? [])
		});
	});
}

test('gallery lists live episodes newest first with no fixture rows', async ({ page }) => {
	await stubEpisodeApi(
		page,
		[
			{ id: 'e1', number: 1, title: 'First real', state: 'ready', visibility: 'private' },
			{ id: 'e2', number: 2, title: 'Second real', state: 'draft', visibility: 'private' },
			{ id: 'e3', number: 3, title: 'Third real', state: 'ready', visibility: 'public' }
		],
		{
			e2: { outcome: null }
		}
	);
	await page.goto('/');
	const list = page.getByRole('list', { name: 'Episodes, newest first' });
	await expect(list).toBeVisible();
	const titles = await list.getByRole('heading', { level: 2 }).allTextContents();
	expect(titles).toEqual(['Third real', 'Second real', 'First real']);
	await expect(page.getByText('Scripted season')).toHaveCount(0);
	await expect(page.getByText('The only place nobody needs anything')).toHaveCount(0);
});

test('a running live card follows its detail job through the stream', async ({ page }) => {
	await stubEpisodeApi(
		page,
		[
			{ id: 'e1', number: 1, title: 'First real', state: 'ready', visibility: 'private' },
			{ id: 'e2', number: 2, title: 'Second real', state: 'rendering', visibility: 'private' }
		],
		{
			e2: { outcome: { job_id: 'job-9', status: 'running', error: '' } }
		}
	);
	await page.route('**/api/jobs/job-9', (route: Route) =>
		route.fulfill({
			status: 200,
			contentType: 'application/json',
			body: JSON.stringify({ jobId: 'job-9', status: 'running', stage: 'rendering', current: 1, total: 4 })
		})
	);
	await page.route('**/api/jobs/job-9/events', (route: Route) =>
		route.fulfill({
			status: 200,
			contentType: 'text/event-stream',
			body: 'id: 1\nevent: progress\ndata: {"job_id":"job-9","stage":"rendering","current":3,"total":4}\n\n'
		})
	);
	await page.goto('/');
	const bar = page.getByRole('progressbar', { name: /Job progress for Second real/ });
	await expect(bar).toBeVisible();
	await expect
		.poll(async () => Number(await bar.getAttribute('aria-valuenow')), { timeout: 10_000 })
		.toBeGreaterThanOrEqual(75);
	const before = Number(await bar.getAttribute('aria-valuenow'));

	await page.reload();
	const afterBar = page.getByRole('progressbar', { name: /Job progress for Second real/ });
	await expect(afterBar).toBeVisible();
	await expect
		.poll(async () => Number(await afterBar.getAttribute('aria-valuenow')), { timeout: 10_000 })
		.toBeGreaterThanOrEqual(before);
});

test('a live thread quote opens the episode at its quoted moment', async ({ page }) => {
	await page.route('**/api/threads', (route: Route) =>
		route.fulfill({
			status: 200,
			contentType: 'application/json',
			body: JSON.stringify({
				name_threads: [
					{
						key: 'june',
						display: 'June',
						kind: 'person',
						episodes: [{ episode_id: 'e1', number: 1, quote: 'I keep the garden.', offset: 5 }],
						mention_count: 2,
						episode_count: 1
					}
				],
				circled_topics: []
			})
		})
	);
	await stubEpisodeApi(
		page,
		[{ id: 'e1', number: 1, title: 'First real', state: 'ready', visibility: 'private' }],
		{
			e1: {
				outcome: null,
				proposals: [
					{ id: 'p1', kind: 'cut', start_word: 0, end_word: 10, reason: 'Trim the pause.', decision: '' }
				]
			}
		}
	);
	await page.goto('/threads');
	await expect(page.getByRole('heading', { name: 'What keeps coming up' })).toBeVisible();
	await expect(page.getByText('June and I share the allotment')).toHaveCount(0);

	await page.getByRole('link', { name: /I keep the garden/ }).click();

	await expect(page).toHaveURL(/\/episode\/e1\?w=5/);
	await expect(page.getByRole('heading', { name: 'First real' })).toBeVisible();
	await expect(page.getByText('Quoted moment at word 5.')).toBeVisible();
	await expect(page.getByRole('link', { name: /I keep the garden/ })).toBeVisible();
	await expect(page.getByText('Trim the pause.')).toBeVisible();
});

test('an empty live season falls back to the record copy', async ({ page }) => {
	await stubEpisodeApi(page, [], {});
	await page.goto('/');
	await expect(page.getByRole('heading', { name: 'No episodes yet' })).toBeVisible();
	await expect(page.getByRole('link', { name: 'Record the first episode' })).toBeVisible();

	await page.route('**/api/threads', (route: Route) =>
		route.fulfill({
			status: 200,
			contentType: 'application/json',
			body: JSON.stringify({ name_threads: [], circled_topics: [] })
		})
	);
	await page.goto('/threads');
	await expect(page.getByText('Nothing threads yet')).toBeVisible();
});

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
	const bar = page.getByRole('progressbar', { name: /Job progress/ });
	await expect(bar).toBeVisible();
	await expect
		.poll(async () => Number(await bar.getAttribute('aria-valuenow')), { timeout: 10_000 })
		.toBeGreaterThanOrEqual(50);
	const before = Number(await bar.getAttribute('aria-valuenow'));

	await page.reload();
	const afterBar = page.getByRole('progressbar', { name: /Job progress/ });
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
	await expect(page.getByText('Rendering… 75%')).toBeVisible({ timeout: 10_000 });
});

test('the episode transcript seeks on word click', async ({ page }) => {
	await page.goto('/episode/ep-4?fixture=1');
	await expect(page.getByRole('heading', { name: 'Three weeks of almost' })).toBeVisible();
	await page.getByRole('button', { name: 'Nothing. Activate to seek.' }).click();
	await expect(page.getByRole('status', { name: 'Playback position' })).toHaveText('0:14 of 2:00');
});

test('the episode shows its cover, a bare release row, and erases to the gallery', async ({
	page
}) => {
	await page.goto('/episode/ep-4?fixture=1');
	await expect(page.getByRole('heading', { name: 'Three weeks of almost' })).toBeVisible();
	await expect(page.locator('.cover').first()).toBeVisible();
	await expect(page.locator('p.eyebrow')).toHaveText('EP.04');
	await expect(page.getByText('Private', { exact: true })).toHaveCount(0);
	await expect(page.getByText('Public', { exact: true })).toHaveCount(0);
	await expect(page.getByText('Demo', { exact: true })).toBeVisible();

	const row = page.locator('section[aria-label="Release"]');
	await row.getByRole('button', { name: 'Publish', exact: true }).click();
	await expect(row.getByRole('link').first()).toContainText('/episode/ep-4?fixture=1');
	await expect(row.getByRole('button', { name: 'Copy link' })).toBeVisible();
	await expect(row.getByRole('button', { name: 'Revoke', exact: true })).toBeVisible();
	await expect(row.getByText('Only the finished audio')).toHaveCount(0);

	await row.getByRole('button', { name: 'Revoke', exact: true }).click();
	await expect(row.getByRole('button', { name: 'Publish', exact: true })).toBeVisible();

	await page.getByRole('button', { name: 'Erase this episode' }).click();
	await expect(page.getByRole('button', { name: 'Confirm erase' })).toBeVisible();
	await page.getByRole('button', { name: 'Confirm erase' }).click();
	await expect(page).toHaveURL(/\/\?fixture=1&erased=1/);
	await expect(page.getByText('Episode erased.')).toBeVisible();
});

test('a live episode walks its release controls against the routes', async ({ page }) => {
	await stubEpisodeApi(
		page,
		[{ id: 'e9', number: 9, title: 'Ninth real', state: 'ready', visibility: 'private' }],
		{ e9: { outcome: null } }
	);
	await page.route('**/api/episodes/e9/publish', (route: Route) => {
		if (route.request().method() === 'DELETE') {
			void route.fulfill({ status: 200, contentType: 'application/json', body: '{}' });
			return;
		}
		void route.fulfill({
			status: 200,
			contentType: 'application/json',
			body: JSON.stringify({ share_path: '/share/token-1' })
		});
	});
	await page.route('**/api/episodes/e9', (route: Route) => {
		if (route.request().method() === 'DELETE') {
			void route.fulfill({
				status: 202,
				contentType: 'application/json',
				body: JSON.stringify({ job_id: 'erase-7' })
			});
			return;
		}
		void route.fallback();
	});
	await page.goto('/episode/e9');
	await expect(page.getByRole('heading', { name: 'Ninth real' })).toBeVisible();
	await expect(page.locator('p.eyebrow')).toHaveText('EP.09');

	const row = page.locator('section[aria-label="Release"]');
	await row.getByRole('button', { name: 'Publish', exact: true }).click();
	await expect(row.getByRole('link').first()).toContainText('/share/token-1');
	await expect(row.getByRole('button', { name: 'Copy link' })).toBeVisible();
	await expect(row.getByRole('button', { name: 'Revoke', exact: true })).toBeVisible();
	await expect(page.getByText('Public at')).toHaveCount(0);
	await expect(page.getByText('Only the finished audio')).toHaveCount(0);

	await row.getByRole('button', { name: 'Revoke', exact: true }).click();
	await expect(row.getByRole('button', { name: 'Publish', exact: true })).toBeVisible();

	await page.getByRole('button', { name: 'Erase this episode' }).click();
	await expect(page.getByRole('button', { name: 'Confirm erase' })).toBeVisible();
	await page.getByRole('button', { name: 'Confirm erase' }).click();
	await expect(page).toHaveURL(/\/\?erased=1/);
	await expect(page.getByText('Episode erased.')).toBeVisible();
});

test('an erase that answers 404 still lands on the gallery', async ({ page }) => {
	await stubEpisodeApi(
		page,
		[{ id: 'e9', number: 9, title: 'Ninth real', state: 'ready', visibility: 'private' }],
		{ e9: { outcome: null } }
	);
	await page.route('**/api/episodes/e9', (route: Route) => {
		if (route.request().method() === 'DELETE') {
			void route.fulfill({ status: 404, contentType: 'application/json', body: '{}' });
			return;
		}
		void route.fallback();
	});
	await page.goto('/episode/e9');
	await expect(page.getByRole('heading', { name: 'Ninth real' })).toBeVisible();

	await page.getByRole('button', { name: 'Erase this episode' }).click();
	await page.getByRole('button', { name: 'Confirm erase' }).click();
	await expect(page).toHaveURL(/\/\?erased=1/);
	await expect(page.getByText('Episode erased.')).toBeVisible();
});

test('a refused erase stays on the episode with the retry', async ({ page }) => {
	await stubEpisodeApi(
		page,
		[{ id: 'e9', number: 9, title: 'Ninth real', state: 'ready', visibility: 'private' }],
		{ e9: { outcome: null } }
	);
	await page.route('**/api/episodes/e9', (route: Route) => {
		if (route.request().method() === 'DELETE') {
			void route.fulfill({ status: 500, contentType: 'application/json', body: '{}' });
			return;
		}
		void route.fallback();
	});
	await page.goto('/episode/e9');
	await expect(page.getByRole('heading', { name: 'Ninth real' })).toBeVisible();

	await page.getByRole('button', { name: 'Erase this episode' }).click();
	await page.getByRole('button', { name: 'Confirm erase' }).click();
	await expect(page.getByText("Couldn't erase this episode. Try again.")).toBeVisible();
	await expect(page).toHaveURL(/\/episode\/e9/);
});

test('a failed take shows one plain line and never the raw error', async ({ page }) => {
	await stubEpisodeApi(
		page,
		[{ id: 'e9', number: 9, title: 'Ninth real', state: 'failed', visibility: 'private' }],
		{
			e9: {
				outcome: { job_id: 'job-t', status: 'error', error: 'raw-boom-xyz' }
			}
		}
	);
	await page.route('**/api/threads', (route: Route) =>
		route.fulfill({
			status: 200,
			contentType: 'application/json',
			body: JSON.stringify({ name_threads: [], circled_topics: [] })
		})
	);
	await page.goto('/episode/e9');
	await expect(page.getByRole('heading', { name: 'Ninth real' })).toBeVisible();
	await expect(page.getByText("This take couldn't be transcribed.")).toBeVisible();
	await expect(page.getByText('raw-boom-xyz')).toHaveCount(0);
	await expect(page.getByText('Transcript pass')).toHaveCount(0);
});

test('a draft with no audio says so in three words', async ({ page }) => {
	await stubEpisodeApi(
		page,
		[{ id: 'e9', number: 9, title: 'Ninth real', state: 'draft', visibility: 'private' }],
		{ e9: { outcome: null } }
	);
	await page.route('**/api/threads', (route: Route) =>
		route.fulfill({
			status: 200,
			contentType: 'application/json',
			body: JSON.stringify({ name_threads: [], circled_topics: [] })
		})
	);
	await page.goto('/episode/e9');
	await expect(page.getByRole('heading', { name: 'Ninth real' })).toBeVisible();
	await expect(page.getByText('No audio yet.')).toBeVisible();
});

test('the episode page holds its column at every width', async ({ page }) => {
	for (const width of [375, 768, 1280, 1920]) {
		await page.setViewportSize({ width, height: 800 });
		await page.goto('/episode/ep-4?fixture=1');
		await expect(page.getByRole('heading', { name: 'Three weeks of almost' })).toBeVisible();
		const scroll = await page.evaluate(() => ({
			scroll: document.documentElement.scrollWidth,
			inner: window.innerWidth
		}));
		expect(scroll.scroll, `sideways scroll at ${width}px`).toBeLessThanOrEqual(scroll.inner + 1);
		const box = await page.locator('main').boundingBox();
		if (box === null) throw new Error('The episode page reported no layout.');
		expect(box.width, `main width at ${width}px`).toBeLessThanOrEqual(1025);
	}
	await page.setViewportSize({ width: 375, height: 800 });
	await page.goto('/episode/ep-4?fixture=1');
	await expect(page.getByRole('button', { name: 'Publish', exact: true })).toBeVisible();
	const heights = await page
		.locator('section[aria-label="Episode playback"] button, section[aria-label="Release"] button')
		.evaluateAll((controls) => controls.map((entry) => (entry as HTMLElement).getBoundingClientRect().height));
	for (const height of heights) {
		expect(height, 'tap target at 375px').toBeGreaterThanOrEqual(43.5);
	}
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
