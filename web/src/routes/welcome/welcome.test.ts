// Pins for the welcome screen. The catalog state comes from the query
// string, the teaser runs thirty seconds, and the empty catalog offers
// one button with no player behind it.
import { describe, expect, it } from 'vitest';
import {
	TEASER,
	TEASER_RATE,
	TEASER_SECONDS,
	buildTone,
	emptyWelcome,
	encodeWavBytes,
	formatClock,
	initialWelcome,
	queryValue,
	teaserAudioUrl,
	welcomeMode,
	WelcomeController
} from './welcome';

describe('catalog state', () => {
	it('renders empty with no query string', () => {
		expect(welcomeMode('')).toBe('empty');
		expect(queryValue('', 'seed')).toBeNull();
	});

	it('renders seeded with the seed flag', () => {
		expect(welcomeMode('?seed=1')).toBe('seeded');
		expect(welcomeMode('?fixture=1')).toBe('seeded');
	});

	it('renders empty when the flag reads anything else', () => {
		expect(welcomeMode('?seed=0')).toBe('empty');
		expect(welcomeMode('?other=1')).toBe('empty');
	});

	it('carries the record notice in both states', () => {
		expect(initialWelcome('').notice).toMatch('first episode');
		expect(initialWelcome('?seed=1').notice).toMatch('already here');
		expect(initialWelcome('').ready).toBe(true);
		expect(initialWelcome('?seed=1').mode).toBe('seeded');
		expect(emptyWelcome().ready).toBe(false);
	});
});

describe('teaser clock', () => {
	it('formats the thirty second readout', () => {
		expect(formatClock(0)).toBe('0:00');
		expect(formatClock(TEASER_SECONDS)).toBe('0:30');
		expect(formatClock(65)).toBe('1:05');
		expect(formatClock(-4)).toBe('0:00');
	});

	it('names the latest seeded episode', () => {
		expect(TEASER.episodeNumber).toBe(4);
		expect(TEASER.title.length).toBeGreaterThan(0);
		expect(TEASER.lineA.length).toBeGreaterThan(0);
		expect(TEASER.lineB.length).toBeGreaterThan(0);
	});
});

describe('teaser audio', () => {
	it('builds thirty seconds of tone at the teaser rate', () => {
		expect(buildTone(TEASER_SECONDS, TEASER.episodeNumber)).toHaveLength(TEASER_SECONDS * TEASER_RATE);
	});

	it('wraps the tone in a WAV header logic checks read', () => {
		const bytes = encodeWavBytes(buildTone(1, 4), TEASER_RATE);
		expect(bytes).toHaveLength(44 + TEASER_RATE * 2);
		expect(String.fromCharCode(bytes[0] ?? 0, bytes[1] ?? 0, bytes[2] ?? 0, bytes[3] ?? 0)).toBe(
			'RIFF'
		);
	});

	it('stays DOM-free where no blob URLs exist', () => {
		expect(teaserAudioUrl()).toBe('');
	});
});

describe('empty controller', () => {
	it('mounts with no player and toggles to nothing', async () => {
		let current = emptyWelcome();
		const controller = new WelcomeController((fresh) => {
			current = fresh;
		});
		controller.mount('');
		expect(current.ready).toBe(true);
		expect(current.mode).toBe('empty');
		await controller.togglePlay();
		expect(current.playing).toBe(false);
		controller.destroy();
	});
});
