// The live words a guest reads. Every notice the three controllers show
// in live mode lives here as an exported constant, and this spec fails
// when one of them uses the build vocabulary instead of plain words.
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
	LIVE_DRAFT_EMPTY_NOTICE,
	LIVE_DRAFT_NOTICE,
	type DraftSnapshot
} from './editor/draft';
import { TRANSCRIBING_DETAIL } from './voice/processing-state';
import {
	EpisodeController,
	emptyScreen,
	GalleryController,
	LIVE_EPISODE_FAILED_NOTICE,
	LIVE_EPISODE_NOTICE,
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
		{ name: 'watching pass', text: TRANSCRIBING_DETAIL }
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

	afterEach(() => {
		vi.unstubAllGlobals();
	});
});
