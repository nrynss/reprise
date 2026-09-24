// Review pin. The episode page plays the render, so its words and
// duration must sit on the render clock, not the raw take clock.
import { describe, expect, it, vi } from 'vitest';
import { EpisodeController, emptyScreen } from './threads';

describe('render clock', () => {
	it('places words after an accepted cut on the rendered file clock', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
				if (url.includes('/api/threads')) return Response.json({ name_threads: [], circled_topics: [] });
				return Response.json({
					episode: { id: 'ep-9', number: 3, title: 'Heard', state: 'ready', visibility: 'private' },
					proposals: [
						{ id: 'cut-1', kind: 'cut', start_word: 1, end_word: 1, reason: 'filler', decision: 'accepted' }
					],
					words: [
						{ text: 'One', start: 0, end: 1 },
						{ text: 'um', start: 1, end: 2 },
						{ text: 'Two', start: 2, end: 3 }
					],
					audio_url: '/media/stem',
					render_audio_url: '/media/opus-9'
				});
			})
		);
		try {
			const snaps: Array<ReturnType<typeof emptyScreen>> = [];
			const controller = new EpisodeController('ep-9', (snap) => snaps.push(snap));
			controller.mount('');
			await vi.waitFor(() => expect(snaps.at(-1)?.ready).toBe(true));
			const snap = snaps.at(-1);
			// The render removed one second, so it runs two seconds and
			// "Two" starts at one second in the file the player loaded.
			expect(snap?.duration).toBe(2);
			expect(snap?.words.find((word) => word.text === 'Two')?.start).toBe(1);
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});
});
