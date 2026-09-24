// A stored proposal whose id matches an inherited key must post that id
// when its cut reverts, as the old Map lookup did.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { DraftController } from './draft';

function detail(id: string) {
	return {
		episode: { id: 'live-1', number: 2, title: 'Live take', state: 'draft', visibility: 'private' },
		proposals: [{ id, kind: 'cut', start_word: 0, end_word: 0, reason: 'Trim the open.', decision: 'accepted' }],
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

describe('stored ids that match inherited keys', () => {
	for (const id of ['__proto__', 'constructor', 'toString', 'abc123']) {
		it(`posts ${id} when its cut reverts`, async () => {
			const posted: string[] = [];
			vi.stubGlobal(
				'fetch',
				vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
					if (init?.method === 'POST') {
						posted.push(String(init.body));
						return new Response('{}', { status: 200 });
					}
					return Response.json(detail(id));
				})
			);
			const controller = new DraftController({ episodeId: 'live-1', onChange: () => {} });
			controller.mount('');
			await vi.waitFor(() => expect(controller.snapshot.ready).toBe(true));
			controller.revertCut(id);
			await vi.waitFor(() => expect(posted.length).toBe(1));
			expect(JSON.parse(posted[0]).proposal_id).toBe(id);
			controller.destroy();
		});
	}
});
