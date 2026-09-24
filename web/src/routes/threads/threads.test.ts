// Pins for the season fixtures and helpers. Quotes must name real
// turns, the season must run newest first, and the job mark must
// never move backwards.
import { describe, expect, it, vi } from 'vitest';
import { activeWordAt } from '@nrynss/chaaya/transcript';
import {
	browserStore,
	buildTone,
	buildWords,
	draftEditHref,
	emptyScreen,
	encodeWavBytes,
	EpisodeController,
	episodeById,
	episodeNumber,
	eraseJob,
	formatClock,
	formatEpisodeNumber,
	installMockJob,
	listSeason,
	listThreads,
	progressFrame,
	progressPercent,
	publishLink,
	queryValue,
	quoteHref,
	quoteInTranscript,
	readHighWater,
	seasonHref,
	transcriptText,
	writeHighWater,
	type SeasonRow,
	type WaterStore
} from './threads';

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
			expect(snaps.at(-1)?.notice).toContain('Fixture link public');
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
			expect(snaps.at(-1)?.notice).toContain('/share/tok-1');
			expect(snaps.at(-1)?.visibility).toBe('public');
			controller.publishState();
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.published).toBe(false);
			});
			expect(snaps.at(-1)?.notice).toContain('revoked');
			const publishCalls = calls.filter((call) => call.url.endsWith('/publish'));
			expect(publishCalls.map((call) => call.method)).toEqual(['POST', 'DELETE']);
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it('names the erasure job a live erase starts', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
				const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
				const method = init?.method ?? 'GET';
				if (url === '/api/episodes/ep-live' && method === 'DELETE') {
					return Response.json({ job_id: 'job-erase-9' });
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
			await controller.erase();
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.notice).toContain('job-erase-9');
			});
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
				expect(snaps.at(-1)?.notice).toContain('stays private');
			});
			expect(snaps.at(-1)?.published).toBe(false);
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});
});
