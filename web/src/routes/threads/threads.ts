// Scripted season, quotes, and pure helpers behind the gallery, the
// episode view, and the thread panel. Fixtures carry no backend, so
// every screen renders with generated audio and quoted turns alone.
// Quotes always name an episode and an offset, and playback seeks to
// that offset when a thread link opens.
import { AudioPlayer } from '@nrynss/chaaya/audio';
import { JobStream, type JobSnapshot } from '@nrynss/chaaya/job';
import { activeWordAt } from '@nrynss/chaaya/transcript';

export type EpisodeState = 'ready' | 'rendering' | 'draft';

// One chapter marker on the finished timeline.
export interface Chapter {
	start: number;
	title: string;
}

// One spoken turn. Starts read in episode seconds.
export interface Turn {
	role: 'host' | 'you';
	start: number;
	text: string;
}

// One finished or in-flight episode.
export interface EpisodeFixture {
	id: string;
	number: number;
	title: string;
	date: string;
	duration: number;
	state: EpisodeState;
	jobId: string;
	published: boolean;
	notes: string;
	chapters: Chapter[];
	turns: Turn[];
}

// One timed word built from a turn.
export interface Word {
	start: number;
	end: number;
	text: string;
	speaker: string;
}

// One quote into the season. Offset seeks playback to the turn start.
export interface ThreadQuote {
	episode: string;
	offset: number;
	text: string;
}

// One thread card: a person, a promise, or a circling topic.
export interface ThreadItem {
	id: string;
	name: string;
	who: string;
	count: string;
	status: 'open' | 'resolved' | null;
	opened: string;
	quotes: ThreadQuote[];
}

const EPISODES: EpisodeFixture[] = [
	{
		id: 'ep-1',
		number: 1,
		title: 'The garden was ours first',
		date: 'Aug 8',
		duration: 70,
		state: 'ready',
		jobId: '',
		published: false,
		notes:
			'The first take. June, the pear tree, and why calling feels like losing. Nothing was decided. That was the whole point.',
		chapters: [
			{ start: 0, title: 'The dread, named' },
			{ start: 20, title: 'Say more about the dread' },
			{ start: 46, title: 'The pear tree' }
		],
		turns: [
			{
				role: 'host',
				start: 0,
				text: 'First take. There is no wrong way to do this. Start anywhere. What has been sitting with you?'
			},
			{
				role: 'you',
				start: 6,
				text: 'The garden, I guess. June and I share the allotment, or we did. I have been dreading calling her.'
			},
			{ role: 'host', start: 20, text: 'Dreading calling your sister. Say more about the dread.' },
			{
				role: 'you',
				start: 28,
				text: 'The garden was ours before it was the landlord’s. If I call, it becomes a conversation about the future.'
			},
			{ role: 'host', start: 46, text: 'And the pear tree?' },
			{
				role: 'you',
				start: 50,
				text: 'Grandma’s shears are still in the shed. I sharpen them every March whether anyone asks or not.'
			}
		]
	},
	{
		id: 'ep-2',
		number: 2,
		title: 'Rent, and what it costs to stay',
		date: 'Aug 15',
		duration: 70,
		state: 'ready',
		jobId: '',
		published: false,
		notes:
			'The arithmetic of the lease. Tomas’s fence, Margot’s tape, and a promise to ask June about the boundary, made out loud, so it counts now.',
		chapters: [
			{ start: 0, title: 'The letter from the landlord' },
			{ start: 20, title: 'The fence, whose job' },
			{ start: 40, title: 'Meaning to' }
		],
		turns: [
			{ role: 'host', start: 0, text: 'A letter came this week. What did it say?' },
			{
				role: 'you',
				start: 5,
				text: 'The lease on the house goes month to month in spring. The garden was never in it.'
			},
			{
				role: 'you',
				start: 20,
				text: 'Tomas says the fence leans like it is tired of holding the line. I keep meaning to ask June whose job the fence is now.'
			},
			{ role: 'host', start: 40, text: 'You said meaning to. How long has that been true?' },
			{
				role: 'you',
				start: 46,
				text: 'Since May. I promised Margot a tape of the allotment recordings too. I record the birds there some mornings.'
			}
		]
	},
	{
		id: 'ep-3',
		number: 3,
		title: 'June, in every other sentence',
		date: 'Aug 26',
		duration: 55,
		state: 'ready',
		jobId: '',
		published: false,
		notes:
			'Three weeks in. The host stopped asking about June because I kept volunteering her. The weather exchange. Coward arithmetic, itemized.',
		chapters: [
			{ start: 0, title: 'Nine mentions' },
			{ start: 18, title: 'The weather exchange' },
			{ start: 26, title: 'Coward arithmetic' }
		],
		turns: [
			{
				role: 'host',
				start: 0,
				text: 'June came up in every other sentence this week. I stopped counting halfway through.'
			},
			{
				role: 'you',
				start: 7,
				text: 'She texted about the pear tree. I answered about the weather. That was the whole exchange.'
			},
			{ role: 'host', start: 18, text: 'What are you protecting with the weather talk?' },
			{
				role: 'you',
				start: 26,
				text: 'Every October I do this arithmetic. Stay another winter, or go down for the harvest. Every March I am still here.'
			}
		]
	},
	{
		id: 'ep-4',
		number: 4,
		title: 'Three weeks of almost',
		date: 'Sep 2',
		duration: 120,
		state: 'ready',
		jobId: '',
		published: false,
		notes:
			'Recorded in one take on a Tuesday. June came up before the coffee was done. By the end the question was what I am afraid she will say when I call. The host kept the pauses in.',
		chapters: [
			{ start: 0, title: 'The almost' },
			{ start: 40, title: 'What she carries' },
			{ start: 92, title: 'The harvest in October' },
			{ start: 108, title: 'The tape, sent' }
		],
		turns: [
			{
				role: 'host',
				start: 0,
				text: 'Three weeks of almost. The garden is the only place nobody needs anything from you. Your words, episode one. What has been said since?'
			},
			{
				role: 'you',
				start: 14,
				text: 'Nothing. She texted about the pear tree. I answered about the weather. That is the whole exchange, three weeks of it.'
			},
			{
				role: 'host',
				start: 40,
				text: 'The pear tree is doing its work, then. What do you think she is carrying that you are not?'
			},
			{
				role: 'you',
				start: 55,
				text: 'The inheritance of it. Grandma’s shears, the shade cloth, the argument about the roses. I kept the garden.'
			},
			{ role: 'host', start: 92, text: 'And the harvest in October?' },
			{
				role: 'you',
				start: 101,
				text: 'I keep not thinking about it. Which is its own kind of thinking about it.'
			},
			{
				role: 'you',
				start: 108,
				text: 'I sent Margot the tape. She cried on the phone. The good kind.'
			}
		]
	},
	{
		id: 'ep-5',
		number: 5,
		title: 'The only place nobody needs anything',
		date: 'Today',
		duration: 100,
		state: 'ready',
		jobId: '',
		published: false,
		notes:
			'She asked me to the harvest. I said yes on a Thursday, after a night of no sleep. The host kept the pause before I said it. That pause is the episode.',
		chapters: [
			{ start: 0, title: 'Cold open, the only place' },
			{ start: 14, title: 'She asked' },
			{ start: 44, title: 'If you go' },
			{ start: 70, title: 'The year of avoiding' }
		],
		turns: [
			{
				role: 'host',
				start: 0,
				text: 'Episode five. Four episodes in. You know what I am opening with.'
			},
			{ role: 'you', start: 6, text: 'June.' },
			{
				role: 'host',
				start: 8,
				text: 'June. Nine mentions, three weeks of almost. What changed this week?'
			},
			{
				role: 'you',
				start: 14,
				text: 'She asked if I would come down for the harvest. The first time she has asked me for something.'
			},
			{ role: 'host', start: 40, text: 'And if you go?' },
			{
				role: 'you',
				start: 44,
				text: 'Then it becomes something we share again. If it goes badly, at least it is over.'
			},
			{ role: 'host', start: 70, text: 'The year you avoided it. What were you doing instead?' },
			{ role: 'you', start: 76, text: 'Tomas’s fence. Keeping busy with things that cannot say no.' }
		]
	}
];

const THREADS_BASE: { commitments: ThreadItem[]; people: ThreadItem[]; topics: ThreadItem[] } = {
	commitments: [
		{
			id: 'ask-june-fence',
			name: 'Ask June about the fence',
			who: 'a promise, recorded',
			count: '2 mentions · still open',
			status: 'open',
			opened: 'EP.02',
			quotes: [
				{
					episode: 'ep-2',
					offset: 20,
					text: 'I keep meaning to ask June whose job the fence is now.'
				},
				{ episode: 'ep-5', offset: 76, text: 'Keeping busy with things that cannot say no.' }
			]
		},
		{
			id: 'margot-tape',
			name: 'Send Margot the tape',
			who: 'a promise, kept',
			count: 'kept in EP.04',
			status: 'resolved',
			opened: 'EP.02',
			quotes: [
				{ episode: 'ep-2', offset: 46, text: 'I promised Margot a tape of the allotment recordings' },
				{ episode: 'ep-4', offset: 108, text: 'I sent Margot the tape. She cried on the phone.' }
			]
		}
	],
	people: [
		{
			id: 'june',
			name: 'June',
			who: 'your sister',
			count: '9 mentions · 4 episodes',
			status: null,
			opened: '',
			quotes: [
				{ episode: 'ep-1', offset: 6, text: 'June and I share the allotment, or we did.' },
				{ episode: 'ep-4', offset: 14, text: 'She texted about the pear tree.' }
			]
		},
		{
			id: 'tomas',
			name: 'Tomas',
			who: 'the neighbour at the allotment',
			count: '3 mentions · 2 episodes',
			status: null,
			opened: '',
			quotes: [
				{ episode: 'ep-2', offset: 20, text: 'Tomas says the fence leans like it is tired of holding the line.' },
				{ episode: 'ep-5', offset: 76, text: 'Keeping busy with things that cannot say no.' }
			]
		},
		{
			id: 'margot',
			name: 'Margot',
			who: 'an old friend',
			count: '2 mentions · 2 episodes',
			status: null,
			opened: '',
			quotes: [
				{ episode: 'ep-2', offset: 46, text: 'I promised Margot a tape of the allotment recordings' },
				{ episode: 'ep-4', offset: 108, text: 'I sent Margot the tape.' }
			]
		}
	],
	topics: [
		{
			id: 'harvest',
			name: 'The harvest in October',
			who: '',
			count: '3 mentions · 3 episodes · unresolved',
			status: null,
			opened: '',
			quotes: [
				{ episode: 'ep-3', offset: 26, text: 'or go down for the harvest.' },
				{ episode: 'ep-4', offset: 92, text: 'And the harvest in October?' }
			]
		},
		{
			id: 'winter',
			name: 'Another winter in the city',
			who: '',
			count: '2 mentions · 2 episodes · unresolved',
			status: null,
			opened: '',
			quotes: [
				{ episode: 'ep-2', offset: 5, text: 'The lease on the house goes month to month in spring.' },
				{ episode: 'ep-3', offset: 26, text: 'Stay another winter, or go down for the harvest.' }
			]
		}
	]
};

// What episode five adds to the threads once it is ready.
const THREADS_AFTER_FIVE: { people: ThreadQuote[]; commitments: ThreadItem[] } = {
	people: [{ episode: 'ep-5', offset: 14, text: 'She asked if I would come down for the harvest.' }],
	commitments: [
		{
			id: 'go-harvest',
			name: 'Go down for the harvest',
			who: 'a promise, fresh',
			count: 'made today · first weekend of October',
			status: 'open',
			opened: 'EP.05',
			quotes: [{ episode: 'ep-5', offset: 44, text: 'Then it becomes something we share again.' }]
		}
	]
};

export type FifthState = 'none' | 'draft' | 'rendering' | 'ready';

export const RENDER_JOB_ID = 'job-render-5';

// The season newest first. The fifth episode joins in its lab state,
// rendering with the job the card follows, draft waiting in the editor,
// or ready with its turns. Four episodes is the base.
export function listSeason(fifth: FifthState): EpisodeFixture[] {
	const base = EPISODES.filter((episode) => episode.number < 5).map((episode) => ({ ...episode }));
	const ordered = base.reverse();
	if (fifth === 'none') return ordered;
	const fifthEpisode = EPISODES.find((episode) => episode.number === 5);
	if (!fifthEpisode) return ordered;
	if (fifth === 'rendering') {
		return [{ ...fifthEpisode, state: 'rendering', jobId: RENDER_JOB_ID }, ...ordered];
	}
	if (fifth === 'draft') {
		return [{ ...fifthEpisode, state: 'draft', jobId: '' }, ...ordered];
	}
	return [{ ...fifthEpisode, state: 'ready', jobId: '' }, ...ordered];
}

// One episode by id, or undefined for an unknown id.
export function episodeById(id: string): EpisodeFixture | undefined {
	return EPISODES.find((episode) => episode.id === id);
}

// Seconds as m:ss for readouts, cards, and quote chips.
export function formatClock(seconds: number): string {
	const clamped = Math.max(0, Math.round(seconds));
	return `${Math.floor(clamped / 60)}:${String(clamped % 60).padStart(2, '0')}`;
}

// Episode counter as EP.04 beside titles and quotes.
export function formatEpisodeNumber(number: number): string {
	return `EP.${String(number).padStart(2, '0')}`;
}

// The season number behind an episode id such as ep-2, or zero when
// the id carries none.
export function episodeNumber(id: string): number {
	const parsed = Number(id.slice(3));
	return Number.isFinite(parsed) ? parsed : 0;
}

// The deep link a thread quote opens: the episode with the playhead
// parked at the quote and playback cued.
export function quoteHref(episode: string, offset: number): `/episode/${string}?${string}` {
	return `/episode/${episode}?fixture=1&t=${offset}&play=1`;
}

// Read one query value by iterating the entries in order. The first
// match answers, and a missing name reads null.
export function queryValue(search: string, name: string): string | null {
	for (const [key, value] of new URLSearchParams(search)) {
		if (key === name) return value;
	}
	return null;
}

// Timed words from turns. Each word follows the last by a fixed beat,
// so quotes land on exact word edges with no clock behind them.
export function buildWords(turns: ReadonlyArray<Turn>): Word[] {
	const words: Word[] = [];
	for (const turn of turns) {
		const parts = turn.text.split(' ');
		for (let index = 0; index < parts.length; index += 1) {
			const start = turn.start + index * 0.32;
			words.push({ start, end: start + 0.28, text: parts[index] ?? '', speaker: turn.role });
		}
	}
	return words;
}

// The whole transcript as one string, for quote checks.
export function transcriptText(turns: ReadonlyArray<Turn>): string {
	return turns.map((turn) => turn.text).join(' ');
}

// True when the quote text appears in the episode transcript.
export function quoteInTranscript(episodeId: string, text: string): boolean {
	const episode = episodeById(episodeId);
	if (!episode) return false;
	return transcriptText(episode.turns).includes(text);
}

// The thread panel. Episode five joins the people counts and adds its
// fresh commitment once it is ready.
export function listThreads(fifthReady: boolean): {
	commitments: ThreadItem[];
	people: ThreadItem[];
	topics: ThreadItem[];
} {
	const commitments = THREADS_BASE.commitments.map((item) => ({
		...item,
		quotes: item.quotes.map((quote) => ({ ...quote }))
	}));
	const people = THREADS_BASE.people.map((item) => ({
		...item,
		quotes: item.quotes.map((quote) => ({ ...quote }))
	}));
	const topics = THREADS_BASE.topics.map((item) => ({
		...item,
		quotes: item.quotes.map((quote) => ({ ...quote }))
	}));
	if (fifthReady) {
		const june = people.find((item) => item.id === 'june');
		if (june) {
			june.count = '10 mentions · 5 episodes';
			for (const quote of THREADS_AFTER_FIVE.people) june.quotes.push({ ...quote });
		}
		for (const item of THREADS_AFTER_FIVE.commitments) {
			commitments.unshift({
				...item,
				quotes: item.quotes.map((quote) => ({ ...quote }))
			});
		}
	}
	return { commitments, people, topics };
}

// Whole percent of a job from its work counters.
export function progressPercent(current: number, total: number): number {
	if (!(total > 0)) return 0;
	const clamped = Math.min(Math.max(current, 0), total);
	return Math.round((clamped / total) * 100);
}

// The storage seam behind the high-water mark. Plain methods only, so
// logic checks inject memory and the page injects the browser store.
export interface WaterStore {
	read(key: string): string | null;
	write(key: string, value: string): void;
}

const WATER_PREFIX = 'season-job-water:';

// The highest percent one job ever showed, or zero before the first.
export function readHighWater(store: WaterStore | null, jobId: string): number {
	if (!store) return 0;
	try {
		const raw = store.read(`${WATER_PREFIX}${jobId}`);
		const parsed = raw === null ? Number.NaN : Number(raw);
		if (!Number.isFinite(parsed)) return 0;
		return Math.min(Math.max(Math.round(parsed), 0), 100);
	} catch {
		return 0;
	}
}

// Raise the mark to at least percent and answer the mark. The card
// renders the answer, so a replayed stream never drags it backwards.
export function writeHighWater(store: WaterStore | null, jobId: string, percent: number): number {
	const mark = Math.min(Math.max(Math.round(percent), 0), 100);
	const held = readHighWater(store, jobId);
	const next = Math.max(held, mark);
	if (store && next > held) {
		try {
			store.write(`${WATER_PREFIX}${jobId}`, String(next));
		} catch {
			// A refused store still leaves the live mark for this run.
		}
	}
	return next;
}

// The browser store, or null where no window lives, such as prerender.
export function browserStore(): WaterStore | null {
	try {
		if (typeof window === 'undefined' || !window.localStorage) return null;
		const storage = window.localStorage;
		return {
			read: (key) => storage.getItem(key),
			write: (key, value) => storage.setItem(key, value)
		};
	} catch {
		return null;
	}
}

export const FIXTURE_RATE = 8000;

// One channel of alternating tones with pauses, so playback has shape
// and seeking has somewhere to land. Deterministic by seed.
export function buildTone(duration: number, seed: number): Float32Array {
	const frames = Math.max(1, Math.floor(duration * FIXTURE_RATE));
	const out = new Float32Array(frames);
	let state = 0x2f6e2b1 + Math.floor(seed * 7919);
	const next = (): number => {
		state = (state * 1103515245 + 12345) & 0x7fffffff;
		return state / 0x7fffffff - 0.5;
	};
	for (let frame = 0; frame < frames; frame += 1) {
		const second = frame / FIXTURE_RATE;
		const phrase = Math.floor(second / 3) % 2 === 0;
		const tone =
			Math.sin(2 * Math.PI * 220 * second) * 0.4 + Math.sin(2 * Math.PI * 330 * second) * 0.2;
		out[frame] = tone * (phrase ? 1 : 0.05) + next() * 0.02;
	}
	return out;
}

// A mono 16-bit WAV around one channel, as bytes. Logic checks read
// them with no DOM. The page wraps them in a blob URL.
export function encodeWavBytes(channel: Float32Array, rate: number): Uint8Array {
	const frames = channel.length;
	const buffer = new ArrayBuffer(44 + frames * 2);
	const view = new DataView(buffer);
	const writeText = (offset: number, text: string): void => {
		for (let i = 0; i < text.length; i += 1) view.setUint8(offset + i, text.charCodeAt(i));
	};
	writeText(0, 'RIFF');
	view.setUint32(4, 36 + frames * 2, true);
	writeText(8, 'WAVE');
	writeText(12, 'fmt ');
	view.setUint32(16, 16, true);
	view.setUint16(20, 1, true);
	view.setUint16(22, 1, true);
	view.setUint32(24, rate, true);
	view.setUint32(28, rate * 2, true);
	view.setUint16(32, 2, true);
	view.setUint16(34, 16, true);
	writeText(36, 'data');
	view.setUint32(40, frames * 2, true);
	for (let frame = 0; frame < frames; frame += 1) {
		const sample = Math.max(-1, Math.min(1, channel[frame] ?? 0));
		view.setInt16(44 + frame * 2, Math.round(sample * 32767), true);
	}
	return new Uint8Array(buffer);
}

const audioUrls: Record<string, string> = {};

// A playable URL for one fixture episode. Built once per id from the
// generated tone at the episode length, or empty where the runtime
// holds no blob URLs. Empty keeps logic checks DOM-free.
export function episodeAudioUrl(episode: EpisodeFixture): string {
	const held = audioUrls[episode.id];
	if (held !== undefined) return held;
	try {
		if (typeof Blob === 'undefined' || typeof URL.createObjectURL !== 'function') {
			audioUrls[episode.id] = '';
			return '';
		}
		const bytes = encodeWavBytes(buildTone(episode.duration, episode.number), FIXTURE_RATE);
		const url = URL.createObjectURL(new Blob([bytes.buffer as ArrayBuffer], { type: 'audio/wav' }));
		audioUrls[episode.id] = url;
		return url;
	} catch {
		audioUrls[episode.id] = '';
		return '';
	}
}

// One progress frame for a scripted job stream, in SSE wire shape.
export function progressFrame(jobId: string, stage: string, current: number, total: number, id = 0): string {
	return `id: ${id}\nevent: progress\ndata: ${JSON.stringify({ job_id: jobId, stage, current, total })}\n\n`;
}

export interface MockJobOptions {
	total?: number;
	steps?: number[];
	stepMs?: number;
}

// Serve one scripted render job behind the job endpoints the card
// follows. Frames arrive on timers and the stream stays open, the way
// a long render would. Returns the fetch restore for teardown.
export function installMockJob(jobId: string, options?: MockJobOptions): () => void {
	const total = options?.total ?? 4;
	const steps = options?.steps ?? [1, 2, 3];
	const stepMs = options?.stepMs ?? 400;
	const realFetch = window.fetch.bind(window);
	const timers: number[] = [];
	const json = (status: number, value: unknown): Response =>
		new Response(JSON.stringify(value), {
			status,
			headers: { 'content-type': 'application/json' }
		});
	window.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
		const url =
			typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
		const match = url.match(/\/api\/jobs\/([^/]+)(\/events)?$/);
		if (match !== null && match[1] === jobId) {
			if (match[2] === '/events') {
				const stream = new ReadableStream<Uint8Array>({
					start(controller) {
						const encoder = new TextEncoder();
						steps.forEach((step, index) => {
							timers.push(
								window.setTimeout(() => {
									controller.enqueue(
										encoder.encode(progressFrame(jobId, 'rendering', step, total, index + 1))
									);
								}, stepMs * (index + 1))
							);
						});
					},
					cancel() {
						for (const timer of timers) window.clearTimeout(timer);
					}
				});
				return new Response(stream, { headers: { 'content-type': 'text/event-stream' } });
			}
			return json(200, {
				jobId,
				status: 'running',
				stage: 'rendering',
				current: 1,
				total
			});
		}
		return realFetch(input, init);
	}) as typeof window.fetch;
	return () => {
		for (const timer of timers) window.clearTimeout(timer);
		if (window.fetch !== realFetch) window.fetch = realFetch;
	};
}

// The season palette as data. Screens draw these values and the
// contrast gate measures this same text, so the two cannot drift.
export const SEASON_THEME_CSS = [
	':root {',
	'color-scheme: dark;',
	'--paper: #191410;',
	'--raised: #241d15;',
	'--ink: #f4edde;',
	'--muted: #d9cfbb;',
	'--accent: #e8a33d;',
	'--on-accent: #201809;',
	'--line: #5a4f41;',
	'}'
].join('\n');

// Every foreground and background pair the season screens draw as text.
export const SEASON_CONTRAST_PAIRS: ReadonlyArray<readonly [string, string]> = [
	['ink', 'paper'],
	['muted', 'paper'],
	['ink', 'raised'],
	['muted', 'raised'],
	['accent', 'paper'],
	['on-accent', 'accent']
];

// Run both library gates against a rendered season screen. Returns the
// one line the screen shows.
export async function runSeasonGates(root: HTMLElement): Promise<string> {
	try {
		const { a11yGate, contrastGate } = await import('@nrynss/chaaya/testing');
		await a11yGate(root);
		contrastGate(SEASON_THEME_CSS, SEASON_CONTRAST_PAIRS);
		return 'Gates passed: accessibility and contrast.';
	} catch (error) {
		return `Gate failed: ${error instanceof Error ? error.message : 'unknown'}`;
	}
}

// One rendering card as the gallery draws it.
export interface GalleryCardProgress {
	jobId: string;
	percent: number;
	detail: string;
	running: boolean;
}

// Everything the gallery renders.
export interface GallerySnapshot {
	ready: boolean;
	notice: string;
	season: EpisodeFixture[];
	progress: Record<string, GalleryCardProgress>;
	gateResult: string;
}

export function emptyGallery(): GallerySnapshot {
	return { ready: false, notice: 'Loading the season.', season: [], progress: {}, gateResult: '' };
}

// The gallery snapshot before any job stream opens. Pure, so the
// server render carries the same season links the crawler follows.
export function initialGallery(search = ''): GallerySnapshot {
	const fifth = queryValue(search, 'fifth');
	const state: FifthState =
		fifth === 'draft' || fifth === 'rendering' || fifth === 'ready' || fifth === 'none'
			? fifth
			: 'rendering';
	return {
		ready: true,
		notice: 'Scripted season. No backend needed.',
		season: listSeason(state),
		progress: {},
		gateResult: ''
	};
}

// The gallery behind the season screen. It draws the scripted season
// with the fifth episode in its lab state and follows every rendering
// job through the same follower the processing screen reads, so the
// card and that screen never drift into two mechanisms. Progress
// renders the high-water mark, so a replayed stream never drags the
// card backwards, across a reload included.
export class GalleryController {
	private snap: GallerySnapshot;
	private readonly onChange: (snap: GallerySnapshot) => void;
	private streams: Array<{ jobId: string; stream: JobStream }> = [];
	private restoreMock: (() => void) | null = null;
	private timer: number | null = null;

	constructor(onChange: (snap: GallerySnapshot) => void) {
		this.onChange = onChange;
		this.snap = emptyGallery();
	}

	mount(search: string): void {
		const snapshot = initialGallery(search);
		this.restoreMock = installMockJob(RENDER_JOB_ID);
		this.snap = { ...this.snap, season: snapshot.season, ready: true, notice: snapshot.notice };
		for (const episode of this.snap.season) {
			if (episode.state === 'rendering' && episode.jobId) this.followJob(episode.jobId);
		}
		this.emit();
		this.refresh();
		this.timer = window.setInterval(() => this.refresh(), 500);
		const target = window as unknown as Record<string, unknown>;
		target['__gallery'] = {
			progress: () => {
				const cards = Object.values(this.snap.progress);
				return cards.length > 0 ? (cards[0]?.percent ?? 0) : 0;
			}
		};
		if (queryValue(search, 'gate') === '1') {
			const main = document.querySelector('main');
			if (main) {
				void runSeasonGates(main).then((result) => {
					this.snap = { ...this.snap, gateResult: result };
					this.emit();
				});
			}
		}
	}

	destroy(): void {
		if (this.timer !== null) {
			window.clearInterval(this.timer);
			this.timer = null;
		}
		for (const entry of this.streams) entry.stream.close();
		this.streams = [];
		this.restoreMock?.();
		this.restoreMock = null;
	}

	// The progress one rendering card shows, or the idle card before
	// the first stream reading lands.
	cardFor(jobId: string): GalleryCardProgress {
		const held = this.snap.progress[jobId];
		if (held) return held;
		return { jobId, percent: 0, detail: 'Waiting for the render.', running: true };
	}

	private emit(): void {
		this.onChange({ ...this.snap, season: [...this.snap.season], progress: { ...this.snap.progress } });
	}

	private followJob(jobId: string): void {
		const seed = readHighWater(browserStore(), jobId);
		this.snap = {
			...this.snap,
			progress: {
				...this.snap.progress,
				[jobId]: {
					jobId,
					percent: seed,
					detail: seed > 0 ? `Rendering · ${seed}% · kept across reload` : 'Rendering · starting.',
					running: true
				}
			}
		};
		const stream = new JobStream({
			url: `/api/jobs/${encodeURIComponent(jobId)}/events`,
			fetchState: async (): Promise<JobSnapshot> => {
				const response = await fetch(`/api/jobs/${encodeURIComponent(jobId)}`);
				if (!response.ok) throw new Error(`job ${response.status}`);
				return (await response.json()) as JobSnapshot;
			}
		});
		this.streams.push({ jobId, stream });
		stream.attach();
	}

	private refresh(): void {
		let changed = false;
		const next: Record<string, GalleryCardProgress> = { ...this.snap.progress };
		for (const entry of this.streams) {
			const live = progressPercent(entry.stream.current ?? 0, entry.stream.total ?? 4);
			const held = next[entry.jobId]?.percent ?? 0;
			const percent = writeHighWater(browserStore(), entry.jobId, Math.max(live, held));
			const running = entry.stream.status === 'queued' || entry.stream.status === 'running';
			const detail =
				entry.stream.status === 'done'
					? 'Render ready.'
					: entry.stream.connection === 'failed'
						? 'The job stream failed. The mark above is kept.'
						: `Rendering · ${percent}% · progress survives a reload`;
			if (!next[entry.jobId] || next[entry.jobId]?.percent !== percent || next[entry.jobId]?.detail !== detail) {
				next[entry.jobId] = { jobId: entry.jobId, percent, detail, running };
				changed = true;
			}
		}
		if (changed) {
			this.snap = { ...this.snap, progress: next };
			this.emit();
		}
	}
}

// Everything the episode view renders.
export interface EpisodeSnapshot {
	ready: boolean;
	notice: string;
	episode: EpisodeFixture | null;
	words: Word[];
	position: number;
	playing: boolean;
	published: boolean;
	eraseArmed: boolean;
	activeWord: number | null;
	activeChapter: number;
	gateResult: string;
}

export function emptyEpisode(): EpisodeSnapshot {
	return {
		ready: false,
		notice: 'Loading the episode.',
		episode: null,
		words: [],
		position: 0,
		playing: false,
		published: false,
		eraseArmed: false,
		activeWord: null,
		activeChapter: 0,
		gateResult: ''
	};
}

// The episode behind its view. Fixture words carry the timings, the
// player carries playback, and every seek parks the readout first, so
// a refused play still leaves the playhead where the quote starts.
// Publish, erase, and export render their states here. Their actions
// live elsewhere, so each control degrades to a notice in the fixture.
export class EpisodeController {
	readonly episodeId: string;
	private snap: EpisodeSnapshot;
	private readonly onChange: (snap: EpisodeSnapshot) => void;
	private player: AudioPlayer | null = null;
	private ticker: number | null = null;

	constructor(episodeId: string, onChange: (snap: EpisodeSnapshot) => void) {
		this.episodeId = episodeId;
		this.onChange = onChange;
		this.snap = emptyEpisode();
	}

	mount(search: string): void {
		const found = episodeById(this.episodeId);
		if (!found) {
			this.snap = { ...this.snap, ready: true, notice: 'No scripted episode carries that id.' };
			this.emit();
			return;
		}
		const episode = { ...found };
		const words = buildWords(episode.turns);
		this.player?.pause();
		this.player = new AudioPlayer();
		const url = episodeAudioUrl(episode);
		if (url) this.player.load(url);
		this.snap = {
			...this.snap,
			ready: true,
			notice: 'Scripted episode. No backend needed.',
			episode,
			words,
			published: queryValue(search, 'published') === '1'
		};
		const at = Number(queryValue(search, 't') ?? '');
		if (Number.isFinite(at) && at > 0) {
			this.seekTo(Math.min(at, episode.duration));
			this.snap = {
				...this.snap,
				notice:
					queryValue(search, 'play') === '1'
						? `Playhead at the quoted moment, ${formatClock(this.snap.position)}. Press play to hear it from there.`
						: `Playhead at the quoted moment, ${formatClock(this.snap.position)}.`
			};
		}
		this.emit();
		const target = window as unknown as Record<string, unknown>;
		target['__episode'] = {
			position: () => this.snap.position,
			playing: () => this.snap.playing,
			quote: () => (Number.isFinite(at) && at > 0 ? at : null),
			failure: () => this.player?.error ?? null,
			source: () => this.player?.source ?? null
		};
		if (queryValue(search, 'gate') === '1') {
			const main = document.querySelector('main');
			if (main) {
				void runSeasonGates(main).then((result) => {
					this.snap = { ...this.snap, gateResult: result };
					this.emit();
				});
			}
		}
	}

	destroy(): void {
		if (this.ticker !== null) {
			window.clearInterval(this.ticker);
			this.ticker = null;
		}
		this.player?.pause();
		this.player = null;
	}

	seekTo(seconds: number): void {
		if (!this.snap.episode) return;
		const clamped = Math.max(0, Math.min(this.snap.episode.duration, seconds));
		this.player?.seek(clamped);
		this.snap = { ...this.snap, position: clamped };
		this.follow();
		this.emit();
	}

	seekWord(index: number): void {
		const word = this.snap.words[index];
		if (!word) return;
		this.player?.seek(word.start);
		this.snap = { ...this.snap, position: word.start };
		this.follow();
		this.emit();
	}

	backFifteen(): void {
		this.seekTo(this.snap.position - 15);
	}

	async togglePlay(): Promise<void> {
		if (!this.player || !this.snap.episode) return;
		if (this.player.playing) {
			this.player.pause();
			this.snap = { ...this.snap, playing: false, position: this.player.currentTime || this.snap.position };
			this.emit();
			return;
		}
		const ok = await this.player.play();
		this.snap = {
			...this.snap,
			playing: ok,
			position: this.player.currentTime || this.snap.position,
			notice: ok ? `Playing from ${formatClock(this.player.currentTime || this.snap.position)}.` : 'Playback refused. Press play again.'
		};
		this.emit();
		if (ok) this.startTicker();
	}

	async togglePlayIfPaused(): Promise<void> {
		if (!this.player || this.player.playing) return;
		await this.togglePlay();
	}

	publishState(): void {
		const published = !this.snap.published;
		this.snap = {
			...this.snap,
			published,
			notice: published
				? 'Fixture link public at the address below. The publish endpoint owns real links.'
				: 'Back to private. The publish endpoint owns real links.'
		};
		this.emit();
	}

	async erase(): Promise<void> {
		if (!this.snap.eraseArmed) {
			this.snap = { ...this.snap, eraseArmed: true, notice: 'Erase works in two steps. Press again to confirm the erase.' };
			this.emit();
			return;
		}
		this.snap = { ...this.snap, eraseArmed: false };
		if (!this.snap.episode) {
			this.emit();
			return;
		}
		try {
			const response = await fetch(`/api/episodes/${encodeURIComponent(this.snap.episode.id)}`, {
				method: 'DELETE'
			});
			this.snap = {
				...this.snap,
				notice: response.ok
					? 'The erase endpoint accepted. The job stream carries it from here.'
					: 'The erase endpoint refused, so the fixture episode stays.'
			};
		} catch {
			this.snap = { ...this.snap, notice: 'The erase endpoint refused, so the fixture episode stays.' };
		}
		this.emit();
	}

	exportNotes(): void {
		this.snap = { ...this.snap, notice: 'Export needs the backend. The fixture stays on this screen.' };
		this.emit();
	}

	private follow(): void {
		this.snap = {
			...this.snap,
			activeWord: activeWordAt(this.snap.words, [], this.snap.position),
			activeChapter: this.chapterAt(this.snap.position)
		};
	}

	private chapterAt(position: number): number {
		if (!this.snap.episode) return 0;
		let current = 0;
		this.snap.episode.chapters.forEach((chapter, index) => {
			if (position >= chapter.start) current = index;
		});
		return current;
	}

	private startTicker(): void {
		if (this.ticker !== null || !this.player) return;
		const clock = this.player;
		this.ticker = window.setInterval(() => {
			this.snap = {
				...this.snap,
				position: clock.currentTime || this.snap.position,
				playing: clock.playing
			};
			this.follow();
			this.emit();
			if (!clock.playing && this.ticker !== null) {
				window.clearInterval(this.ticker);
				this.ticker = null;
			}
		}, 250);
	}

	private emit(): void {
		this.onChange({ ...this.snap });
	}
}
