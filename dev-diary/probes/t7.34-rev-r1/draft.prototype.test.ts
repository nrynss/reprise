// A live cut id that no stored proposal backs must read as empty, even
// when it matches a key every plain object inherits. The old Map lookup
// gave empty for every unknown id.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { DraftController } from './draft';

function detail() {
	return {
		episode: { id: 'live-1', number: 2, title: 'Live take', state: 'draft', visibility: 'private' },
		proposals: [
			{ id: 'cut-a', kind: 'cut', start_word: 0, end_word: 0, reason: 'Trim the open.', decision: 'accepted' }
		],
		words: [
			{ text: 'Um', start: 0, end: 0.4 },
			{ text: 'hello', start: 0.4, end: 0.9 }
		],
		audio_url: '/media/user-stem',
		render_audio_url: ''
	};
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('unbacked live cut ids', () => {
	for (const cutId of ['constructor', 'toString', '__proto__', 'hasOwnProperty', 'cut-z']) {
		it(`says no stored proposal backs ${cutId}`, async () => {
			const posted: string[] = [];
			vi.stubGlobal(
				'fetch',
				vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
					if (init?.method === 'POST') {
						posted.push(String(init.body));
						return new Response('{}', { status: 200 });
					}
					return Response.json(detail());
				})
			);
			const controller = new DraftController({ episodeId: 'live-1', onChange: () => {} });
			controller.mount('');
			await vi.waitFor(() => expect(controller.snapshot.ready).toBe(true));
			controller.revertCut(cutId);
			expect(controller.snapshot.notice).toBe(`No stored proposal backs cut ${cutId}. Nothing changed.`);
			expect(posted).toEqual([]);
			controller.destroy();
		});
	}
});
