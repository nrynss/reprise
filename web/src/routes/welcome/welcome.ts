// First visit screen behind the welcome route. An empty catalog offers
// one button to record. A seeded catalog plays the latest episode first,
// then offers one button to record the next one. The screen reads the
// real catalog from the first visit route and falls back to the query
// string where no backend answers, so scripted proofs run with no
// server. Generated audio stands in for the episode where the teaser
// stores no render. Playback runs through the shared player, so the
// first gesture unlocks it for the session.
import { AudioPlayer } from '@nrynss/chaaya/audio';

// Length of the seeded teaser, in seconds.
export const TEASER_SECONDS = 30;

// Catalog states the screen renders. Empty means no copies exist yet.
export type WelcomeMode = 'empty' | 'seeded';

// The latest seeded episode behind the teaser.
export interface WelcomeTeaser {
	episodeNumber: number;
	title: string;
	lineA: string;
	lineB: string;
}

export const TEASER: WelcomeTeaser = {
	episodeNumber: 4,
	title: 'Three weeks of almost',
	lineA: 'You have brought up June three times now. Three weeks. What is the shape of the thing you are not saying?',
	lineB: 'That if I call, the garden becomes hers. And if I do not, it stays mine. Which is worse, probably.'
};

// Read one query value by iterating the entries in order. The first
// match answers, and a missing name reads null.
export function queryValue(search: string, name: string): string | null {
	for (const [key, value] of new URLSearchParams(search)) {
		if (key === name) return value;
	}
	return null;
}

// Pick the catalog state from the query string. A seed flag means copies
// exist. Anything else renders the empty catalog a new guest meets.
export function welcomeMode(search: string): WelcomeMode {
	if (queryValue(search, 'seed') === '1' || queryValue(search, 'fixture') === '1') return 'seeded';
	return 'empty';
}

// Seconds as m:ss for the teaser readout.
export function formatClock(seconds: number): string {
	const clamped = Math.max(0, Math.round(seconds));
	return `${Math.floor(clamped / 60)}:${String(clamped % 60).padStart(2, '0')}`;
}

export const TEASER_RATE = 8000;

// One channel of alternating tones with pauses, so the teaser has shape
// and the readout has somewhere to land. Deterministic by seed.
export function buildTone(duration: number, seed: number): Float32Array {
	const frames = Math.max(1, Math.floor(duration * TEASER_RATE));
	const out = new Float32Array(frames);
	let state = 0x2f6e2b1 + Math.floor(seed * 7919);
	const next = (): number => {
		state = (state * 1103515245 + 12345) & 0x7fffffff;
		return state / 0x7fffffff - 0.5;
	};
	for (let frame = 0; frame < frames; frame += 1) {
		const second = frame / TEASER_RATE;
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

const toneUrls: Record<number, string> = {};

// A playable URL for generated tone behind one episode number. Built
// once per number at the teaser length, or empty where the runtime
// holds no blob URLs. Empty keeps logic checks DOM-free.
export function teaserToneUrl(episodeNumber: number): string {
	const cached = toneUrls[episodeNumber];
	if (cached !== undefined) return cached;
	try {
		if (typeof Blob === 'undefined' || typeof URL.createObjectURL !== 'function') {
			return '';
		}
		const bytes = encodeWavBytes(buildTone(TEASER_SECONDS, episodeNumber), TEASER_RATE);
		const url = URL.createObjectURL(new Blob([bytes.buffer as ArrayBuffer], { type: 'audio/wav' }));
		toneUrls[episodeNumber] = url;
		return url;
	} catch {
		return '';
	}
}

// A playable URL for the thirty second teaser. Built once from the
// generated tone at the teaser length, or empty where the runtime
// holds no blob URLs. Empty keeps logic checks DOM-free.
export function teaserAudioUrl(): string {
	return teaserToneUrl(TEASER.episodeNumber);
}

// The welcome palette as data. The screen draws these values and the
// contrast gate measures this same text, so the two cannot drift.
export const WELCOME_THEME_CSS = [
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

// Every foreground and background pair the welcome screen draws as text.
export const WELCOME_CONTRAST_PAIRS: ReadonlyArray<readonly [string, string]> = [
	['ink', 'paper'],
	['muted', 'paper'],
	['ink', 'raised'],
	['muted', 'raised'],
	['accent', 'paper'],
	['on-accent', 'accent']
];

// Run both library gates against a rendered welcome screen. Returns the
// one line the screen shows.
export async function runWelcomeGates(root: HTMLElement): Promise<string> {
	try {
		const { a11yGate, contrastGate } = await import('@nrynss/chaaya/testing');
		await a11yGate(root);
		contrastGate(WELCOME_THEME_CSS, WELCOME_CONTRAST_PAIRS);
		return 'Gates passed: accessibility and contrast.';
	} catch (error) {
		return `Gate failed: ${error instanceof Error ? error.message : 'unknown'}`;
	}
}

// The fetch seam behind the live read. Logic checks inject a stub and
// the controller passes the browser fetch.
export type FetchFn = (url: string) => Promise<Response>;

// The teaser episode behind a live read.
export interface WelcomeLiveTeaser {
	episodeNumber: number;
	title: string;
	lineA: string;
	lineB: string;
	audioUrl: string;
}

// The season behind a live first visit read.
export interface WelcomeLive {
	mode: WelcomeMode;
	episodes: number;
	teaser: WelcomeLiveTeaser | null;
}

// Read the real catalog state from the first visit route. Answers null
// where no backend answers or the body carries no usable season, so the
// screen falls back to the query string fixtures.
export async function fetchWelcome(fetchFn: FetchFn = fetch): Promise<WelcomeLive | null> {
	try {
		const response = await fetchFn('/api/welcome');
		if (!response.ok) return null;
		const body = (await response.json()) as {
			mode?: unknown;
			episodes?: unknown;
			teaser?: {
				episode_number?: unknown;
				title?: unknown;
				line_a?: unknown;
				line_b?: unknown;
				audio_url?: unknown;
			} | null;
		};
		if (body.mode !== 'empty' && body.mode !== 'seeded') return null;
		if (typeof body.episodes !== 'number') return null;
		if (body.mode === 'empty' || body.teaser == null) {
			return { mode: body.mode, episodes: 0, teaser: null };
		}
		const teaser = body.teaser;
		if (typeof teaser.episode_number !== 'number' || typeof teaser.title !== 'string') return null;
		if (typeof teaser.line_a !== 'string' || typeof teaser.line_b !== 'string') return null;
		if (typeof teaser.audio_url !== 'string') return null;
		return {
			mode: 'seeded',
			episodes: body.episodes,
			teaser: {
				episodeNumber: teaser.episode_number,
				title: teaser.title,
				lineA: teaser.line_a,
				lineB: teaser.line_b,
				audioUrl: teaser.audio_url
			}
		};
	} catch {
		return null;
	}
}

// Everything the welcome screen renders.
export interface WelcomeSnapshot {
	ready: boolean;
	mode: WelcomeMode;
	notice: string;
	playing: boolean;
	position: number;
	gateResult: string;
	teaser: WelcomeTeaser;
	audioUrl: string;
	capped: boolean;
	episodes: number;
}

export function emptyWelcome(): WelcomeSnapshot {
	return {
		ready: false,
		mode: 'empty',
		notice: 'Loading.',
		playing: false,
		position: 0,
		gateResult: '',
		teaser: TEASER,
		audioUrl: '',
		capped: true,
		episodes: 0
	};
}

// The season notice behind a live read. Empty offers the record button
// alone. Seeded names the count behind the teaser.
export function liveNotice(episodes: number): string {
	if (episodes <= 1) return 'One episode is already here. The host has been listening.';
	return `${episodes} episodes are already here. The host has been listening.`;
}

// The record link label behind one teaser. Empty offers the first
// episode. Seeded offers the episode after the teaser.
export function recordLabel(mode: WelcomeMode, episodeNumber: number): string {
	if (mode !== 'seeded') return 'Record your first episode';
	return `Record episode ${episodeNumber + 1}`;
}

// The teaser eyebrow behind one episode number, zero padded.
export function teaserEyebrow(episodeNumber: number): string {
	return `From EP.${String(episodeNumber).padStart(2, '0')}`;
}

// The snapshot before any player opens. Pure, so the server render
// carries the same record link the crawler follows.
export function initialWelcome(search = ''): WelcomeSnapshot {
	const mode = welcomeMode(search);
	return {
		ready: true,
		mode,
		notice:
			mode === 'seeded'
				? 'Four episodes are already here. The host has been listening.'
				: 'Nothing recorded yet. Your first episode starts here.',
		playing: false,
		position: 0,
		gateResult: '',
		teaser: TEASER,
		audioUrl: '',
		capped: true,
		episodes: mode === 'seeded' ? 4 : 0
	};
}

// The first visit behind its view. The fixture snapshot renders first,
// so the server markup and the scripted proofs agree. The live read then
// replaces it where a backend answers. The record link stays a plain
// link, so the empty catalog reaches a live session through two plain
// clicks.
export class WelcomeController {
	private snap: WelcomeSnapshot;
	private readonly onChange: (snap: WelcomeSnapshot) => void;
	private player: AudioPlayer | null = null;
	private ticker: number | null = null;

	constructor(onChange: (snap: WelcomeSnapshot) => void) {
		this.onChange = onChange;
		this.snap = emptyWelcome();
	}

	mount(search: string): void {
		const initial = initialWelcome(search);
		this.snap = { ...this.snap, ready: true, mode: initial.mode, notice: initial.notice };
		if (initial.mode === 'seeded') {
			this.loadSource(teaserToneUrl(initial.teaser.episodeNumber), true);
		}
		this.emit();
		this.expose();
		if (queryValue(search, 'gate') === '1') {
			const main = document.querySelector('main');
			if (main) {
				void runWelcomeGates(main).then((result) => {
					this.snap = { ...this.snap, gateResult: result };
					this.emit();
				});
			}
		}
		void this.loadLive();
	}

	// Replace the fixture snapshot with the live season. A backend that
	// never answers leaves the fixtures in place.
	async loadLive(): Promise<void> {
		let live: WelcomeLive | null;
		try {
			live = await fetchWelcome(fetch);
		} catch {
			return;
		}
		if (!live) return;
		const teaser: WelcomeTeaser = live.teaser
			? {
					episodeNumber: live.teaser.episodeNumber,
					title: live.teaser.title,
					lineA: live.teaser.lineA,
					lineB: live.teaser.lineB
				}
			: TEASER;
		const audioUrl = live.teaser?.audioUrl ? live.teaser.audioUrl : teaserToneUrl(teaser.episodeNumber);
		this.snap = {
			...this.snap,
			mode: live.mode,
			notice:
				live.mode === 'seeded'
					? liveNotice(live.episodes)
					: 'Nothing recorded yet. Your first episode starts here.',
			playing: false,
			position: 0,
			teaser,
			audioUrl,
			capped: !live.teaser?.audioUrl,
			episodes: live.episodes
		};
		if (live.mode === 'seeded') {
			this.loadSource(audioUrl, !live.teaser?.audioUrl);
		} else {
			this.player?.pause();
			this.player = null;
		}
		this.emit();
		this.expose();
	}

	// Point the shared player at one teaser address. A capped source is
	// generated tone that stops at the teaser length. A live render plays
	// to its end.
	private loadSource(url: string, capped: boolean): void {
		this.player?.pause();
		this.player = new AudioPlayer();
		this.snap = { ...this.snap, audioUrl: url, capped };
		if (url) this.player.load(url);
	}

	private expose(): void {
		const target = window as unknown as Record<string, unknown>;
		target['__welcome'] = {
			mode: () => this.snap.mode,
			playing: () => this.snap.playing,
			position: () => this.snap.position,
			source: () => this.player?.source ?? null,
			failure: () => this.player?.error ?? null
		};
	}

	destroy(): void {
		if (this.ticker !== null) {
			window.clearInterval(this.ticker);
			this.ticker = null;
		}
		this.player?.pause();
		this.player = null;
	}

	async togglePlay(): Promise<void> {
		if (!this.player) return;
		if (this.player.playing) {
			this.player.pause();
			this.snap = { ...this.snap, playing: false, position: this.player.currentTime || this.snap.position };
			this.emit();
			return;
		}
		const ok = await this.player.play();
		const playingNotice = this.snap.capped
			? `Playing thirty seconds of episode ${this.snap.teaser.episodeNumber}.`
			: `Playing episode ${this.snap.teaser.episodeNumber}.`;
		this.snap = {
			...this.snap,
			playing: ok,
			position: this.player.currentTime || this.snap.position,
			notice: ok ? playingNotice : 'Playback refused. Press play again.'
		};
		this.emit();
		if (ok) this.startTicker();
	}

	private emit(): void {
		this.onChange({ ...this.snap });
	}

	private startTicker(): void {
		if (this.ticker !== null) return;
		this.ticker = window.setInterval(() => {
			const at = this.player?.currentTime ?? this.snap.position;
			const position = this.snap.capped ? Math.min(at, TEASER_SECONDS) : at;
			const finished =
				this.player?.playing === false || (this.snap.capped && position >= TEASER_SECONDS);
			if (finished) {
				if (this.ticker !== null) {
					window.clearInterval(this.ticker);
					this.ticker = null;
				}
				this.snap = { ...this.snap, playing: false, position };
				this.emit();
				return;
			}
			if (position !== this.snap.position) {
				this.snap = { ...this.snap, position };
				this.emit();
			}
		}, 250);
	}
}
