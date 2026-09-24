// Scripted season, quotes, and pure helpers behind the gallery, the
// episode view, and the thread panel. Fixtures carry no backend, so
// every screen renders with generated audio and quoted turns alone.
// Quotes always name an episode and an offset, and playback seeks to
// that offset when a thread link opens.
import { AudioPlayer } from '@nrynss/chaaya/audio';
import { isTerminalStatus, JobStream, type JobSnapshot, type JobStatus } from '@nrynss/chaaya/job';
import { activeWordAt, toEditedTime } from '@nrynss/chaaya/transcript';

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

// One rendering card as the gallery draws it. Status is the latest job
// state the card knows.
export interface GalleryCardProgress {
	jobId: string;
	percent: number;
	detail: string;
	running: boolean;
	status: JobStatus;
}

// Which card one gallery row draws. A row whose job has not finished
// keeps its progress card, draft or not, so a running pass shows its
// progress and a failed pass shows its text. A draft opens the editor
// once its job is done, or when it names no job. Any other row with a
// job keeps its progress card, and the rest open the episode.
export type GalleryCardKind = 'job' | 'editor' | 'episode';

export function galleryCardKind(row: SeasonRow, card: GalleryCardProgress | null): GalleryCardKind {
	if (row.state === 'draft' && (!row.jobId || card?.status === 'done')) return 'editor';
	if (row.jobId) return 'job';
	return 'episode';
}

// Where the link on a progress card goes. A live draft whose pass has
// not finished opens the episode, which shows the pass state, because
// the editor has no words to show yet. Every other row follows its
// season link.
export function jobCardHref(row: SeasonRow): SeasonHref {
	if (row.state === 'draft' && !row.fixture) return `/episode/${row.id}`;
	return seasonHref(row);
}

// One gallery row in either mode. Fixture rows carry a duration and a
// cover badge from the scripted season. Live rows carry what the list
// handler answers: id, number, title, state, and visibility. Duration
// stays null until the detail exposes it, and the card omits the line.
export interface SeasonRow {
	id: string;
	number: number;
	title: string;
	state: string;
	visibility: string;
	meta: string;
	duration: number | null;
	jobId: string;
	fixture: boolean;
}

// A fixture episode as a gallery row. Ready rows open the episode,
// drafts open the editor, and rendering rows open the processing
// screen that reads the same job stream as the card.
export function fixtureRow(episode: EpisodeFixture): SeasonRow {
	return {
		id: episode.id,
		number: episode.number,
		title: episode.title,
		state: episode.state,
		visibility: episode.published ? 'public' : 'private',
		meta: `${episode.date} · ${formatClock(episode.duration)}`,
		duration: episode.duration,
		jobId: episode.jobId,
		fixture: true
	};
}

// A listed episode as a gallery row. The detail fills the job id later
// for rows that still run. Drafts open the editor. Other rows open
// the episode view.
export function liveRow(episode: LiveEpisode, jobId: string): SeasonRow {
	return {
		id: episode.id,
		number: episode.number,
		title: episode.title,
		state: episode.state,
		visibility: episode.visibility,
		meta: episode.visibility === 'public' ? 'Public' : 'Private',
		duration: null,
		jobId,
		fixture: false
	};
}

// Where one gallery row opens. A draft opens the editor. A fixture
// draft keeps the fixture query. A fixture render opens the processing
// screen. Every other row opens the episode.
export function seasonHref(row: SeasonRow): SeasonHref {
	if (row.state === 'draft') {
		if (row.fixture) return `/episode/${row.id}/edit?fixture=1`;
		return `/episode/${row.id}/edit`;
	}
	if (row.fixture && row.jobId && row.state === 'rendering') {
		return `/processing?episode=${row.id}`;
	}
	if (row.fixture) return `/episode/${row.id}?fixture=1`;
	return `/episode/${row.id}`;
}

export type SeasonHref =
	| `/episode/${string}/edit`
	| `/episode/${string}/edit?${string}`
	| `/processing?${string}`
	| `/episode/${string}?${string}`
	| `/episode/${string}`;

// Everything the gallery renders.
export interface GallerySnapshot {
	ready: boolean;
	notice: string;
	failed: boolean;
	live: boolean;
	rows: SeasonRow[];
	progress: Record<string, GalleryCardProgress>;
	gateResult: string;
}

export function emptyGallery(): GallerySnapshot {
	return {
		ready: false,
		notice: 'Loading the season.',
		failed: false,
		live: true,
		rows: [],
		progress: {},
		gateResult: ''
	};
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
		failed: false,
		live: false,
		rows: listSeason(state).map((episode) => fixtureRow(episode)),
		progress: {},
		gateResult: ''
	};
}

// The gallery behind the season screen. The fixture flag keeps the
// scripted season for offline runs. Otherwise the screen lists the
// owner episodes newest first and follows every unfinished row through
// the job its detail names, with the same follower the processing
// screen reads. Progress renders the high-water mark, so a replayed
// stream never drags the card backwards, across a reload included.
export class GalleryController {
	private snap: GallerySnapshot;
	private readonly onChange: (snap: GallerySnapshot) => void;
	private streams: Array<{ jobId: string; stream: JobStream }> = [];
	private cleanups: Array<() => void> = [];
	private restoreMock: (() => void) | null = null;
	private timer: number | null = null;
	private search = '';
	private statuses: Record<string, JobStatus> = {};

	constructor(onChange: (snap: GallerySnapshot) => void) {
		this.onChange = onChange;
		this.snap = emptyGallery();
	}

	mount(search: string): void {
		this.search = search;
		this.teardown();
		this.snap = emptyGallery();
		if (queryValue(search, 'fixture') === '1') {
			this.mountFixture(search);
		} else {
			this.snap = { ...this.snap, ready: false, notice: 'Loading the live season.' };
			this.emit();
			void this.mountLive();
		}
		this.watchGates(search);
	}

	retry(): void {
		this.mount(this.search);
	}

	destroy(): void {
		this.teardown();
	}

	// The progress one card shows, or the idle card before the first
	// stream reading lands.
	cardFor(jobId: string): GalleryCardProgress {
		const held = this.snap.progress[jobId];
		if (held) return held;
		return { jobId, percent: 0, detail: 'Waiting for the job.', running: true, status: 'running' };
	}

	private mountFixture(search: string): void {
		const snapshot = initialGallery(search);
		this.restoreMock = installMockJob(RENDER_JOB_ID);
		this.snap = {
			...this.snap,
			rows: snapshot.rows,
			ready: true,
			live: false,
			notice: snapshot.notice
		};
		for (const row of this.snap.rows) {
			if (row.jobId) {
				this.statuses[row.jobId] = 'running';
				this.followJob(row.jobId);
			}
		}
		this.emit();
		this.timer = window.setInterval(() => this.refresh(), 500);
		this.expose();
	}

	private async mountLive(): Promise<void> {
		let listed: LiveEpisode[];
		try {
			listed = await fetchSeason(window.fetch);
		} catch {
			this.snap = {
				...this.snap,
				ready: true,
				failed: true,
				notice: 'The season endpoint refused, so no rows render. Retry the load.'
			};
			this.emit();
			return;
		}
		if (listed.length === 0) {
			this.snap = {
				...this.snap,
				ready: true,
				notice: 'No episodes yet. Record the first one and it lands here.'
			};
			this.emit();
			return;
		}
		this.snap = {
			...this.snap,
			rows: listed.map((episode) => liveRow(episode, '')),
			ready: true,
			notice: 'Live season, newest first.'
		};
		this.emit();
		await this.followUnfinished(listed);
		this.timer = window.setInterval(() => this.refresh(), 500);
		this.expose();
	}

	// Detail answers per unfinished row carry the latest pass job, so
	// the card follows a real id. A refused detail leaves the row on
	// its state text instead of failing the whole season.
	private async followUnfinished(listed: LiveEpisode[]): Promise<void> {
		let changed = false;
		for (const episode of listed) {
			if (episode.state === 'ready') continue;
			const found = await this.jobOf(episode);
			if (!found) continue;
			this.statuses[found.jobId] = outcomeJobStatus(found.status);
			this.followJob(found.jobId);
			const row = this.snap.rows.find((candidate) => candidate.id === episode.id);
			if (row && row.jobId !== found.jobId) {
				row.jobId = found.jobId;
				changed = true;
			}
		}
		if (changed) this.emit();
	}

	// The latest pass job behind one unfinished episode, or null when
	// the detail refuses or names none.
	private async jobOf(episode: LiveEpisode): Promise<{ jobId: string; status: string } | null> {
		try {
			const detail = await fetchEpisodeDetail(window.fetch, episode.id);
			const jobId = detail.outcome?.jobId ?? '';
			if (!jobId) return null;
			return { jobId, status: detail.outcome?.status ?? '' };
		} catch {
			return null;
		}
	}

	private expose(): void {
		const target = window as unknown as Record<string, unknown>;
		target['__gallery'] = {
			progress: () => {
				const cards = Object.values(this.snap.progress);
				return cards.length > 0 ? (cards[0]?.percent ?? 0) : 0;
			}
		};
	}

	private watchGates(search: string): void {
		if (queryValue(search, 'gate') !== '1') return;
		const main = document.querySelector('main');
		if (main) {
			void runSeasonGates(main).then((result) => {
				this.snap = { ...this.snap, gateResult: result };
				this.emit();
			});
		}
	}

	private teardown(): void {
		if (this.timer !== null) {
			window.clearInterval(this.timer);
			this.timer = null;
		}
		for (const cleanup of this.cleanups) {
			try {
				cleanup();
			} catch {
				// A spent cleanup never blocks the rest.
			}
		}
		this.cleanups = [];
		for (const entry of this.streams) entry.stream.close();
		this.streams = [];
		this.restoreMock?.();
		this.restoreMock = null;
		this.statuses = {};
	}

	private emit(): void {
		this.onChange({ ...this.snap, rows: [...this.snap.rows], progress: { ...this.snap.progress } });
	}

	private followJob(jobId: string): void {
		const seed = readHighWater(browserStore(), jobId);
		const status = this.statuses[jobId] ?? 'running';
		this.snap = {
			...this.snap,
			progress: {
				...this.snap.progress,
				[jobId]: {
					jobId,
					percent: seed,
					detail: seed > 0 ? `Working · ${seed}% · kept across reload` : 'Working · starting.',
					running: !isTerminalStatus(status),
					status
				}
			}
		};
		const statuses = this.statuses;
		const stream = new JobStream({
			url: `/api/jobs/${encodeURIComponent(jobId)}/events`,
			fetchState: async (): Promise<JobSnapshot> => {
				try {
					const response = await fetch(`/api/jobs/${encodeURIComponent(jobId)}`);
					if (response.ok) return (await response.json()) as JobSnapshot;
				} catch {
					// The state endpoint stays unwired, so the seed stands.
				}
				return { jobId, status: statuses[jobId] ?? 'running' };
			}
		});
		this.streams.push({ jobId, stream });
		// Explicit cleanup because the stream may attach after an
		// await, outside the effect context the default runner needs.
		stream.attach((task) => {
			this.cleanups.push(task());
		});
	}

	private refresh(): void {
		let changed = false;
		const next: Record<string, GalleryCardProgress> = { ...this.snap.progress };
		for (const entry of this.streams) {
			const live = progressPercent(entry.stream.current ?? 0, entry.stream.total ?? 4);
			const held = next[entry.jobId]?.percent ?? 0;
			const percent = writeHighWater(browserStore(), entry.jobId, Math.max(live, held));
			// A stored terminal state stands, because a finished job never
			// runs again. A fresh stream reads queued before its first state.
			const known = this.statuses[entry.jobId];
			const status = known && isTerminalStatus(known) ? known : entry.stream.status;
			this.statuses[entry.jobId] = status;
			const running = !isTerminalStatus(status);
			const stage = entry.stream.stage ? `${entry.stream.stage} · ` : '';
			const detail =
				status === 'done'
					? 'Ready. Open the episode.'
					: entry.stream.connection === 'failed'
						? 'The job stream failed. The mark above is kept.'
						: `Working · ${stage}${percent}% · progress survives a reload`;
			const prior = next[entry.jobId];
			if (!prior || prior.percent !== percent || prior.detail !== detail || prior.status !== status) {
				next[entry.jobId] = { jobId: entry.jobId, percent, detail, running, status };
				changed = true;
			}
		}
		if (changed) {
			this.snap = { ...this.snap, progress: next };
			this.emit();
		}
	}
}

// Everything the episode view renders. Fixture rows carry generated
// audio, chapters, notes, and timed words. Live rows carry what the
// detail answers: metadata, proposals, the latest pass outcome, and
// quoted moments from the thread index. A render address plays that
// file. Without one, the view says playback waits.
export interface EpisodeScreen {
	ready: boolean;
	notice: string;
	failed: boolean;
	missing: boolean;
	live: boolean;
	id: string;
	number: number;
	title: string;
	state: string;
	visibility: string;
	audioUrl: string;
	duration: number | null;
	chapters: Chapter[];
	notes: string;
	words: Word[];
	proposals: LiveProposal[];
	outcome: LiveOutcome | null;
	moments: LiveThreadHit[];
	momentWord: number | null;
	momentQuote: string | null;
	coveringId: string | null;
	position: number;
	playing: boolean;
	published: boolean;
	eraseArmed: boolean;
	activeWord: number | null;
	activeChapter: number;
	gateResult: string;
}

export function emptyScreen(id: string): EpisodeScreen {
	return {
		ready: false,
		notice: 'Loading the episode.',
		failed: false,
		missing: false,
		live: true,
		id,
		number: 0,
		title: '',
		state: '',
		visibility: '',
		audioUrl: '',
		duration: null,
		chapters: [],
		notes: '',
		words: [],
		proposals: [],
		outcome: null,
		moments: [],
		momentWord: null,
		momentQuote: null,
		coveringId: null,
		position: 0,
		playing: false,
		published: false,
		eraseArmed: false,
		activeWord: null,
		activeChapter: 0,
		gateResult: ''
	};
}

// The episode behind its view. The fixture flag keeps the scripted
// episode with generated audio. Otherwise the view reads the wired
// detail: proposals with their word ranges and decisions, the latest
// pass outcome, and quoted moments from the thread index. A render
// address loads the player. A moment word parks on its covering
// proposal and names its quote.
export class EpisodeController {
	readonly episodeId: string;
	private snap: EpisodeScreen;
	private readonly onChange: (snap: EpisodeScreen) => void;
	private player: AudioPlayer | null = null;
	private ticker: number | null = null;
	private durationTimer: number | null = null;
	private search = '';

	constructor(episodeId: string, onChange: (snap: EpisodeScreen) => void) {
		this.episodeId = episodeId;
		this.onChange = onChange;
		this.snap = emptyScreen(episodeId);
	}

	mount(search: string): void {
		this.search = search;
		if (queryValue(search, 'fixture') === '1') {
			this.mountFixture(search);
		} else {
			void this.mountLive(search);
		}
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

	retry(): void {
		this.mount(this.search);
	}

	destroy(): void {
		if (this.ticker !== null) {
			window.clearInterval(this.ticker);
			this.ticker = null;
		}
		this.stopDurationWatch();
		this.player?.pause();
		this.player = null;
	}

	seekTo(seconds: number): void {
		if (!this.snap.audioUrl || this.snap.duration === null) return;
		const clamped = Math.max(0, Math.min(this.snap.duration, seconds));
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
		if (!this.player || !this.snap.audioUrl) {
			this.snap = { ...this.snap, notice: 'No audio stream on this episode yet.' };
			this.emit();
			return;
		}
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
		try {
			const response = await fetch(`/api/episodes/${encodeURIComponent(this.episodeId)}`, {
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

	private mountFixture(search: string): void {
		const found = episodeById(this.episodeId);
		if (!found) {
			this.snap = { ...this.snap, ready: true, missing: true, live: false, notice: 'No scripted episode carries that id.' };
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
			...emptyScreen(this.episodeId),
			ready: true,
			live: false,
			notice: 'Scripted episode. No backend needed.',
			id: episode.id,
			number: episode.number,
			title: episode.title,
			state: episode.state,
			visibility: 'private',
			audioUrl: url,
			duration: episode.duration,
			chapters: episode.chapters,
			notes: episode.notes,
			words,
			published: queryValue(search, 'published') === '1'
		};
		const at = Number(queryValue(search, 't') ?? '');
		if (Number.isFinite(at) && at > 0) {
			const clamped = Math.min(at, episode.duration);
			this.player?.seek(clamped);
			this.snap = {
				...this.snap,
				position: clamped,
				notice:
					queryValue(search, 'play') === '1'
						? `Playhead at the quoted moment, ${formatClock(clamped)}. Press play to hear it from there.`
						: `Playhead at the quoted moment, ${formatClock(clamped)}.`
			};
			this.follow();
		}
		this.emit();
		this.expose(Number.isFinite(at) && at > 0 ? at : null);
	}

	private async mountLive(search: string): Promise<void> {
		let detail: LiveDetail;
		try {
			detail = await fetchEpisodeDetail(window.fetch, this.episodeId);
		} catch (error) {
			const missing = error instanceof Error && error.message.startsWith('missing');
			this.snap = {
				...emptyScreen(this.episodeId),
				ready: true,
				missing,
				failed: !missing,
				notice: missing
					? 'No episode lives at this id.'
					: 'The episode endpoint refused, so nothing renders. Retry the load.'
			};
			this.emit();
			this.expose(null);
			return;
		}
		const word = Number(queryValue(search, 'w') ?? '');
		const momentWord = Number.isFinite(word) && word >= 0 ? Math.floor(word) : null;
		let moments: LiveThreadHit[] = [];
		try {
			const index = await fetchThreadsIndex(window.fetch);
			moments = [...index.names, ...index.topics]
				.flatMap((thread) => thread.episodes)
				.filter((hit) => hit.episodeId === this.episodeId);
		} catch {
			// Moments stay empty while the thread index refuses.
		}
		const momentQuote = momentWord === null ? null : (moments.find((hit) => hit.offset === momentWord)?.quote ?? null);
		const coveringId =
			momentWord === null
				? null
				: (detail.proposals.find((proposal) => proposal.startWord <= momentWord && momentWord <= proposal.endWord)?.id ?? null);
		const notice =
			momentWord === null
				? 'Live episode from the wired detail.'
				: momentQuote === null
					? `Quoted moment at word ${momentWord}. No stored quote names it.`
					: `Quoted moment at word ${momentWord}. “${momentQuote}”`;
		// The player loads the render, so the words and the scrubber sit on
		// the render clock. The last placed word gives a first length, and
		// the audio element replaces it once it reads the file.
		const renderUrl = detail.renderAudioUrl;
		const words = renderUrl ? renderClockWords(detail) : detail.words;
		const duration = renderUrl ? (words.at(-1)?.end ?? 0) : null;
		this.stopDurationWatch();
		this.player?.pause();
		this.player = null;
		if (renderUrl) {
			this.player = new AudioPlayer();
			this.player.load(renderUrl);
		}
		this.snap = {
			...emptyScreen(this.episodeId),
			ready: true,
			live: true,
			notice,
			id: detail.episode.id,
			number: detail.episode.number,
			title: detail.episode.title,
			state: detail.episode.state,
			visibility: detail.episode.visibility,
			audioUrl: renderUrl,
			duration,
			words: words.map((word) => ({
				start: word.start,
				end: word.end,
				text: word.text,
				speaker: ''
			})),
			proposals: detail.proposals,
			outcome: detail.outcome,
			moments,
			momentWord,
			momentQuote,
			coveringId,
			published: detail.episode.visibility === 'public'
		};
		this.emit();
		this.expose(momentWord);
		if (renderUrl) this.watchDuration();
	}

	// Poll the element until it knows the length of the loaded file. A
	// failed load stops the poll, and the placed words keep the length.
	private watchDuration(): void {
		this.stopDurationWatch();
		this.durationTimer = window.setInterval(() => {
			if (!this.player || this.player.error || this.syncDuration()) this.stopDurationWatch();
		}, 250);
	}

	private stopDurationWatch(): void {
		if (this.durationTimer === null) return;
		window.clearInterval(this.durationTimer);
		this.durationTimer = null;
	}

	// Copy the element length onto the screen once it is known. Returns
	// true when the length is known.
	private syncDuration(): boolean {
		const length = this.player?.duration ?? 0;
		if (!Number.isFinite(length) || length <= 0) return false;
		if (this.snap.duration !== length) {
			this.snap = { ...this.snap, duration: length };
			this.emit();
		}
		return true;
	}

	private expose(quote: number | null): void {
		const target = window as unknown as Record<string, unknown>;
		target['__episode'] = {
			position: () => this.snap.position,
			playing: () => this.snap.playing,
			quote: () => quote,
			failure: () => this.player?.error ?? null,
			source: () => this.player?.source ?? null
		};
	}

	private follow(): void {
		this.snap = {
			...this.snap,
			activeWord: activeWordAt(this.snap.words, [], this.snap.position),
			activeChapter: this.chapterAt(this.snap.position)
		};
	}

	private chapterAt(position: number): number {
		let current = 0;
		this.snap.chapters.forEach((chapter, index) => {
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

// Live wire shapes mirror the episode and thread handlers field for
// field. Parsers below reject anything else, so a renamed backend
// field fails here instead of rendering a half row.

// One listed episode as the list handler answers it.
export interface LiveEpisode {
	id: string;
	number: number;
	title: string;
	state: string;
	visibility: string;
}

// One stored proposal with its latest decision, as detail answers it.
export interface LiveProposal {
	id: string;
	kind: string;
	startWord: number;
	endWord: number;
	reason: string;
	decision: string;
}

// The latest transcript pass outcome, as detail answers it.
export interface LiveOutcome {
	jobId: string;
	status: string;
	error: string;
}

// One edit word as the detail answers it. Start and end are seconds.
export interface LiveWord {
	text: string;
	start: number;
	end: number;
}

// One episode with its proposals, edit words, and playable addresses.
// Render words come from the rendered file itself, and stay empty until
// analysis stores them.
export interface LiveDetail {
	episode: LiveEpisode;
	proposals: LiveProposal[];
	outcome: LiveOutcome | null;
	words: LiveWord[];
	audioUrl: string;
	renderAudioUrl: string;
	renderWords: LiveWord[];
}

// The silence the render lays between the cold open and the episode.
const COLD_OPEN_GAP_SECONDS = 0.75;

// The words that follow the rendered file, on its clock. Words analysis
// transcribed from the render win whenever they exist. Otherwise the edit
// words move the way the render moved them. An accepted cut removes its
// words and pulls every later word earlier. An applied cold open plays
// first, then a short gap, then the episode. The opening repeats words
// the list already holds, so no word lights while it plays. Each join also
// overlaps by a ten millisecond crossfade, which this leaves out.
export function renderClockWords(
	detail: Pick<LiveDetail, 'words' | 'proposals' | 'renderWords'>
): LiveWord[] {
	if (detail.renderWords.length > 0) return detail.renderWords.map((word) => ({ ...word }));
	const words = detail.words;
	// The render drops a range that runs backwards, past the words, or
	// over no time at all, so this drops it too.
	const spans = (proposal: LiveProposal): boolean => {
		const first = words[proposal.startWord];
		const last = words[proposal.endWord];
		return (
			proposal.startWord >= 0 &&
			proposal.endWord >= proposal.startWord &&
			first !== undefined &&
			last !== undefined &&
			last.end > first.start
		);
	};
	const cuts: Parameters<typeof toEditedTime>[1] = detail.proposals
		.filter((proposal) => proposal.kind === 'cut' && proposal.decision === 'accepted' && spans(proposal))
		.map((proposal) => ({
			id: proposal.id,
			range: { start: proposal.startWord, end: proposal.endWord },
			reason: proposal.reason
		}));
	const edited = (seconds: number): number => toEditedTime(words, cuts, seconds);
	const cold = detail.proposals.filter((proposal) => proposal.kind === 'cold_open').at(-1);
	let lead = 0;
	if (cold && cold.decision !== 'reverted' && spans(cold)) {
		const kept =
			edited(words[cold.endWord]?.end ?? 0) - edited(words[cold.startWord]?.start ?? 0);
		if (kept > 0) lead = kept + COLD_OPEN_GAP_SECONDS;
	}
	const placed: LiveWord[] = [];
	words.forEach((word, index) => {
		if (cuts.some((cut) => index >= cut.range.start && index <= cut.range.end)) return;
		placed.push({ text: word.text, start: edited(word.start) + lead, end: edited(word.end) + lead });
	});
	return placed;
}

// The editor link for a live draft, or null for every other episode.
// An empty audio address does not hide it. Playback can wait and the
// draft still opens in the editor.
export function draftEditHref(screen: {
	live: boolean;
	state: string;
	id: string;
	audioUrl: string;
}): `/episode/${string}/edit` | null {
	if (!screen.live || screen.state !== 'draft' || screen.id === '') return null;
	return `/episode/${screen.id}/edit`;
}

// One appearance of a thread in one episode. Offset counts rendered
// words, so the episode view parks on the covering proposal.
export interface LiveThreadHit {
	episodeId: string;
	number: number;
	quote: string;
	offset: number;
}

// One recurring name with every stored appearance behind it.
export interface LiveNameThread {
	key: string;
	display: string;
	kind: string;
	episodes: LiveThreadHit[];
	mentionCount: number;
	episodeCount: number;
}

// One circling phrase with every stored appearance behind it.
export interface LiveTopic {
	key: string;
	display: string;
	episodes: LiveThreadHit[];
	mentionCount: number;
	episodeCount: number;
}

// The thread index across the owner episodes.
export interface LiveThreads {
	names: LiveNameThread[];
	topics: LiveTopic[];
}

// The fetch seam behind the live loaders. Logic checks inject a stub
// and the page injects the browser fetch.
export type FetchFn = (url: string, init?: RequestInit) => Promise<Response>;

// True for a plain record, the only shape parsers accept.
function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === 'object' && value !== null;
}

// One required string field, or empty when the shape drifts.
function textField(body: Record<string, unknown>, name: string): string {
	const value = body[name];
	return typeof value === 'string' ? value : '';
}

// One required numeric field, or zero when the shape drifts.
function numberField(body: Record<string, unknown>, name: string): number {
	const value = body[name];
	return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

// One listed episode, or null when the row drifts.
function parseLiveEpisode(value: unknown): LiveEpisode | null {
	if (!isRecord(value)) return null;
	const id = textField(value, 'id');
	const title = textField(value, 'title');
	if (!id || !title) return null;
	return {
		id,
		number: numberField(value, 'number'),
		title,
		state: textField(value, 'state'),
		visibility: textField(value, 'visibility')
	};
}

// The season list newest first. The handler answers oldest first, so
// the parser reverses by episode number here.
export function parseSeasonList(raw: string): LiveEpisode[] {
	let decoded: unknown;
	try {
		decoded = JSON.parse(raw);
	} catch {
		throw new Error('season is not JSON');
	}
	if (!isRecord(decoded) || !Array.isArray(decoded['episodes'])) {
		throw new Error('season holds no episodes');
	}
	const rows: LiveEpisode[] = [];
	for (const value of decoded['episodes'] as unknown[]) {
		const episode = parseLiveEpisode(value);
		if (episode) rows.push(episode);
	}
	rows.sort((first, second) => second.number - first.number);
	return rows;
}

// One proposal row, or null when the row drifts.
function parseLiveProposal(value: unknown): LiveProposal | null {
	if (!isRecord(value)) return null;
	const id = textField(value, 'id');
	if (!id) return null;
	return {
		id,
		kind: textField(value, 'kind'),
		startWord: numberField(value, 'start_word'),
		endWord: numberField(value, 'end_word'),
		reason: textField(value, 'reason'),
		decision: textField(value, 'decision')
	};
}

// The pass outcome, or null when no pass ever started.
function parseLiveOutcome(value: unknown): LiveOutcome | null {
	if (value === null || value === undefined) return null;
	if (!isRecord(value)) return null;
	const jobId = textField(value, 'job_id');
	if (!jobId) return null;
	return { jobId, status: textField(value, 'status'), error: textField(value, 'error') };
}

// One edit word, or null when the row is not an object.
function parseLiveWord(value: unknown): LiveWord | null {
	if (!isRecord(value)) return null;
	return {
		text: textField(value, 'text'),
		start: numberField(value, 'start'),
		end: numberField(value, 'end')
	};
}

// One episode detail with its proposals, words, and playable addresses.
export function parseEpisodeDetail(raw: string): LiveDetail {
	let decoded: unknown;
	try {
		decoded = JSON.parse(raw);
	} catch {
		throw new Error('detail is not JSON');
	}
	if (!isRecord(decoded)) throw new Error('detail holds no episode');
	const episode = parseLiveEpisode(decoded['episode']);
	if (!episode) throw new Error('detail holds no episode');
	const rawProposals = Array.isArray(decoded['proposals']) ? (decoded['proposals'] as unknown[]) : [];
	const proposals: LiveProposal[] = [];
	for (const value of rawProposals) {
		const proposal = parseLiveProposal(value);
		if (proposal) proposals.push(proposal);
	}
	const rawWords = Array.isArray(decoded['words']) ? (decoded['words'] as unknown[]) : [];
	const words: LiveWord[] = [];
	for (const value of rawWords) {
		const word = parseLiveWord(value);
		if (word) words.push(word);
	}
	const rawRendered = Array.isArray(decoded['render_words']) ? (decoded['render_words'] as unknown[]) : [];
	const renderWords: LiveWord[] = [];
	for (const value of rawRendered) {
		const word = parseLiveWord(value);
		if (word) renderWords.push(word);
	}
	return {
		episode,
		proposals,
		outcome: parseLiveOutcome(decoded['transcript_outcome']),
		words,
		audioUrl: textField(decoded, 'audio_url'),
		renderAudioUrl: textField(decoded, 'render_audio_url'),
		renderWords
	};
}

// One thread appearance, or null when the hit drifts.
function parseLiveHit(value: unknown): LiveThreadHit | null {
	if (!isRecord(value)) return null;
	const episodeId = textField(value, 'episode_id');
	const quote = textField(value, 'quote');
	if (!episodeId || !quote) return null;
	return { episodeId, number: numberField(value, 'number'), quote, offset: numberField(value, 'offset') };
}

// One name thread with every stored appearance behind it.
function parseNameThread(value: unknown): LiveNameThread | null {
	if (!isRecord(value)) return null;
	const key = textField(value, 'key');
	if (!key) return null;
	const rawHits = Array.isArray(value['episodes']) ? (value['episodes'] as unknown[]) : [];
	const episodes: LiveThreadHit[] = [];
	for (const hit of rawHits) {
		const parsed = parseLiveHit(hit);
		if (parsed) episodes.push(parsed);
	}
	return {
		key,
		display: textField(value, 'display') || key,
		kind: textField(value, 'kind'),
		episodes,
		mentionCount: numberField(value, 'mention_count'),
		episodeCount: numberField(value, 'episode_count')
	};
}

// One circled topic with every stored appearance behind it.
function parseLiveTopic(value: unknown): LiveTopic | null {
	if (!isRecord(value)) return null;
	const key = textField(value, 'key');
	if (!key) return null;
	const rawHits = Array.isArray(value['episodes']) ? (value['episodes'] as unknown[]) : [];
	const episodes: LiveThreadHit[] = [];
	for (const hit of rawHits) {
		const parsed = parseLiveHit(hit);
		if (parsed) episodes.push(parsed);
	}
	return {
		key,
		display: textField(value, 'display') || key,
		episodes,
		mentionCount: numberField(value, 'mention_count'),
		episodeCount: numberField(value, 'episode_count')
	};
}

// The thread index with recurring names and circled topics.
export function parseThreadsIndex(raw: string): LiveThreads {
	let decoded: unknown;
	try {
		decoded = JSON.parse(raw);
	} catch {
		throw new Error('threads are not JSON');
	}
	if (!isRecord(decoded)) throw new Error('threads hold no index');
	const rawNames = Array.isArray(decoded['name_threads']) ? (decoded['name_threads'] as unknown[]) : [];
	const rawTopics = Array.isArray(decoded['circled_topics']) ? (decoded['circled_topics'] as unknown[]) : [];
	const names: LiveNameThread[] = [];
	for (const value of rawNames) {
		const thread = parseNameThread(value);
		if (thread) names.push(thread);
	}
	const topics: LiveTopic[] = [];
	for (const value of rawTopics) {
		const topic = parseLiveTopic(value);
		if (topic) topics.push(topic);
	}
	return { names, topics };
}

// An empty thread index, the shape the thread panel renders before
// the first load lands.
export function emptyLiveThreads(): LiveThreads {
	return { names: [], topics: [] };
}
// Read one JSON body. A missing episode throws a missing error the
// view renders as its empty state. Any other refusal throws its
// status, and the view renders its retry.
async function readJSON(fetchFn: FetchFn, url: string): Promise<string> {
	const response = await fetchFn(url);
	if (response.status === 404) throw new Error(`missing ${url}`);
	if (!response.ok) throw new Error(`request ${response.status} for ${url}`);
	return response.text();
}

// List the owner episodes newest first.
export async function fetchSeason(fetchFn: FetchFn): Promise<LiveEpisode[]> {
	return parseSeasonList(await readJSON(fetchFn, '/api/episodes'));
}

// Read one episode detail with its proposals and pass outcome.
export async function fetchEpisodeDetail(fetchFn: FetchFn, id: string): Promise<LiveDetail> {
	return parseEpisodeDetail(await readJSON(fetchFn, `/api/episodes/${encodeURIComponent(id)}`));
}

// Read the thread index across the owner episodes.
export async function fetchThreadsIndex(fetchFn: FetchFn): Promise<LiveThreads> {
	return parseThreadsIndex(await readJSON(fetchFn, '/api/threads'));
}

// The deep link a live thread quote opens: the episode with the word
// offset parked as its quoted moment.
export function liveQuoteHref(episodeId: string, offset: number): `/episode/${string}?${string}` {
	return `/episode/${episodeId}?w=${offset}`;
}

// Map a stored job status onto the follower states. Unknown values
// read as running, because a named job the stream never closed is
// still work the card follows.
export function outcomeJobStatus(status: string): JobStatus {
	if (
		status === 'queued' ||
		status === 'running' ||
		status === 'done' ||
		status === 'error' ||
		status === 'cancelled' ||
		status === 'interrupted'
	) {
		return status;
	}
	return 'running';
}
