// First visit screen behind the welcome route. An empty catalog offers
// one button to record. A seeded catalog plays thirty seconds of the
// latest episode first, then offers one button to record the next one.
// Fixtures carry no backend, so the query string picks the catalog state
// and generated audio stands in for the episode. Playback runs through
// the shared player, so the first gesture unlocks it for the session.
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

let teaserUrl: string | undefined;

// A playable URL for the thirty second teaser. Built once from the
// generated tone at the teaser length, or empty where the runtime
// holds no blob URLs. Empty keeps logic checks DOM-free.
export function teaserAudioUrl(): string {
	if (teaserUrl !== undefined) return teaserUrl;
	try {
		if (typeof Blob === 'undefined' || typeof URL.createObjectURL !== 'function') {
			teaserUrl = '';
			return teaserUrl;
		}
		const bytes = encodeWavBytes(buildTone(TEASER_SECONDS, TEASER.episodeNumber), TEASER_RATE);
		teaserUrl = URL.createObjectURL(new Blob([bytes.buffer as ArrayBuffer], { type: 'audio/wav' }));
		return teaserUrl;
	} catch {
		teaserUrl = '';
		return teaserUrl;
	}
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

// Everything the welcome screen renders.
export interface WelcomeSnapshot {
	ready: boolean;
	mode: WelcomeMode;
	notice: string;
	playing: boolean;
	position: number;
	gateResult: string;
}

export function emptyWelcome(): WelcomeSnapshot {
	return { ready: false, mode: 'empty', notice: 'Loading.', playing: false, position: 0, gateResult: '' };
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
		gateResult: ''
	};
}

// The first visit behind its view. Seeded state loads the teaser into
// the shared player, so the visitor's first gesture unlocks playback
// for the session. The record link stays a plain link, so the empty
// catalog reaches a live session through two plain clicks.
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
			this.player?.pause();
			this.player = new AudioPlayer();
			const url = teaserAudioUrl();
			if (url) this.player.load(url);
		}
		this.emit();
		const target = window as unknown as Record<string, unknown>;
		target['__welcome'] = {
			mode: () => this.snap.mode,
			playing: () => this.snap.playing,
			position: () => this.snap.position,
			source: () => this.player?.source ?? null,
			failure: () => this.player?.error ?? null
		};
		if (queryValue(search, 'gate') === '1') {
			const main = document.querySelector('main');
			if (main) {
				void runWelcomeGates(main).then((result) => {
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

	async togglePlay(): Promise<void> {
		if (!this.player) return;
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
			notice: ok ? `Playing thirty seconds of episode ${TEASER.episodeNumber}.` : 'Playback refused. Press play again.'
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
			const position = Math.min(at, TEASER_SECONDS);
			const finished = position >= TEASER_SECONDS || this.player?.playing === false;
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
