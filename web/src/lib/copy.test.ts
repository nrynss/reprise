// The live words a guest reads. Every notice the three controllers show
// in live mode lives here as an exported constant, and this spec fails
// when one of them uses the build vocabulary instead of plain words.
// That covers the erase outcome, the render pass details, and the
// stopped progress line beside the load notices.
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
	LIVE_DRAFT_EMPTY_NOTICE,
	LIVE_DRAFT_NOTICE,
	LIVE_RENDER_DONE_DETAIL,
	LIVE_RENDER_DONE_READY_DETAIL,
	LIVE_RENDER_RUNNING_DETAIL,
	type DraftSnapshot
} from './editor/draft';
import { PROGRESS_STOPPED_DETAIL, TRANSCRIBING_DETAIL } from './voice/processing-state';
import {
	EpisodeController,
	emptyScreen,
	GalleryController,
	LIVE_EPISODE_FAILED_NOTICE,
	LIVE_EPISODE_NOTICE,
	LIVE_ERASE_FAILED_NOTICE,
	LIVE_ERASE_STARTED_NOTICE,
	LIVE_SEASON_FAILED_NOTICE,
	LIVE_SEASON_NOTICE,
	type GallerySnapshot
} from '../routes/threads/threads';

// Words the build uses for itself. A guest notice never carries one.
const BUILD_WORDS = ['endpoint', 'wired', 'fixture', 'scripted', 'job', 'backend', 'detail'];

function liveNotices(): Array<{ name: string; text: string }> {
	return [
		{ name: 'season failed', text: LIVE_SEASON_FAILED_NOTICE },
		{ name: 'season', text: LIVE_SEASON_NOTICE },
		{ name: 'episode failed', text: LIVE_EPISODE_FAILED_NOTICE },
		{ name: 'episode', text: LIVE_EPISODE_NOTICE },
		{ name: 'draft', text: LIVE_DRAFT_NOTICE },
		{ name: 'draft empty', text: LIVE_DRAFT_EMPTY_NOTICE },
		{ name: 'watching pass', text: TRANSCRIBING_DETAIL },
		{ name: 'erase started', text: LIVE_ERASE_STARTED_NOTICE },
		{ name: 'erase failed', text: LIVE_ERASE_FAILED_NOTICE },
		{ name: 'render running', text: LIVE_RENDER_RUNNING_DETAIL },
		{ name: 'render done ready', text: LIVE_RENDER_DONE_READY_DETAIL },
		{ name: 'render done', text: LIVE_RENDER_DONE_DETAIL },
		{ name: 'progress stopped', text: PROGRESS_STOPPED_DETAIL }
	];
}

function usesBuildWord(text: string): string | null {
	const lower = text.toLowerCase();
	for (const word of BUILD_WORDS) {
		if (lower.includes(word)) return word;
	}
	return null;
}

describe('live copy', () => {
	it('shows plain words on every exported live notice', () => {
		for (const notice of liveNotices()) {
			expect(
				usesBuildWord(notice.text),
				`${notice.name} notice reads "${notice.text}"`
			).toBeNull();
		}
	});

	it('names the lost episodes and the retry on a refused season', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async () => new Response('{"error":"slow"}', { status: 500 }))
		);
		const snaps: GallerySnapshot[] = [];
		const controller = new GalleryController((snap) => snaps.push(snap));
		try {
			controller.mount('');
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.failed).toBe(true);
			});
			expect(snaps.at(-1)?.notice).toBe(LIVE_SEASON_FAILED_NOTICE);
			expect(usesBuildWord(snaps.at(-1)?.notice ?? '')).toBeNull();
		} finally {
			controller.destroy();
			vi.unstubAllGlobals();
		}
	});

	it('shows no notice on a loaded episode without a quoted moment', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
				if (url.includes('/api/threads')) {
					return Response.json({ name_threads: [], circled_topics: [] });
				}
				return Response.json({
					episode: { id: 'e1', number: 1, title: 'First take', state: 'ready', visibility: 'private' },
					proposals: [],
					words: [{ text: 'Hello', start: 0, end: 0.4 }],
					audio_url: '',
					render_audio_url: ''
				});
			})
		);
		const snaps: Array<ReturnType<typeof emptyScreen>> = [];
		const controller = new EpisodeController('e1', (snap) => snaps.push(snap));
		try {
			controller.mount('');
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.ready).toBe(true);
			});
			expect(snaps.at(-1)?.notice).toBe(LIVE_EPISODE_NOTICE);
			expect(usesBuildWord(snaps.at(-1)?.notice ?? '')).toBeNull();
		} finally {
			controller.destroy();
			vi.unstubAllGlobals();
		}
	});

	it('names the lost episode and the retry on a refused detail', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async () => new Response('{"error":"slow"}', { status: 500 }))
		);
		const snaps: Array<ReturnType<typeof emptyScreen>> = [];
		const controller = new EpisodeController('e1', (snap) => snaps.push(snap));
		try {
			controller.mount('');
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.failed).toBe(true);
			});
			expect(snaps.at(-1)?.notice).toBe(LIVE_EPISODE_FAILED_NOTICE);
			expect(usesBuildWord(snaps.at(-1)?.notice ?? '')).toBeNull();
		} finally {
			controller.destroy();
			vi.unstubAllGlobals();
		}
	});

	it('shows no notice on a stored draft and a plain line on an empty one', async () => {
		const { DraftController } = await import('./editor/draft');
		const detail = (words: unknown[]): unknown => ({
			episode: { id: 'live-1', number: 2, title: 'Live take', state: 'draft', visibility: 'private' },
			proposals: [],
			words,
			audio_url: '',
			render_audio_url: ''
		});
		vi.stubGlobal(
			'fetch',
			vi.fn().mockResolvedValue(Response.json(detail([{ text: 'Hello', start: 0, end: 0.4 }])))
		);
		const stored: DraftSnapshot[] = [];
		const loaded = new DraftController({ episodeId: 'live-1', onChange: (snap) => stored.push(snap) });
		try {
			loaded.mount('');
			await vi.waitFor(() => {
				expect(stored.at(-1)?.ready ?? loaded.snapshot.ready).toBe(true);
			});
			expect(stored.at(-1)?.notice ?? loaded.snapshot.notice).toBe(LIVE_DRAFT_NOTICE);
		} finally {
			loaded.destroy();
			vi.unstubAllGlobals();
		}
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json(detail([]))));
		const blanks: DraftSnapshot[] = [];
		const blank = new DraftController({ episodeId: 'live-1', onChange: (snap) => blanks.push(snap) });
		try {
			blank.mount('');
			await vi.waitFor(() => {
				expect(blanks.at(-1)?.ready ?? blank.snapshot.ready).toBe(true);
			});
			expect(blanks.at(-1)?.notice ?? blank.snapshot.notice).toBe(LIVE_DRAFT_EMPTY_NOTICE);
			expect(usesBuildWord(blanks.at(-1)?.notice ?? blank.snapshot.notice)).toBeNull();
		} finally {
			blank.destroy();
			vi.unstubAllGlobals();
		}
	});

	it('names the take while a watched pass runs', async () => {
		const { ProcessingController } = await import('./voice/processing-state');
		vi.stubGlobal(
			'fetch',
			vi.fn(async () => new Response('{"error":"slow"}', { status: 500 }))
		);
		let latest = '';
		const controller = new ProcessingController(
			new URLSearchParams('episode=ep-1&transcript=tj-1&uploads=done&userBytes=10&hostBytes=20'),
			(snap) => {
				latest = snap.transcription.detail;
			}
		);
		try {
			controller.mount();
			expect(latest).toBe(TRANSCRIBING_DETAIL);
			expect(usesBuildWord(latest)).toBeNull();
		} finally {
			controller.destroy();
			vi.unstubAllGlobals();
		}
	});

	it('starts a live erase without naming the reference', async () => {
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
					words: [{ text: 'Hello', start: 0, end: 0.4 }],
					audio_url: '',
					render_audio_url: ''
				});
			})
		);
		const snaps: Array<ReturnType<typeof emptyScreen>> = [];
		const controller = new EpisodeController('ep-live', (snap) => snaps.push(snap));
		try {
			controller.mount('');
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.ready).toBe(true);
			});
			await controller.erase();
			expect(snaps.at(-1)?.eraseArmed).toBe(true);
			await controller.erase();
			expect(snaps.at(-1)?.notice).toBe(LIVE_ERASE_STARTED_NOTICE);
			expect(usesBuildWord(snaps.at(-1)?.notice ?? '')).toBeNull();
		} finally {
			controller.destroy();
			vi.unstubAllGlobals();
		}
	});

	it('names the retry on a refused live erase', async () => {
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
					words: [{ text: 'Hello', start: 0, end: 0.4 }],
					audio_url: '',
					render_audio_url: ''
				});
			})
		);
		const snaps: Array<ReturnType<typeof emptyScreen>> = [];
		const controller = new EpisodeController('ep-live', (snap) => snaps.push(snap));
		try {
			controller.mount('');
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.ready).toBe(true);
			});
			await controller.erase();
			await controller.erase();
			expect(snaps.at(-1)?.notice).toBe(LIVE_ERASE_FAILED_NOTICE);
			expect(usesBuildWord(snaps.at(-1)?.notice ?? '')).toBeNull();
		} finally {
			controller.destroy();
			vi.unstubAllGlobals();
		}
	});

	it('follows a started render without naming the reference', async () => {
		const { DraftController } = await import('./editor/draft');
		const detail = {
			episode: { id: 'live-9', number: 9, title: 'Live take', state: 'draft', visibility: 'private' },
			proposals: [],
			words: [{ text: 'Hello', start: 0, end: 0.4 }],
			audio_url: '',
			render_audio_url: ''
		};
		const fetchMock = vi.fn(async (input: unknown) => {
			const url = typeof input === 'string' ? input : String((input as Request)?.url ?? input);
			if (url.includes('/done')) {
				return Response.json({ job_id: 'r-1', queued: false }, { status: 202 });
			}
			if (url.includes('/api/jobs/r-1/events')) {
				return new Response(
					'id: 1\nevent: progress\ndata: {"job_id":"r-1","stage":"rendering","current":1,"total":4}\n\n',
					{ status: 200, headers: { 'content-type': 'text/event-stream' } }
				);
			}
			if (url.includes('/api/jobs/r-1')) {
				return Response.json({ jobId: 'r-1', status: 'running', current: 1, total: 4 });
			}
			return Response.json(detail);
		});
		vi.stubGlobal('fetch', fetchMock);
		const controller = new DraftController({ episodeId: 'live-9', onChange: () => {} });
		try {
			controller.mount('');
			await vi.waitFor(() => {
				expect(controller.snapshot.ready).toBe(true);
			});
			controller.markDone();
			controller.markDone();
			await vi.waitFor(() => {
				expect(controller.snapshot.renderStage).toBe('running');
			});
			expect(controller.snapshot.renderDetail).toBe(LIVE_RENDER_RUNNING_DETAIL);
			expect(usesBuildWord(controller.snapshot.renderDetail)).toBeNull();
		} finally {
			controller.destroy();
			vi.unstubAllGlobals();
		}
	});

	it('reads a finished render without naming the reference', async () => {
		const { DraftController } = await import('./editor/draft');
		const detail = (state: string): unknown => ({
			episode: { id: 'live-9', number: 9, title: 'Live take', state, visibility: 'private' },
			proposals: [],
			words: [{ text: 'Hello', start: 0, end: 0.4 }],
			audio_url: '',
			render_audio_url: ''
		});
		const fetchMock = vi.fn(async (input: unknown) => {
			const url = typeof input === 'string' ? input : String((input as Request)?.url ?? input);
			if (url.includes('/done')) {
				return Response.json({ job_id: 'r-9', queued: false }, { status: 202 });
			}
			if (url.includes('/api/jobs/r-9/events')) {
				return new Response('event: done\ndata: {"job_id":"r-9","status":"done"}\n\n', {
					status: 200,
					headers: { 'content-type': 'text/event-stream' }
				});
			}
			if (url.includes('/api/jobs/r-9')) {
				return Response.json({ jobId: 'r-9', status: 'done' });
			}
			return Response.json(detail('ready'));
		});
		vi.stubGlobal('fetch', fetchMock);
		const controller = new DraftController({ episodeId: 'live-9', onChange: () => {} });
		try {
			controller.mount('');
			await vi.waitFor(() => {
				expect(controller.snapshot.ready).toBe(true);
			});
			controller.markDone();
			controller.markDone();
			await vi.waitFor(
				() => {
					expect(controller.snapshot.renderStage).toBe('done');
				},
				{ timeout: 10_000 }
			);
			expect(controller.snapshot.renderDetail).toBe(LIVE_RENDER_DONE_READY_DETAIL);
			expect(usesBuildWord(controller.snapshot.renderDetail)).toBeNull();
		} finally {
			controller.destroy();
			vi.unstubAllGlobals();
		}
	});

	it('reads an unfinished render without naming the reference', async () => {
		const { DraftController } = await import('./editor/draft');
		const detail = (state: string): unknown => ({
			episode: { id: 'live-9', number: 9, title: 'Live take', state, visibility: 'private' },
			proposals: [],
			words: [{ text: 'Hello', start: 0, end: 0.4 }],
			audio_url: '',
			render_audio_url: ''
		});
		const fetchMock = vi.fn(async (input: unknown) => {
			const url = typeof input === 'string' ? input : String((input as Request)?.url ?? input);
			if (url.includes('/done')) {
				return Response.json({ job_id: 'r-9', queued: false }, { status: 202 });
			}
			if (url.includes('/api/jobs/r-9/events')) {
				return new Response('event: done\ndata: {"job_id":"r-9","status":"done"}\n\n', {
					status: 200,
					headers: { 'content-type': 'text/event-stream' }
				});
			}
			if (url.includes('/api/jobs/r-9')) {
				return Response.json({ jobId: 'r-9', status: 'done' });
			}
			return Response.json(detail('rendering'));
		});
		vi.stubGlobal('fetch', fetchMock);
		const controller = new DraftController({ episodeId: 'live-9', onChange: () => {} });
		try {
			controller.mount('');
			await vi.waitFor(() => {
				expect(controller.snapshot.ready).toBe(true);
			});
			controller.markDone();
			controller.markDone();
			await vi.waitFor(
				() => {
					expect(controller.snapshot.renderStage).toBe('done');
				},
				{ timeout: 10_000 }
			);
			expect(controller.snapshot.renderDetail).toBe(LIVE_RENDER_DONE_DETAIL);
			expect(usesBuildWord(controller.snapshot.renderDetail)).toBeNull();
		} finally {
			controller.destroy();
			vi.unstubAllGlobals();
		}
	});

	it('keeps the last step when live progress stops', async () => {
		const { readingFromJob } = await import('./voice/processing-state');
		const step = { name: 'Transcription', detail: 'Waiting.', state: 'waiting' as const, percent: 0 };
		const next = readingFromJob(
			step,
			'running',
			'failed',
			undefined,
			undefined,
			'',
			'Transcript ready.',
			'transcript'
		);
		expect(next.detail).toBe(PROGRESS_STOPPED_DETAIL);
		expect(usesBuildWord(next.detail)).toBeNull();
	});

	afterEach(() => {
		vi.unstubAllGlobals();
	});
});
