// The episode page plays the render, so its words, its seek targets and
// its length must sit on the clock of the rendered file, never on the
// raw take the edit words came from.
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { AudioPlayer } from '@nrynss/chaaya/audio';
import { EpisodeController, emptyScreen, renderClockWords, type LiveProposal } from './threads';

type Screen = ReturnType<typeof emptyScreen>;

const takeWords = [
	{ text: 'One', start: 0, end: 1 },
	{ text: 'um', start: 1, end: 2 },
	{ text: 'Two', start: 2, end: 3 }
];

function proposal(partial: Partial<LiveProposal>): LiveProposal {
	return { id: 'p', kind: 'cut', startWord: 0, endWord: 0, reason: '', decision: 'accepted', ...partial };
}

function serveDetail(body: Record<string, unknown>): void {
	vi.stubGlobal(
		'fetch',
		vi.fn(async (input: RequestInfo | URL) => {
			const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
			if (url.includes('/api/threads')) return Response.json({ name_threads: [], circled_topics: [] });
			return Response.json({
				episode: { id: 'ep-9', number: 3, title: 'Heard', state: 'ready', visibility: 'private' },
				proposals: [],
				words: [],
				audio_url: '/media/stem',
				render_audio_url: '/media/opus-9',
				...body
			});
		})
	);
}

async function mounted(snaps: Screen[]): Promise<EpisodeController> {
	const controller = new EpisodeController('ep-9', (snap) => snaps.push(snap));
	controller.mount('');
	await vi.waitFor(() => expect(snaps.at(-1)?.ready).toBe(true));
	return controller;
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('render clock', () => {
	it('places words after an accepted cut on the rendered file clock', async () => {
		serveDetail({
			proposals: [
				{ id: 'cut-1', kind: 'cut', start_word: 1, end_word: 1, reason: 'filler', decision: 'accepted' }
			],
			words: takeWords
		});
		const snaps: Screen[] = [];
		const controller = await mounted(snaps);
		const snap = snaps.at(-1);
		// The render removed one second, so it runs two seconds and "Two"
		// starts at one second in the file the player loaded.
		expect(snap?.duration).toBe(2);
		expect(snap?.words.map((word) => [word.text, word.start])).toEqual([
			['One', 0],
			['Two', 1]
		]);
		controller.seekWord(1);
		expect(snaps.at(-1)?.position).toBe(1);
		controller.destroy();
	});

	it('follows the stored rendered words when analysis stored them', async () => {
		serveDetail({
			proposals: [
				{ id: 'cut-1', kind: 'cut', start_word: 1, end_word: 1, reason: 'filler', decision: 'accepted' }
			],
			words: takeWords,
			render_words: [
				{ text: 'One', start: 0, end: 0.98 },
				{ text: 'Two', start: 0.99, end: 1.97 }
			]
		});
		const snaps: Screen[] = [];
		const controller = await mounted(snaps);
		const snap = snaps.at(-1);
		expect(snap?.words.map((word) => word.start)).toEqual([0, 0.99]);
		expect(snap?.duration).toBe(1.97);
		controller.destroy();
	});

	it('plays a render with no stored words and takes its length from the element', async () => {
		serveDetail({ words: [] });
		const snaps: Screen[] = [];
		const controller = await mounted(snaps);
		const snap = snaps.at(-1);
		expect(snap?.audioUrl).toBe('/media/opus-9');
		expect(snap?.duration).not.toBeNull();
		expect(snap?.words).toEqual([]);
		const player = (controller as unknown as { player: AudioPlayer }).player;
		player.duration = 42.5;
		await vi.waitFor(() => expect(snaps.at(-1)?.duration).toBe(42.5));
		controller.destroy();
	});

	it('keeps raw take words while no render exists', async () => {
		serveDetail({ words: takeWords, render_audio_url: '' });
		const snaps: Screen[] = [];
		const controller = await mounted(snaps);
		const snap = snaps.at(-1);
		expect(snap?.duration).toBeNull();
		expect(snap?.words.map((word) => word.start)).toEqual([0, 1, 2]);
		controller.destroy();
	});
});

describe('render clock words', () => {
	it('leaves an untouched or reverted cut in place, as the render does', () => {
		const placed = renderClockWords({
			words: takeWords,
			renderWords: [],
			proposals: [
				proposal({ id: 'a', startWord: 0, endWord: 0, decision: '' }),
				proposal({ id: 'b', startWord: 1, endWord: 1, decision: 'reverted' })
			]
		});
		expect(placed.map((word) => word.start)).toEqual([0, 1, 2]);
	});

	it('puts an applied cold open first, then the gap, then the episode', () => {
		const placed = renderClockWords({
			words: takeWords,
			renderWords: [],
			proposals: [
				proposal({ id: 'cut', startWord: 1, endWord: 1 }),
				proposal({ id: 'cold', kind: 'cold_open', startWord: 2, endWord: 2, decision: '' })
			]
		});
		// The one second opening and the gap push the episode 1.75 s later.
		expect(placed.map((word) => [word.text, word.start])).toEqual([
			['One', 1.75],
			['Two', 2.75]
		]);
	});

	it('starts at the top when the cold open is reverted', () => {
		const placed = renderClockWords({
			words: takeWords,
			renderWords: [],
			proposals: [proposal({ id: 'cold', kind: 'cold_open', startWord: 2, endWord: 2, decision: 'reverted' })]
		});
		expect(placed[0]?.start).toBe(0);
	});
});
