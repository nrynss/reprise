// Pins for the welcome screen. The catalog state comes from the query
// string, the teaser runs thirty seconds, and the empty catalog offers
// one button with no player behind it. The live read replaces the
// fixtures where a backend answers and leaves them where none does.
import { describe, expect, it, vi } from 'vitest';
import {
	TEASER,
	TEASER_RATE,
	TEASER_SECONDS,
	buildTone,
	emptyWelcome,
	encodeWavBytes,
	fetchWelcome,
	formatClock,
	initialWelcome,
	liveNotice,
	queryValue,
	recordLabel,
	teaserAudioUrl,
	teaserEyebrow,
	teaserToneUrl,
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

describe('live labels', () => {
	it('names the record episode after the teaser', () => {
		expect(recordLabel('empty', 4)).toBe('Record your first episode');
		expect(recordLabel('seeded', 4)).toBe('Record episode 5');
		expect(recordLabel('seeded', 1)).toBe('Record episode 2');
	});

	it('pads the teaser eyebrow', () => {
		expect(teaserEyebrow(4)).toBe('From EP.04');
		expect(teaserEyebrow(12)).toBe('From EP.12');
	});

	it('counts the season notice', () => {
		expect(liveNotice(0)).toMatch('One episode');
		expect(liveNotice(1)).toMatch('One episode');
		expect(liveNotice(4)).toMatch('4 episodes');
	});

	it('builds tone per episode number', () => {
		expect(teaserToneUrl(TEASER.episodeNumber)).toBe(teaserAudioUrl());
	});
});

describe('live read', () => {
	it('reads the seeded season from the backend', async () => {
		const live = await fetchWelcome(async () =>
			Response.json({
				mode: 'seeded',
				episodes: 1,
				teaser: {
					episode_number: 1,
					title: 'The dreaded conversation',
					line_a: 'Maya',
					line_b: 'Jonas',
					audio_url: '/media/blob-1'
				}
			})
		);
		expect(live?.mode).toBe('seeded');
		expect(live?.episodes).toBe(1);
		expect(live?.teaser?.title).toBe('The dreaded conversation');
		expect(live?.teaser?.audioUrl).toBe('/media/blob-1');
	});

	it('reads the empty season without a teaser', async () => {
		const live = await fetchWelcome(async () => Response.json({ mode: 'empty', episodes: 0 }));
		expect(live?.mode).toBe('empty');
		expect(live?.teaser).toBeNull();
	});

	it('keeps the fixtures where the backend never answers', async () => {
		expect(await fetchWelcome(async () => new Response('nope', { status: 404 }))).toBeNull();
		expect(
			await fetchWelcome(async () => Response.json({ mode: 'seeded', episodes: 'many' }))
		).toBeNull();
		expect(
			await fetchWelcome(async () => {
				throw new Error('no backend');
			})
		).toBeNull();
	});
});

describe('live controller', () => {
	it('replaces the fixtures with the live season', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async () =>
				Response.json({
					mode: 'seeded',
					episodes: 1,
					teaser: {
						episode_number: 1,
						title: 'The dreaded conversation',
						line_a: 'Maya',
						line_b: 'Jonas',
						audio_url: '/media/blob-1'
					}
				})
			)
		);
		try {
			let current = emptyWelcome();
			const controller = new WelcomeController((fresh) => {
				current = fresh;
			});
			controller.mount('');
			expect(current.mode).toBe('empty');
			await controller.loadLive();
			expect(current.mode).toBe('seeded');
			expect(current.teaser.episodeNumber).toBe(1);
			expect(current.teaser.title).toBe('The dreaded conversation');
			expect(current.notice).toMatch('One episode');
			expect(current.audioUrl).toBe('/media/blob-1');
			expect(current.capped).toBe(false);
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it('keeps the fixtures where the live read fails', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async () => {
				throw new Error('no backend');
			})
		);
		try {
			let current = emptyWelcome();
			const controller = new WelcomeController((fresh) => {
				current = fresh;
			});
			controller.mount('?seed=1');
			await controller.loadLive();
			expect(current.mode).toBe('seeded');
			expect(current.teaser).toEqual(TEASER);
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});
});
