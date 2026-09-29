// Pins for the season fixtures and helpers. Quotes must name real
// turns, the season must run newest first, and the job mark must
// never move backwards.
import { afterEach, describe, expect, it, vi } from 'vitest';
// mount and unmount come from the client runtime by path. The bare
// specifier resolves to the server build under vitest, which refuses to
// mount. The relative path reaches the same client build the page ships.
// The runtime ships no declaration file on this path.
// @ts-expect-error: untyped client runtime path, typed as used below.
import { mount, unmount } from '../../../node_modules/svelte/src/internal/client/render.js';
import { activeWordAt } from '@nrynss/chaaya/transcript';
import EpisodePage from '../episode/[id]/+page.svelte';
import {
	browserStore,
	buildTone,
	buildWords,
	canPublish,
	draftEditHref,
	emptyScreen,
	encodeWavBytes,
	EpisodeController,
	episodeById,
	episodeNumber,
	eraseJob,
	failedTakeNotice,
	fixtureSharePath,
	formatClock,
	formatEpisodeNumber,
	GalleryController,
	installMockJob,
	LIVE_CARD_STALLED_NOTICE,
	LIVE_CARD_WAITING_NOTICE,
	LIVE_COPY_FAILED_NOTICE,
	LIVE_EPISODE_FAILED_NOTICE,
	LIVE_EPISODE_NOTICE,
	LIVE_ERASE_ARM_NOTICE,
	LIVE_ERASE_FAILED_NOTICE,
	LIVE_ERASE_STARTED_NOTICE,
	LIVE_LINK_COPIED_NOTICE,
	LIVE_NO_AUDIO_NOTICE,
	LIVE_PLAYBACK_FAILED_NOTICE,
	LIVE_PUBLISH_FAILED_NOTICE,
	LIVE_REVOKE_FAILED_NOTICE,
	LIVE_SEASON_FAILED_NOTICE,
	LIVE_SEASON_NOTICE,
	LIVE_TAKE_FAILED_NOTICE,
	LIVE_TAKE_UNFINISHED_NOTICE,
	listSeason,
	listThreads,
	parseEpisodeDetail,
	progressFrame,
	progressPercent,
	publishLink,
	queryValue,
	quoteHref,
	quoteInTranscript,
	readHighWater,
	seasonHref,
	shareUrl,
	transcriptText,
	writeHighWater,
	type GallerySnapshot,
	type SeasonRow,
	type WaterStore
} from './threads';

vi.mock('$app/environment', () => ({ browser: true }));
vi.mock('$app/paths', () => ({ resolve: (path: string) => path }));
vi.mock('$app/state', () => ({
	page: { params: { id: 'e9' }, url: new URL('http://localhost/episode/e9') }
}));

function memoryStore(): WaterStore {
	const held: Record<string, string> = {};
	return {
		read: (key) => (key in held ? (held[key] as string) : null),
		write: (key, value) => {
			held[key] = value;
		}
	};
}

describe('season order', () => {
	it('runs newest first with four ready episodes at base', () => {
		const season = listSeason('none');
		expect(season.map((episode) => episode.number)).toEqual([4, 3, 2, 1]);
		for (const episode of season) expect(episode.state).toBe('ready');
	});

	it('carries the fifth episode in its lab state', () => {
		expect(listSeason('rendering')[0]).toMatchObject({ number: 5, state: 'rendering' });
		expect(listSeason('draft')[0]).toMatchObject({ number: 5, state: 'draft' });
		expect(listSeason('ready')[0]).toMatchObject({ number: 5, state: 'ready' });
		expect(listSeason('none')).toHaveLength(4);
	});

	it('looks episodes up by id and misses unknown ids', () => {
		expect(episodeById('ep-4')?.title).toBe('Three weeks of almost');
		expect(episodeById('nope')).toBeUndefined();
	});
});

describe('quotes', () => {
	it('every thread quote sits in its episode transcript', () => {
		const threads = listThreads(true);
		const items = [...threads.commitments, ...threads.people, ...threads.topics];
		expect(items.length).toBeGreaterThan(0);
		for (const item of items) {
			expect(item.quotes.length).toBeGreaterThan(0);
			for (const quote of item.quotes) {
				expect(quoteInTranscript(quote.episode, quote.text)).toBe(true);
			}
		}
	});

	it('every quote offset opens the turn that carries it', () => {
		const threads = listThreads(false);
		const items = [...threads.commitments, ...threads.people, ...threads.topics];
		for (const item of items) {
			for (const quote of item.quotes) {
				const episode = episodeById(quote.episode);
				expect(episode).toBeDefined();
				const turn = episode?.turns.find((candidate) => candidate.start === quote.offset);
				expect(turn).toBeDefined();
				expect(turn?.text.includes(quote.text)).toBe(true);
			}
		}
	});

	it('quote offsets land on words sounding at that second', () => {
		const episode = episodeById('ep-4');
		if (!episode) throw new Error('missing ep-4');
		const words = buildWords(episode.turns);
		const index = activeWordAt(words, [], 14);
		expect(index).not.toBeNull();
		expect(words[index ?? 0]?.text).toBe('Nothing.');
	});

	it('episode five joins the threads once ready', () => {
		const before = listThreads(false);
		const after = listThreads(true);
		expect(after.commitments).toHaveLength(before.commitments.length + 1);
		expect(after.commitments[0]?.id).toBe('go-harvest');
		const june = after.people.find((item) => item.id === 'june');
		expect(june?.count).toBe('10 mentions · 5 episodes');
		expect(transcriptText(episodeById('ep-5')?.turns ?? []).length).toBeGreaterThan(0);
	});
});

describe('clocks and queries', () => {
	it('renders m:ss with zero padding', () => {
		expect(formatClock(0)).toBe('0:00');
		expect(formatClock(14)).toBe('0:14');
		expect(formatClock(75)).toBe('1:15');
		expect(formatClock(-3)).toBe('0:00');
		expect(formatEpisodeNumber(4)).toBe('EP.04');
	});

	it('reads the first matching query value', () => {
		expect(queryValue('?fixture=1&t=14', 't')).toBe('14');
		expect(queryValue('?fixture=1', 't')).toBeNull();
	});

	it('links quotes to the episode at their offset', () => {
		expect(quoteHref('ep-2', 20)).toBe('/episode/ep-2?fixture=1&t=20&play=1');
		expect(episodeNumber('ep-4')).toBe(4);
		expect(episodeNumber('nope')).toBe(0);
	});
});

describe('progress water', () => {
	it('renders whole percent clamped to the total', () => {
		expect(progressPercent(1, 4)).toBe(25);
		expect(progressPercent(3, 4)).toBe(75);
		expect(progressPercent(9, 4)).toBe(100);
		expect(progressPercent(0, 0)).toBe(0);
	});

	it('never moves backwards across writes and restarts', () => {
		const store = memoryStore();
		expect(readHighWater(store, 'job-1')).toBe(0);
		expect(writeHighWater(store, 'job-1', 25)).toBe(25);
		expect(writeHighWater(store, 'job-1', 10)).toBe(25);
		expect(writeHighWater(store, 'job-1', 50)).toBe(50);
		const fresh = memoryStore();
		expect(readHighWater(fresh, 'job-1')).toBe(0);
		expect(readHighWater(store, 'job-1')).toBe(50);
	});

	it('answers zero when the store refuses', () => {
		const refusing: WaterStore = {
			read: () => {
				throw new Error('denied');
			},
			write: () => {
				throw new Error('denied');
			}
		};
		expect(readHighWater(refusing, 'job-1')).toBe(0);
		expect(writeHighWater(refusing, 'job-1', 40)).toBe(40);
	});

	it('the browser store holds the mark where a window lives', () => {
		const store = browserStore();
		if (!store) {
			expect(store).toBeNull();
			return;
		}
		expect(writeHighWater(store, 'job-dom', 30)).toBe(30);
		expect(readHighWater(store, 'job-dom')).toBe(30);
	});
});

describe('fixture audio', () => {
	it('builds a deterministic tone at the fixture rate', () => {
		const first = buildTone(2, 4);
		const second = buildTone(2, 4);
		expect(first.length).toBe(16000);
		expect(first).toEqual(second);
		expect(buildTone(2, 5)).not.toEqual(first);
	});

	it('wraps the tone in a WAV of exact size', () => {
		const bytes = encodeWavBytes(buildTone(1, 1), 8000);
		expect(bytes.length).toBe(44 + 8000 * 2);
		expect(String.fromCharCode(bytes[0] ?? 0, bytes[1] ?? 0, bytes[2] ?? 0, bytes[3] ?? 0)).toBe(
			'RIFF'
		);
	});

	it('frames scripted progress in SSE wire shape', () => {
		expect(progressFrame('job-1', 'rendering', 2, 4, 2)).toContain('event: progress');
		expect(progressFrame('job-1', 'rendering', 2, 4, 2)).toContain('"current":2');
		expect(typeof installMockJob).toBe('function');
	});
});

describe('live season', () => {
	it('lists episodes newest first and drops drifting rows', async () => {
		const { parseSeasonList } = await import('./threads');
		const season = parseSeasonList(
			JSON.stringify({
				episodes: [
					{ id: 'a', number: 1, title: 'First', state: 'ready', visibility: 'private' },
					{ id: 'b', number: 3, title: 'Third', state: 'rendering', visibility: 'private' },
					{ id: 'c', number: 2, title: 'Second', state: 'draft', visibility: 'private' },
					{ id: '', number: 9, title: '', state: 'ready', visibility: 'private' }
				]
			})
		);
		expect(season.map((episode) => episode.number)).toEqual([3, 2, 1]);
		expect(season[0]?.title).toBe('Third');
	});

	it('carries the cover path from the list and the detail', async () => {
		const { liveRow, parseEpisodeDetail, parseSeasonList } = await import('./threads');
		const season = parseSeasonList(
			JSON.stringify({
				episodes: [
					{
						id: 'e9',
						number: 9,
						title: 'Ninth',
						state: 'ready',
						visibility: 'private',
						cover_path: '/api/episodes/e9/cover'
					},
					{ id: 'e10', number: 10, title: 'Tenth', state: 'ready', visibility: 'private' }
				]
			})
		);
		expect(season.find((episode) => episode.id === 'e9')?.coverPath).toBe(
			'/api/episodes/e9/cover'
		);
		expect(season.find((episode) => episode.id === 'e10')?.coverPath).toBe('');
		const detail = parseEpisodeDetail(
			JSON.stringify({
				episode: {
					id: 'e9',
					number: 9,
					title: 'Ninth',
					state: 'ready',
					visibility: 'private',
					cover_path: '/api/episodes/e9/cover'
				},
				proposals: []
			})
		);
		expect(detail.episode.coverPath).toBe('/api/episodes/e9/cover');
		expect(liveRow(detail.episode, '').coverPath).toBe('/api/episodes/e9/cover');
		expect(liveRow({ ...detail.episode, coverPath: '' }, '').coverPath).toBe('');
	});

	it('rejects a body with no episode list', async () => {
		const { parseSeasonList } = await import('./threads');
		expect(() => parseSeasonList('{}')).toThrow();
		expect(() => parseSeasonList('nope')).toThrow();
	});

	it('decodes detail with proposals and a nullable outcome', async () => {
		const { parseEpisodeDetail } = await import('./threads');
		const withOutcome = parseEpisodeDetail(
			JSON.stringify({
				episode: { id: 'e1', number: 1, title: 'First', state: 'draft', visibility: 'private' },
				proposals: [
					{ id: 'p1', kind: 'cut', start_word: 0, end_word: 4, reason: 'Trim.', decision: '' }
				],
				transcript_outcome: { job_id: 'job-7', status: 'running', error: '' }
			})
		);
		expect(withOutcome.episode.title).toBe('First');
		expect(withOutcome.proposals).toHaveLength(1);
		expect(withOutcome.outcome?.jobId).toBe('job-7');
		const withoutOutcome = parseEpisodeDetail(
			JSON.stringify({
				episode: { id: 'e1', number: 1, title: 'First', state: 'ready', visibility: 'private' },
				proposals: []
			})
		);
		expect(withoutOutcome.outcome).toBeNull();
		expect(() => parseEpisodeDetail(JSON.stringify({ proposals: [] }))).toThrow();
	});

	it('decodes the thread index with counts from stored rows', async () => {
		const { parseThreadsIndex } = await import('./threads');
		const index = parseThreadsIndex(
			JSON.stringify({
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
				circled_topics: [
					{
						key: 'harvest',
						display: 'The harvest',
						episodes: [{ episode_id: 'e2', number: 2, quote: 'Down for the harvest.', offset: 9 }],
						mention_count: 3,
						episode_count: 2
					}
				]
			})
		);
		expect(index.names).toHaveLength(1);
		expect(index.names[0]?.mentionCount).toBe(2);
		expect(index.topics[0]?.episodes[0]?.quote).toBe('Down for the harvest.');
	});

	it('links live quotes to the episode at their word offset', async () => {
		const { liveQuoteHref, outcomeJobStatus } = await import('./threads');
		expect(liveQuoteHref('e1', 5)).toBe('/episode/e1?w=5');
		expect(outcomeJobStatus('running')).toBe('running');
		expect(outcomeJobStatus('interrupted')).toBe('interrupted');
		expect(outcomeJobStatus('mystery')).toBe('running');
	});

	it('fetches the season through the injected fetch', async () => {
		const { fetchSeason } = await import('./threads');
		const season = await fetchSeason(async () =>
			Response.json({
				episodes: [{ id: 'a', number: 2, title: 'B', state: 'ready', visibility: 'private' }]
			})
		);
		expect(season).toHaveLength(1);
		await expect(fetchSeason(async () => new Response('no', { status: 500 }))).rejects.toThrow();
	});

	it('keeps words and both audio addresses on a detail', async () => {
		const { parseEpisodeDetail } = await import('./threads');
		const detail = parseEpisodeDetail(
			JSON.stringify({
				episode: { id: 'e1', number: 1, title: 'First', state: 'draft', visibility: 'private' },
				proposals: [],
				words: [
					{ text: 'Hello', start: 0, end: 0.4 },
					{ text: 'there', start: 0.4, end: 0.9 }
				],
				audio_url: '/media/user-blob',
				render_audio_url: '/media/opus-new'
			})
		);
		expect(detail.words.map((word) => word.text)).toEqual(['Hello', 'there']);
		expect(detail.audioUrl).toBe('/media/user-blob');
		expect(detail.renderAudioUrl).toBe('/media/opus-new');
		expect(detail.words[1]?.end).toBe(0.9);
	});
});

function galleryRow(partial: Partial<SeasonRow>): SeasonRow {
	return {
		id: 'e1',
		number: 1,
		title: 'Take',
		state: 'ready',
		visibility: 'private',
		meta: 'Private',
		duration: null,
		jobId: '',
		fixture: false,
		proposed: false,
		...partial
	};
}

describe('gallery links', () => {
	it('opens a live draft in the editor and keeps the other rows', () => {
		expect(seasonHref(galleryRow({ state: 'draft' }))).toBe('/episode/e1/edit');
		expect(seasonHref(galleryRow({ state: 'draft', jobId: 'job-7' }))).toBe('/episode/e1/edit');
		expect(seasonHref(galleryRow({ id: 'ep-5', state: 'draft', fixture: true }))).toBe(
			'/episode/ep-5/edit?fixture=1'
		);
		expect(seasonHref(galleryRow({ state: 'ready' }))).toBe('/episode/e1');
		expect(seasonHref(galleryRow({ id: 'ep-4', state: 'ready', fixture: true }))).toBe(
			'/episode/ep-4?fixture=1'
		);
		expect(
			seasonHref(galleryRow({ id: 'ep-5', state: 'rendering', fixture: true, jobId: 'job-r' }))
		).toBe('/processing?episode=ep-5');
	});
});

describe('episode playback', () => {
	it('keeps the editor link on a live draft while playback waits', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
				if (url.includes('/api/threads')) {
					return Response.json({ name_threads: [], circled_topics: [] });
				}
				return Response.json({
					episode: { id: 'ep-live', number: 2, title: 'Quiet take', state: 'draft', visibility: 'private' },
					proposals: [],
					words: [],
					audio_url: '/media/user-blob',
					render_audio_url: ''
				});
			})
		);
		try {
			const snaps: Array<ReturnType<typeof emptyScreen>> = [];
			const controller = new EpisodeController('ep-live', (snap) => snaps.push(snap));
			controller.mount('');
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.ready).toBe(true);
			});
			const snap = snaps.at(-1);
			expect(snap?.audioUrl).toBe('');
			expect(snap?.duration).toBeNull();
			expect(snap?.state).toBe('draft');
			expect(draftEditHref(snap ?? emptyScreen('ep-live'))).toBe('/episode/ep-live/edit');
			const exposed = window as unknown as { __episode?: { source: () => string | null } };
			expect(exposed.__episode?.source() ?? null).toBeNull();
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it('plays the render address from the detail', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
				if (url.includes('/api/threads')) {
					return Response.json({ name_threads: [], circled_topics: [] });
				}
				return Response.json({
					episode: { id: 'ep-9', number: 3, title: 'Heard', state: 'ready', visibility: 'private' },
					proposals: [],
					words: [
						{ text: 'One', start: 0, end: 1.2 },
						{ text: 'Two', start: 1.2, end: 3.5 }
					],
					audio_url: '/media/stem',
					render_audio_url: '/media/opus-9'
				});
			})
		);
		try {
			const snaps: Array<ReturnType<typeof emptyScreen>> = [];
			const controller = new EpisodeController('ep-9', (snap) => snaps.push(snap));
			controller.mount('');
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.audioUrl).toBe('/media/opus-9');
			});
			const snap = snaps.at(-1);
			expect(snap?.duration).toBe(3.5);
			expect(snap?.words.map((word) => word.text)).toEqual(['One', 'Two']);
			const exposed = window as unknown as { __episode?: { source: () => string | null } };
			expect(exposed.__episode?.source()).toBe('/media/opus-9');
			expect(draftEditHref(snap ?? emptyScreen('ep-9'))).toBeNull();
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});
});

describe('release controls', () => {
	it('reads the share path and the erasure job behind the controls', () => {
		expect(publishLink(JSON.stringify({ share_token: 'tok', share_path: '/share/tok' }))).toBe(
			'/share/tok'
		);
		expect(publishLink('{"share_path":"/episode/x"}')).toBe('');
		expect(publishLink('not json')).toBe('');
		expect(eraseJob(JSON.stringify({ job_id: 'job-erase-1' }))).toBe('job-erase-1');
		expect(eraseJob('{}')).toBe('');
	});

	it('keeps the fixture publish flip with no fetch', () => {
		const fetchSpy = vi.fn(async () => Response.json({}));
		vi.stubGlobal('fetch', fetchSpy);
		try {
			const snaps: Array<ReturnType<typeof emptyScreen>> = [];
			const controller = new EpisodeController('ep-4', (snap) => snaps.push(snap));
			controller.mount('?fixture=1');
			controller.publishState();
			expect(snaps.at(-1)?.published).toBe(true);
			expect(snaps.at(-1)?.sharePath).toBe(fixtureSharePath('ep-4'));
			expect(snaps.at(-1)?.notice).toBe('');
			expect(fetchSpy).not.toHaveBeenCalled();
			controller.publishState();
			expect(snaps.at(-1)?.published).toBe(false);
			expect(snaps.at(-1)?.sharePath).toBe('');
			expect(fetchSpy).not.toHaveBeenCalled();
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it('publishes and revokes a live episode through the routes', async () => {
		const calls: Array<{ url: string; method: string }> = [];
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
				const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
				const method = init?.method ?? 'GET';
				calls.push({ url, method });
				if (url.endsWith('/publish') && method === 'POST') {
					return Response.json({ share_token: 'tok-1', share_path: '/share/tok-1' });
				}
				if (url.endsWith('/publish') && method === 'DELETE') {
					return Response.json({ share_token: 'tok-2' });
				}
				if (url.includes('/api/threads')) {
					return Response.json({ name_threads: [], circled_topics: [] });
				}
				return Response.json({
					episode: { id: 'ep-live', number: 2, title: 'Quiet take', state: 'ready', visibility: 'private' },
					proposals: [],
					words: [],
					audio_url: '',
					render_audio_url: ''
				});
			})
		);
		try {
			const snaps: Array<ReturnType<typeof emptyScreen>> = [];
			const controller = new EpisodeController('ep-live', (snap) => snaps.push(snap));
			controller.mount('');
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.ready).toBe(true);
			});
			controller.publishState();
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.published).toBe(true);
			});
			expect(snaps.at(-1)?.sharePath).toBe('/share/tok-1');
			expect(snaps.at(-1)?.notice).toBe('');
			expect(snaps.at(-1)?.visibility).toBe('public');
			controller.publishState();
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.published).toBe(false);
			});
			expect(snaps.at(-1)?.sharePath).toBe('');
			expect(snaps.at(-1)?.notice).toBe('');
			const publishCalls = calls.filter((call) => call.url.endsWith('/publish'));
			expect(publishCalls.map((call) => call.method)).toEqual(['POST', 'DELETE']);
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it('shows the retry on a refused live publish and on a refused revoke', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
				if (url.endsWith('/publish')) {
					return new Response('{"error":"slow"}', { status: 500 });
				}
				if (url.includes('/api/threads')) {
					return Response.json({ name_threads: [], circled_topics: [] });
				}
				return Response.json({
					episode: { id: 'ep-live', number: 2, title: 'Quiet take', state: 'ready', visibility: 'public' },
					proposals: [],
					words: [],
					audio_url: '',
					render_audio_url: '',
					share_path: '/share/tok-9'
				});
			})
		);
		try {
			const snaps: Array<ReturnType<typeof emptyScreen>> = [];
			const controller = new EpisodeController('ep-live', (snap) => snaps.push(snap));
			controller.mount('');
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.ready).toBe(true);
			});
			expect(snaps.at(-1)?.published).toBe(true);
			controller.publishState();
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.notice).toBe(LIVE_REVOKE_FAILED_NOTICE);
			});
			expect(snaps.at(-1)?.published).toBe(true);
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it('erases on a 202 by leaving for the gallery', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
				const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
				const method = init?.method ?? 'GET';
				if (url === '/api/episodes/ep-live' && method === 'DELETE') {
					return Response.json({ job_id: 'job-erase-9' }, { status: 202 });
				}
				if (url.includes('/api/threads')) {
					return Response.json({ name_threads: [], circled_topics: [] });
				}
				return Response.json({
					episode: { id: 'ep-live', number: 2, title: 'Quiet take', state: 'ready', visibility: 'private' },
					proposals: [],
					words: [],
					audio_url: '',
					render_audio_url: ''
				});
			})
		);
		try {
			const snaps: Array<ReturnType<typeof emptyScreen>> = [];
			const controller = new EpisodeController('ep-live', (snap) => snaps.push(snap));
			controller.mount('');
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.ready).toBe(true);
			});
			await controller.erase();
			expect(snaps.at(-1)?.eraseArmed).toBe(true);
			expect(snaps.at(-1)?.notice).toBe(LIVE_ERASE_ARM_NOTICE);
			await controller.erase();
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.notice).toBe(LIVE_ERASE_STARTED_NOTICE);
			});
			expect(snaps.at(-1)?.redirect).toBe('/?erased=1');
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it('counts a 404 erase as already erased', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
				const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
				const method = init?.method ?? 'GET';
				if (url === '/api/episodes/ep-live' && method === 'DELETE') {
					return new Response('{}', { status: 404 });
				}
				if (url.includes('/api/threads')) {
					return Response.json({ name_threads: [], circled_topics: [] });
				}
				return Response.json({
					episode: { id: 'ep-live', number: 2, title: 'Quiet take', state: 'ready', visibility: 'private' },
					proposals: [],
					words: [],
					audio_url: '',
					render_audio_url: ''
				});
			})
		);
		try {
			const snaps: Array<ReturnType<typeof emptyScreen>> = [];
			const controller = new EpisodeController('ep-live', (snap) => snaps.push(snap));
			controller.mount('');
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.ready).toBe(true);
			});
			await controller.erase();
			await controller.erase();
			expect(snaps.at(-1)?.notice).toBe(LIVE_ERASE_STARTED_NOTICE);
			expect(snaps.at(-1)?.redirect).toBe('/?erased=1');
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it('shows the retry on any other erase failure', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
				const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
				const method = init?.method ?? 'GET';
				if (url === '/api/episodes/ep-live' && method === 'DELETE') {
					return new Response('{"error":"slow"}', { status: 500 });
				}
				if (url.includes('/api/threads')) {
					return Response.json({ name_threads: [], circled_topics: [] });
				}
				return Response.json({
					episode: { id: 'ep-live', number: 2, title: 'Quiet take', state: 'ready', visibility: 'private' },
					proposals: [],
					words: [],
					audio_url: '',
					render_audio_url: ''
				});
			})
		);
		try {
			const snaps: Array<ReturnType<typeof emptyScreen>> = [];
			const controller = new EpisodeController('ep-live', (snap) => snaps.push(snap));
			controller.mount('');
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.ready).toBe(true);
			});
			await controller.erase();
			await controller.erase();
			expect(snaps.at(-1)?.notice).toBe(LIVE_ERASE_FAILED_NOTICE);
			expect(snaps.at(-1)?.redirect).toBeNull();
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it('reports a refused live publish instead of flipping the flag', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
				if (url.endsWith('/publish')) {
					return new Response('{"error":{"code":"no_render","message":"no render"}}', {
						status: 409,
						headers: { 'content-type': 'application/json' }
					});
				}
				if (url.includes('/api/threads')) {
					return Response.json({ name_threads: [], circled_topics: [] });
				}
				return Response.json({
					episode: { id: 'ep-live', number: 2, title: 'Quiet take', state: 'ready', visibility: 'private' },
					proposals: [],
					words: [],
					audio_url: '',
					render_audio_url: ''
				});
			})
		);
		try {
			const snaps: Array<ReturnType<typeof emptyScreen>> = [];
			const controller = new EpisodeController('ep-live', (snap) => snaps.push(snap));
			controller.mount('');
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.ready).toBe(true);
			});
			controller.publishState();
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.notice).toBe(LIVE_PUBLISH_FAILED_NOTICE);
			});
			expect(snaps.at(-1)?.published).toBe(false);
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});
});

function stubMomentEpisode(state: string): void {
	vi.stubGlobal(
		'fetch',
		vi.fn(async (input: RequestInfo | URL) => {
			const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
			if (url.includes('/api/threads')) {
				return Response.json({ name_threads: [], circled_topics: [] });
			}
			return Response.json({
				episode: { id: 'e1', number: 1, title: 'First take', state, visibility: 'private' },
				proposals: [],
				words: [{ text: 'Hello', start: 0, end: 0.4 }],
				audio_url: '',
				render_audio_url: ''
			});
		})
	);
}

async function mountMomentEpisode(search: string): Promise<ReturnType<typeof emptyScreen>> {
	const snaps: Array<ReturnType<typeof emptyScreen>> = [];
	const controller = new EpisodeController('e1', (snap) => snaps.push(snap));
	controller.mount(search);
	try {
		await vi.waitFor(() => {
			expect(snaps.at(-1)?.ready).toBe(true);
		});
		return snaps.at(-1) ?? emptyScreen('e1');
	} finally {
		controller.destroy();
	}
}

describe('quoted moments', () => {
	it('treats a missing or empty moment as no moment', async () => {
		stubMomentEpisode('ready');
		try {
			const missing = await mountMomentEpisode('');
			expect(missing.momentWord).toBeNull();
			expect(missing.momentQuote).toBeNull();
			expect(missing.notice).not.toContain('Quoted moment');
			const empty = await mountMomentEpisode('?w=');
			expect(empty.momentWord).toBeNull();
			expect(empty.notice).not.toContain('Quoted moment');
			const numbered = await mountMomentEpisode('?w=12');
			expect(numbered.momentWord).toBe(12);
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it('drops a moment that names no usable word offset', async () => {
		stubMomentEpisode('ready');
		try {
			expect((await mountMomentEpisode('?w=words')).momentWord).toBeNull();
			expect((await mountMomentEpisode('?w=-2')).momentWord).toBeNull();
		} finally {
			vi.unstubAllGlobals();
		}
	});
});

describe('publish gate', () => {
	it('offers publish only on a stored episode', () => {
		expect(canPublish({ state: 'ready', published: false })).toBe(true);
		expect(canPublish({ state: 'recording', published: false })).toBe(false);
		expect(canPublish({ state: 'rendering', published: false })).toBe(false);
		expect(canPublish({ state: 'ready', published: true })).toBe(false);
	});

	it('a take that ended before storage exposes no publish control', async () => {
		stubMomentEpisode('recording');
		try {
			const snap = await mountMomentEpisode('');
			expect(snap.state).toBe('recording');
			expect(snap.audioUrl).toBe('');
			expect(canPublish(snap)).toBe(false);
		} finally {
			vi.unstubAllGlobals();
		}
	});
});

function seasonStubUrl(input: RequestInfo | URL): string {
	if (typeof input === 'string') return input;
	if (input instanceof URL) return input.href;
	return input.url;
}

function seasonDetail(id: string, state: string): Record<string, unknown> {
	const body: Record<string, unknown> = {
		episode: { id, number: 1, title: `Take ${id}`, state, visibility: 'private' },
		proposals: [],
		transcript_outcome: { job_id: `job-t-${id}`, status: 'running', error: '' }
	};
	if (state === 'rendering') {
		body['render_outcome'] = { job_id: `job-r-${id}`, status: 'running', error: '' };
	}
	return body;
}

function openEventStream(): Response {
	return new Response(new ReadableStream<Uint8Array>({ start() {} }), {
		status: 200,
		headers: { 'content-type': 'text/event-stream' }
	});
}

describe('gallery detail budget', () => {
	let controller: GalleryController | null = null;

	afterEach(() => {
		controller?.destroy();
		controller = null;
		vi.unstubAllGlobals();
		vi.useRealTimers();
	});

	it('reads the list once and skips recording rows with no stems', async () => {
		vi.useFakeTimers();
		const episodes = [
			{ id: 'e1', number: 1, title: 'First', state: 'recording', visibility: 'private' },
			{ id: 'e2', number: 2, title: 'Second', state: 'recording', visibility: 'private' },
			{ id: 'e3', number: 3, title: 'Third', state: 'recording', visibility: 'private' },
			{ id: 'e4', number: 4, title: 'Fourth', state: 'recording', visibility: 'private' },
			{ id: 'e5', number: 5, title: 'Fifth', state: 'recording', visibility: 'private' },
			{ id: 'e6', number: 6, title: 'Sixth', state: 'recording', visibility: 'private' },
			{ id: 'e7', number: 7, title: 'Seventh', state: 'draft', visibility: 'private' },
			{ id: 'e8', number: 8, title: 'Eighth', state: 'draft', visibility: 'private' },
			{ id: 'e9', number: 9, title: 'Ninth', state: 'rendering', visibility: 'private' }
		];
		let listReads = 0;
		let detailReads = 0;
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = seasonStubUrl(input);
				if (url.endsWith('/api/episodes')) {
					listReads += 1;
					return Response.json({ episodes });
				}
				const detail = url.match(/\/api\/episodes\/([^/]+)$/);
				if (detail?.[1]) {
					detailReads += 1;
					const row = episodes.find((candidate) => candidate.id === detail[1]);
					return Response.json(seasonDetail(detail[1] ?? '', row?.state ?? 'draft'));
				}
				if (url.endsWith('/events')) return openEventStream();
				const job = url.match(/\/api\/jobs\/([^/]+)$/);
				if (job?.[1]) return Response.json({ jobId: job[1], status: 'running' });
				return new Response('', { status: 404 });
			})
		);
		const snaps: GallerySnapshot[] = [];
		controller = new GalleryController((snap) => {
			snaps.push(structuredClone(snap));
		});
		controller.mount('');
		await vi.advanceTimersByTimeAsync(800);
		expect(listReads).toBe(1);
		expect(detailReads).toBeLessThanOrEqual(3);
		expect(snaps.at(-1)?.rows).toHaveLength(9);
	});

	it('still reads a recording row that reports stems', async () => {
		vi.useFakeTimers();
		let detailReads = 0;
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = seasonStubUrl(input);
				if (url.endsWith('/api/episodes')) {
					return Response.json({
						episodes: [{ id: 'e1', number: 1, title: 'First', state: 'recording', visibility: 'private', stem_count: 2 }]
					});
				}
				const detail = url.match(/\/api\/episodes\/([^/]+)$/);
				if (detail?.[1]) {
					detailReads += 1;
					return Response.json(seasonDetail('e1', 'recording'));
				}
				if (url.endsWith('/events')) return openEventStream();
				const job = url.match(/\/api\/jobs\/([^/]+)$/);
				if (job?.[1]) return Response.json({ jobId: job[1], status: 'running' });
				return new Response('', { status: 404 });
			})
		);
		const snaps: GallerySnapshot[] = [];
		controller = new GalleryController((snap) => {
			snaps.push(structuredClone(snap));
		});
		controller.mount('');
		await vi.advanceTimersByTimeAsync(800);
		expect(detailReads).toBe(1);
		expect(snaps.at(-1)?.rows).toHaveLength(1);
	});
});

describe('gallery refused season', () => {
	let controller: GalleryController | null = null;

	afterEach(() => {
		controller?.destroy();
		controller = null;
		vi.unstubAllGlobals();
		vi.useRealTimers();
	});

	it('waits out a refused season read and renders without a refusal line', async () => {
		vi.useFakeTimers();
		let listReads = 0;
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = seasonStubUrl(input);
				if (url.endsWith('/api/episodes')) {
					listReads += 1;
					if (listReads === 1) {
						return new Response(JSON.stringify({ episodes: [] }), {
							status: 429,
							headers: { 'retry-after': '1', 'content-type': 'application/json' }
						});
					}
					return Response.json({
						episodes: [{ id: 'a', number: 1, title: 'First', state: 'ready', visibility: 'private' }]
					});
				}
				return new Response('', { status: 404 });
			})
		);
		const snaps: GallerySnapshot[] = [];
		controller = new GalleryController((snap) => {
			snaps.push(structuredClone(snap));
		});
		controller.mount('');
		await vi.advanceTimersByTimeAsync(1500);
		expect(listReads).toBe(2);
		const last = snaps.at(-1);
		expect(last?.rows).toHaveLength(1);
		expect(last?.failed).toBe(false);
		expect(last?.notice).not.toContain('refused');
	});
});

describe('share link release', () => {
	it('reads the share path only behind a public detail', () => {
		const held = parseEpisodeDetail(
			JSON.stringify({
				episode: { id: 'e9', number: 9, title: 'Ninth real', state: 'ready', visibility: 'public' },
				proposals: [],
				words: [],
				audio_url: '',
				render_audio_url: '',
				share_path: '/share/tok-9'
			})
		);
		expect(held.sharePath).toBe('/share/tok-9');
		const drifted = parseEpisodeDetail(
			JSON.stringify({
				episode: { id: 'e9', number: 9, title: 'Ninth real', state: 'ready', visibility: 'public' },
				proposals: [],
				words: [],
				audio_url: '',
				render_audio_url: '',
				share_path: '/episode/e9'
			})
		);
		expect(drifted.sharePath).toBe('');
		const missing = parseEpisodeDetail(
			JSON.stringify({
				episode: { id: 'e9', number: 9, title: 'Ninth real', state: 'ready', visibility: 'private' },
				proposals: [],
				words: [],
				audio_url: '',
				render_audio_url: ''
			})
		);
		expect(missing.sharePath).toBe('');
	});

	it('exposes the absolute share address behind one path', () => {
		const url = shareUrl('/share/tok-9');
		expect(url.endsWith('/share/tok-9')).toBe(true);
	});

	function stubPublicSeason(next: string | null): void {
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
				const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
				const method = init?.method ?? 'GET';
				if (url.endsWith('/publish') && method === 'POST') {
					return Response.json({ share_token: 'tok-2', share_path: next ?? '/share/tok-2' });
				}
				if (url.endsWith('/publish') && method === 'DELETE') {
					return Response.json({ share_token: 'tok-3' });
				}
				if (url.includes('/api/threads')) {
					return Response.json({ name_threads: [], circled_topics: [] });
				}
				return Response.json({
					episode: { id: 'e9', number: 9, title: 'Ninth real', state: 'ready', visibility: 'public' },
					proposals: [],
					words: [],
					audio_url: '',
					render_audio_url: '',
					share_path: '/share/tok-9'
				});
			})
		);
	}

	function stubClipboard(): string[] {
		const written: string[] = [];
		Object.defineProperty(window.navigator, 'clipboard', {
			value: {
				writeText: async (text: string) => {
					written.push(text);
				}
			},
			configurable: true
		});
		return written;
	}

	it('loads the share path on a public detail, sets it on publish, and clears it on revoke', async () => {
		stubPublicSeason('/share/tok-2');
		try {
			const snaps: Array<ReturnType<typeof emptyScreen>> = [];
			const controller = new EpisodeController('e9', (snap) => snaps.push(snap));
			controller.mount('');
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.ready).toBe(true);
			});
			expect(snaps.at(-1)?.published).toBe(true);
			expect(snaps.at(-1)?.sharePath).toBe('/share/tok-9');
			controller.publishState();
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.published).toBe(false);
			});
			expect(snaps.at(-1)?.sharePath).toBe('');
			controller.publishState();
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.sharePath).toBe('/share/tok-2');
			});
			expect(snaps.at(-1)?.published).toBe(true);
			expect(snaps.at(-1)?.notice).toBe('');
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it('copies the absolute share address with confirmation', async () => {
		stubPublicSeason('/share/tok-2');
		const written = stubClipboard();
		try {
			const snaps: Array<ReturnType<typeof emptyScreen>> = [];
			const controller = new EpisodeController('e9', (snap) => snaps.push(snap));
			controller.mount('');
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.sharePath).toBe('/share/tok-9');
			});
			controller.copyLink();
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.notice).toBe(LIVE_LINK_COPIED_NOTICE);
			});
			expect(written).toHaveLength(1);
			expect(written[0]).toBe(shareUrl('/share/tok-9'));
			controller.destroy();
		} finally {
			Reflect.deleteProperty(window.navigator, 'clipboard');
			vi.unstubAllGlobals();
		}
	});

	it('names the retry when the copy refuses', async () => {
		stubPublicSeason('/share/tok-2');
		Object.defineProperty(window.navigator, 'clipboard', {
			value: {
				writeText: async () => {
					throw new Error('denied');
				}
			},
			configurable: true
		});
		try {
			const snaps: Array<ReturnType<typeof emptyScreen>> = [];
			const controller = new EpisodeController('e9', (snap) => snaps.push(snap));
			controller.mount('');
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.sharePath).toBe('/share/tok-9');
			});
			controller.copyLink();
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.notice).toBe(LIVE_COPY_FAILED_NOTICE);
			});
			controller.destroy();
		} finally {
			Reflect.deleteProperty(window.navigator, 'clipboard');
			vi.unstubAllGlobals();
		}
	});

	it('shows the share address as a link beside the copy control while published', async () => {
		stubPublicSeason('/share/tok-2');
		const written = stubClipboard();
		const app = mount(EpisodePage, { target: document.body });
		try {
			await vi.waitFor(() => {
				expect(document.querySelector('h1')?.textContent).toBe('Ninth real');
			});
			const anchor = document.querySelector(
				'section[aria-label="Release"] a[target="_blank"]'
			);
			expect(anchor instanceof HTMLAnchorElement).toBe(true);
			const href = anchor instanceof HTMLAnchorElement ? anchor.href : '';
			expect(href.endsWith('/share/tok-9')).toBe(true);
			const buttons = Array.from(
				document.querySelectorAll<HTMLButtonElement>('section[aria-label="Release"] button')
			);
			const labels = buttons.map((button) => button.textContent);
			expect(labels).toContain('Copy link');
			expect(labels).toContain('Revoke');
			const releaseText = (document.body.textContent ?? '').replace(/\s+/g, ' ');
			expect(releaseText).not.toContain('Stems and the transcript stay private.');
			buttons.find((button) => button.textContent === 'Copy link')?.click();
			await vi.waitFor(() => {
				expect(document.body.textContent).toContain('Link copied.');
			});
			expect(written).toHaveLength(1);
			expect(written[0]).toBe(href);
		} finally {
			unmount(app);
			document.body.innerHTML = '';
			Reflect.deleteProperty(window.navigator, 'clipboard');
			vi.unstubAllGlobals();
		}
	});
});

describe('live notices', () => {
	it('names the lost season and the lost episode in plain words', () => {
		expect(LIVE_SEASON_FAILED_NOTICE).toBe('Your episodes did not load. Retry.');
		expect(LIVE_EPISODE_FAILED_NOTICE).toBe('This episode did not load. Retry.');
		expect(LIVE_SEASON_NOTICE).toBe('Live season, newest first.');
	});

	it('shows no notice on a loaded episode without a quoted moment', async () => {
		stubMomentEpisode('ready');
		try {
			const snap = await mountMomentEpisode('');
			expect(snap.notice).toBe(LIVE_EPISODE_NOTICE);
			expect(snap.notice).toBe('');
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it('names the lost episode on a refused live detail', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async () => new Response('{"error":"slow"}', { status: 500 }))
		);
		try {
			const snaps: Array<ReturnType<typeof emptyScreen>> = [];
			const controller = new EpisodeController('e1', (snap) => snaps.push(snap));
			controller.mount('');
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.failed).toBe(true);
			});
			expect(snaps.at(-1)?.notice).toBe(LIVE_EPISODE_FAILED_NOTICE);
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it('names the lost season on a refused live list', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async () => new Response('{"error":"slow"}', { status: 500 }))
		);
		let controller: GalleryController | null = null;
		try {
			const snaps: GallerySnapshot[] = [];
			controller = new GalleryController((snap) => {
				snaps.push(structuredClone(snap));
			});
			controller.mount('');
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.failed).toBe(true);
			});
			expect(snaps.at(-1)?.notice).toBe(LIVE_SEASON_FAILED_NOTICE);
		} finally {
			controller?.destroy();
			vi.unstubAllGlobals();
		}
	});
});

describe('episode season nav', () => {
	it('renders plain season tabs with Edit as a primary pill on a live draft', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
				if (url.includes('/api/threads')) {
					return Response.json({ name_threads: [], circled_topics: [] });
				}
				return Response.json({
					episode: { id: 'e9', number: 3, title: 'Heard', state: 'draft', visibility: 'private' },
					proposals: [],
					words: [{ text: 'One', start: 0, end: 1.2 }],
					audio_url: '',
					render_audio_url: ''
				});
			})
		);
		const app = mount(EpisodePage, { target: document.body });
		try {
			await vi.waitFor(() => {
				expect(document.querySelector('nav[aria-label="Season"] a.primary')?.textContent).toBe(
					'Edit'
				);
			});
			const nav = document.querySelector('nav[aria-label="Season"]');
			const gallery = nav?.querySelector('a.tab[href="/"]');
			const threads = nav?.querySelector('a.tab[href="/threads"]');
			expect(gallery?.textContent).toBe('Gallery');
			expect(threads?.textContent).toBe('Threads');
			expect(gallery?.classList.contains('active')).toBe(false);
			expect(threads?.classList.contains('active')).toBe(false);
			expect(gallery?.getAttribute('aria-current')).toBeNull();
			expect(threads?.getAttribute('aria-current')).toBeNull();
			const edit = nav?.querySelector('a.primary');
			expect(edit?.getAttribute('href')).toBe('/episode/e9/edit');
		} finally {
			unmount(app);
			document.body.innerHTML = '';
			vi.unstubAllGlobals();
		}
	});

	it('renders plain fixture tabs with no filled tab on a scripted episode', async () => {
		const { page } = await import('$app/state');
		const heldParams = page.params;
		const heldUrl = page.url;
		page.params = { id: 'ep-4' };
		page.url = new URL('http://localhost/episode/ep-4?fixture=1') as unknown as typeof page.url;
		const app = mount(EpisodePage, { target: document.body });
		try {
			await vi.waitFor(() => {
				expect(document.querySelector('nav[aria-label="Season"] a.tab')?.textContent).toBe(
					'Gallery'
				);
			});
			const nav = document.querySelector('nav[aria-label="Season"]');
			const tabs = Array.from(nav?.querySelectorAll('a.tab') ?? []);
			expect(tabs.map((tab) => tab.textContent)).toEqual(['Gallery', 'Threads']);
			for (const tab of tabs) {
				expect(tab.classList.contains('active')).toBe(false);
				expect(tab.getAttribute('aria-current')).toBeNull();
			}
			expect(nav?.querySelector('a[href="/threads?fixture=1"]')?.textContent).toBe('Threads');
			expect(nav?.querySelector('a.primary')).toBeNull();
		} finally {
			unmount(app);
			document.body.innerHTML = '';
			page.params = heldParams;
			page.url = heldUrl;
			vi.unstubAllGlobals();
		}
	});
});

describe('episode first press', () => {
	async function mountPlayableEpisode(): Promise<{
		controller: EpisodeController;
		snaps: Array<ReturnType<typeof emptyScreen>>;
	}> {
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
				if (url.includes('/api/threads')) {
					return Response.json({ name_threads: [], circled_topics: [] });
				}
				return Response.json({
					episode: { id: 'ep-9', number: 3, title: 'Heard', state: 'ready', visibility: 'private' },
					proposals: [],
					words: [{ text: 'One', start: 0, end: 1.2 }],
					audio_url: '/media/stem',
					render_audio_url: '/media/opus-9'
				});
			})
		);
		const snaps: Array<ReturnType<typeof emptyScreen>> = [];
		const controller = new EpisodeController('ep-9', (snap) => snaps.push(snap));
		controller.mount('');
		await vi.waitFor(() => {
			expect(snaps.at(-1)?.audioUrl).toBe('/media/opus-9');
		});
		return { controller, snaps };
	}

	function episodePlayer(controller: EpisodeController): {
		play(): Promise<boolean>;
		lastPlayError: { name: string; message: string } | null;
	} {
		return (controller as unknown as { player: { play(): Promise<boolean>; lastPlayError: { name: string; message: string } | null } }).player;
	}

	it('plays after one press when the retry answers', async () => {
		const { controller, snaps } = await mountPlayableEpisode();
		const play = vi
			.spyOn(episodePlayer(controller), 'play')
			.mockResolvedValueOnce(false)
			.mockResolvedValueOnce(true);
		try {
			await controller.togglePlay();
			expect(play).toHaveBeenCalledTimes(2);
			expect(snaps.at(-1)?.playing).toBe(true);
			expect(snaps.at(-1)?.notice).not.toContain('refused');
		} finally {
			controller.destroy();
			vi.unstubAllGlobals();
		}
	});

	it('shows the refusal only after the retry also fails', async () => {
		const { controller, snaps } = await mountPlayableEpisode();
		const play = vi.spyOn(episodePlayer(controller), 'play').mockResolvedValue(false);
		try {
			await controller.togglePlay();
			expect(play).toHaveBeenCalledTimes(2);
			expect(snaps.at(-1)?.playing).toBe(false);
			expect(snaps.at(-1)?.notice).toBe(LIVE_PLAYBACK_FAILED_NOTICE);
		} finally {
			controller.destroy();
			vi.unstubAllGlobals();
		}
	});
});

describe('gallery card progress lines', () => {
	it('waits on the episode before its first progress reading lands', () => {
		const controller = new GalleryController(() => {});
		try {
			const card = controller.cardFor('job-unseen');
			expect(card.detail).toBe(LIVE_CARD_WAITING_NOTICE);
			expect(card.percent).toBe(0);
			expect(card.running).toBe(true);
		} finally {
			controller.destroy();
		}
	});

	it('keeps the last mark when the progress feed drops', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = seasonStubUrl(input);
				if (url.endsWith('/api/episodes')) {
					return Response.json({
						episodes: [{ id: 'e1', number: 1, title: 'Take', state: 'rendering', visibility: 'private' }]
					});
				}
				if (url.endsWith('/api/episodes/e1')) {
					return Response.json(seasonDetail('e1', 'rendering'));
				}
				if (url.endsWith('/events')) return new Response('', { status: 500 });
				const job = url.match(/\/api\/jobs\/([^/]+)$/);
				if (job?.[1]) return Response.json({ jobId: job[1], status: 'running' });
				return new Response('', { status: 404 });
			})
		);
		const snaps: GallerySnapshot[] = [];
		const controller = new GalleryController((snap) => {
			snaps.push(structuredClone(snap));
		});
		try {
			controller.mount('');
			await vi.waitFor(
				() => {
					expect(snaps.at(-1)?.rows[0]?.jobId).toBe('job-r-e1');
				},
				{ timeout: 5000 }
			);
			await vi.waitFor(
				() => {
					expect(controller.cardFor('job-r-e1').detail).toBe(LIVE_CARD_STALLED_NOTICE);
				},
				{ timeout: 8000 }
			);
		} finally {
			controller.destroy();
			vi.unstubAllGlobals();
		}
	});
});

describe('failed takes', () => {
	it('names the transcription when the transcript pass failed', () => {
		expect(
			failedTakeNotice({
				outcome: { jobId: 'job-t', status: 'error', error: 'raw-boom' },
				renderOutcome: null
			})
		).toBe(LIVE_TAKE_FAILED_NOTICE);
		expect(LIVE_TAKE_FAILED_NOTICE).toBe("This take couldn't be transcribed.");
	});

	it('names the finish when a later pass failed', () => {
		expect(
			failedTakeNotice({
				outcome: { jobId: 'job-t', status: 'done', error: '' },
				renderOutcome: { jobId: 'job-r', status: 'error', error: 'raw-boom' }
			})
		).toBe(LIVE_TAKE_UNFINISHED_NOTICE);
		expect(
			failedTakeNotice({
				outcome: null,
				renderOutcome: null
			})
		).toBe(LIVE_TAKE_UNFINISHED_NOTICE);
	});

	it('shows one plain line on a failed episode and never the raw error', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
				if (url.includes('/api/threads')) {
					return Response.json({ name_threads: [], circled_topics: [] });
				}
				return Response.json({
					episode: { id: 'e1', number: 1, title: 'First take', state: 'failed', visibility: 'private' },
					proposals: [],
					words: [{ text: 'Hello', start: 0, end: 0.4 }],
					audio_url: '',
					render_audio_url: '',
					transcript_outcome: { job_id: 'job-t', status: 'error', error: 'raw-boom-xyz' }
				});
			})
		);
		try {
			const snaps: Array<ReturnType<typeof emptyScreen>> = [];
			const controller = new EpisodeController('e1', (snap) => snaps.push(snap));
			controller.mount('');
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.ready).toBe(true);
			});
			expect(snaps.at(-1)?.notice).toBe(LIVE_TAKE_FAILED_NOTICE);
			expect(snaps.at(-1)?.notice).not.toContain('raw-boom-xyz');
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});
});

describe('takes without audio', () => {
	it('reads no audio yet on a press with nothing loaded', async () => {
		const snaps: Array<ReturnType<typeof emptyScreen>> = [];
		const controller = new EpisodeController('ep-live', (snap) => snaps.push(snap));
		try {
			await controller.togglePlay();
			expect(snaps.at(-1)?.notice).toBe(LIVE_NO_AUDIO_NOTICE);
			expect(LIVE_NO_AUDIO_NOTICE).toBe('No audio yet.');
		} finally {
			controller.destroy();
		}
	});

	it('reads no audio yet on a live draft with no render address', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
				if (url.includes('/api/threads')) {
					return Response.json({ name_threads: [], circled_topics: [] });
				}
				return Response.json({
					episode: { id: 'e1', number: 1, title: 'First take', state: 'draft', visibility: 'private' },
					proposals: [],
					words: [],
					audio_url: '/media/user-blob',
					render_audio_url: ''
				});
			})
		);
		try {
			const snaps: Array<ReturnType<typeof emptyScreen>> = [];
			const controller = new EpisodeController('e1', (snap) => snaps.push(snap));
			controller.mount('');
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.ready).toBe(true);
			});
			expect(snaps.at(-1)?.audioUrl).toBe('');
			await controller.togglePlay();
			expect(snaps.at(-1)?.notice).toBe(LIVE_NO_AUDIO_NOTICE);
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});
});

describe('erased gallery', () => {
	it('shows the erased line after an erase lands back on the season', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = seasonStubUrl(input);
				if (url.endsWith('/api/episodes')) {
					return Response.json({
						episodes: [{ id: 'e1', number: 1, title: 'Take', state: 'ready', visibility: 'private' }]
					});
				}
				return new Response('', { status: 404 });
			})
		);
		let controller: GalleryController | null = null;
		try {
			const snaps: GallerySnapshot[] = [];
			controller = new GalleryController((snap) => {
				snaps.push(structuredClone(snap));
			});
			controller.mount('?erased=1');
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.ready).toBe(true);
			});
			expect(snaps.at(-1)?.rows).toHaveLength(1);
			expect(snaps.at(-1)?.notice).toBe(LIVE_ERASE_STARTED_NOTICE);
			expect(snaps.at(-1)?.notice).toBe('Episode erased.');
		} finally {
			controller?.destroy();
			vi.unstubAllGlobals();
		}
	});
});

describe('episode notices read plain', () => {
	it('names no machinery and keeps every line short', () => {
		const banned = ['endpoint', 'wired', 'fixture', 'scripted', 'job', 'backend', 'detail'];
		const lines = [
			LIVE_NO_AUDIO_NOTICE,
			LIVE_PLAYBACK_FAILED_NOTICE,
			LIVE_PUBLISH_FAILED_NOTICE,
			LIVE_REVOKE_FAILED_NOTICE,
			LIVE_LINK_COPIED_NOTICE,
			LIVE_COPY_FAILED_NOTICE,
			LIVE_ERASE_ARM_NOTICE,
			LIVE_ERASE_STARTED_NOTICE,
			LIVE_ERASE_FAILED_NOTICE,
			LIVE_TAKE_FAILED_NOTICE,
			LIVE_TAKE_UNFINISHED_NOTICE
		];
		for (const line of lines) {
			const lower = line.toLowerCase();
			for (const word of banned) expect(lower, `episode notice reads "${line}"`).not.toContain(word);
			for (const sentence of line.split('.')) {
				expect(
					sentence.trim().split(/\s+/).filter(Boolean).length,
					`long sentence in "${line}"`
				).toBeLessThanOrEqual(20);
			}
		}
	});
});
