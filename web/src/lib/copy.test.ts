// The live words a guest reads. Every notice the three controllers show
// in live mode lives here as an exported constant, and this spec fails
// when one of them uses the build vocabulary instead of plain words.
// That covers the erase outcome, the render pass details, and the
// stopped progress line beside the load notices.
import { afterEach, describe, expect, it, vi } from 'vitest';
import * as files from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
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
	LIVE_CARD_STALLED_NOTICE,
	LIVE_CARD_WAITING_NOTICE,
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
		{ name: 'card waiting', text: LIVE_CARD_WAITING_NOTICE },
		{ name: 'card stalled', text: LIVE_CARD_STALLED_NOTICE },
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

// Words no screen line shows a guest. The match runs on whole words,
// so a longer word that only holds one stays allowed.
const SHOWN_BANNED_WORDS = [
	'job',
	'stream',
	'endpoint',
	'detail',
	'backend',
	'fixture',
	'scripted',
	'ledger',
	'reservation',
	'provider',
	'stem',
	'render',
	'proposal',
	'mint',
	'artifact'
];

// Phrases no screen line shows, matched as written.
const SHOWN_BANNED_PHRASES = ['session row', 'no backend', 'survives a reload'];

// Longest sentence a person reads, in words.
const MAX_SHOWN_WORDS = 20;

// Export names that never reach a screen. Routes, codes, style text,
// event names and fixture markers stay out of the shown set.
const NON_SHOWN_EXPORT = /(^|_)(ID|PATH|PAGE|API|URL|TOKEN|KEY|CSS|EVENT|RATE|SECONDS)($|_)|FIXTURE|MISSING/;

interface ShownLine {
	name: string;
	text: string;
}

function bannedShownHit(text: string): string | null {
	const lower = text.toLowerCase();
	for (const word of SHOWN_BANNED_WORDS) {
		const found = lower.match(new RegExp(`\\b${word}s?\\b`));
		if (found) return found[0];
	}
	for (const phrase of SHOWN_BANNED_PHRASES) {
		if (lower.includes(phrase)) return phrase;
	}
	return null;
}

function longShownSentence(text: string): string | null {
	for (const sentence of text.split(/(?<=[.!?…])\s+/)) {
		const words = sentence.match(/[A-Za-z0-9'’]+/g) ?? [];
		if (words.length > MAX_SHOWN_WORDS) return sentence.trim();
	}
	return null;
}

function readRouteSources(): Array<{ path: string; source: string }> {
	const routesDir = join(dirname(fileURLToPath(import.meta.url)), '..', 'routes');
	const found: string[] = [];
	const walk = (dir: string): void => {
		// @ts-expect-error: the checked fs stub omits directory reads, the runtime provides them.
		for (const entry of files.readdirSync(dir, { withFileTypes: true })) {
			const full = join(dir, entry.name);
			if (entry.isDirectory()) walk(full);
			else if (entry.name.endsWith('.svelte')) found.push(full);
		}
	};
	walk(routesDir);
	return found.map((path) => ({ path, source: files.readFileSync(path, 'utf8') }));
}

// Shown strings are the text between tags plus the attributes a person
// reads. Screen reader labels count as shown, since a listener hears
// them. Identifiers, comments, component props and test hooks stay out.
function shownStrings(source: string): string[] {
	const out: string[] = [];
	let code = source.replace(/<script[\s\S]*?<\/script>/g, '');
	code = code.replace(/<style[\s\S]*?<\/style>/g, '');
	code = code.replace(/<!--[\s\S]*?-->/g, '');
	for (const match of code.matchAll(/>([^<>{}]+)</g)) {
		const text = (match[1] ?? '').trim().replace(/\s+/g, ' ');
		if (text.length > 0) out.push(text);
	}
	// Literals inside template expressions mix state tokens with labels
	// that render. A literal that reads like words counts as shown,
	// while a lowercase token, a route or a key stays out.
	for (const match of code.matchAll(/\{([^{}]*)\}/g)) {
		const expr = match[1] ?? '';
		for (const lit of expr.matchAll(/'([^']*)'|"([^"]*)"/g)) {
			const text = (lit[1] ?? lit[2] ?? '').trim().replace(/\s+/g, ' ');
			if (text.length < 2 || text.startsWith('/')) continue;
			const first = text[0] ?? '';
			const looksShown =
				text.includes(' ') || text.includes('…') || first !== first.toLowerCase();
			if (looksShown) out.push(text);
		}
	}
	for (const match of code.matchAll(/<([a-zA-Z][^\s/>]*)([^>]*)>/g)) {
		const tag = (match[1] ?? '').toLowerCase();
		const attrs = match[2] ?? '';
		if (tag === 'meta') {
			if (!/name="description"|property="og:description"|name="twitter:description"/.test(attrs)) {
				continue;
			}
			for (const content of attrs.matchAll(/content="([^"]*)"/g)) {
				const text = (content[1] ?? '').trim().replace(/\s+/g, ' ');
				if (text.length > 0 && !text.includes('{')) out.push(text);
			}
			continue;
		}
		for (const attr of attrs.matchAll(/(?:alt|placeholder|title|aria-label|aria-description)="([^"]*)"/g)) {
			const text = (attr[1] ?? '').trim().replace(/\s+/g, ' ');
			if (text.length > 0 && !text.includes('{')) out.push(text);
		}
	}
	return out;
}

async function controllerLines(): Promise<ShownLine[]> {
	const modules: Array<{ name: string; room: Record<string, unknown> }> = [
		{ name: 'threads', room: (await import('../routes/threads/threads')) as Record<string, unknown> },
		{ name: 'record-state', room: (await import('./voice/record-state')) as Record<string, unknown> },
		{
			name: 'processing-state',
			room: (await import('./voice/processing-state')) as Record<string, unknown>
		},
		{ name: 'draft', room: (await import('./editor/draft')) as Record<string, unknown> },
		{ name: 'account', room: (await import('../routes/account/account')) as Record<string, unknown> },
		{
			name: 'google',
			room: (await import('../routes/account/google/google')) as Record<string, unknown>
		},
		{ name: 'limits', room: (await import('../routes/admin/limits')) as Record<string, unknown> },
		{ name: 'welcome', room: (await import('../routes/welcome/welcome')) as Record<string, unknown> },
		{
			name: 'share',
			room: (await import('../routes/share/[token]/share')) as Record<string, unknown>
		}
	];
	const lines: ShownLine[] = [];
	for (const room of modules) {
		for (const [key, value] of Object.entries(room.room)) {
			if (typeof value !== 'string') continue;
			if (NON_SHOWN_EXPORT.test(key)) continue;
			if (value.length === 0) continue;
			lines.push({ name: `${room.name}.${key}`, text: value });
		}
	}
	// The welcome page renders the teaser quotes, which live one level
	// inside the exported teaser rather than beside it.
	const welcome = modules[7]?.room as { TEASER?: { lineA?: unknown; lineB?: unknown } };
	if (typeof welcome.TEASER?.lineA === 'string') {
		lines.push({ name: 'welcome.TEASER.lineA', text: welcome.TEASER.lineA });
	}
	if (typeof welcome.TEASER?.lineB === 'string') {
		lines.push({ name: 'welcome.TEASER.lineB', text: welcome.TEASER.lineB });
	}
	return lines;
}

describe('shown copy', () => {
	it('shows no banned word in any route template or page controller line', async () => {
		const violations: string[] = [];
		for (const file of readRouteSources()) {
			for (const text of shownStrings(file.source)) {
				const hit = bannedShownHit(text);
				if (hit) violations.push(`${file.path} shows "${text}" with banned "${hit}"`);
			}
		}
		for (const line of await controllerLines()) {
			const hit = bannedShownHit(line.text);
			if (hit) violations.push(`${line.name} reads "${line.text}" with banned "${hit}"`);
		}
		expect(violations).toEqual([]);
	});

	it('keeps every shown sentence to twenty words or fewer', async () => {
		const violations: string[] = [];
		for (const file of readRouteSources()) {
			for (const text of shownStrings(file.source)) {
				const hit = longShownSentence(text);
				if (hit) violations.push(`${file.path} shows a long sentence: "${hit}"`);
			}
		}
		for (const line of await controllerLines()) {
			const hit = longShownSentence(line.text);
			if (hit) violations.push(`${line.name} holds a long sentence: "${hit}"`);
		}
		expect(violations).toEqual([]);
	});
});
