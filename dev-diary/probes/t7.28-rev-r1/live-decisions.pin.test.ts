// Review pins. A live draft must post the stored proposal id on a
// revert, and must load a stored revert as not applied.
import { describe, expect, it, vi } from 'vitest';
import { DraftController } from './draft';

function detail(decision: string) {
	return {
		episode: { id: 'live-1', number: 2, title: 'Live take', state: 'draft', visibility: 'private' },
		proposals: [
			{ id: 'cut-a', kind: 'cut', start_word: 0, end_word: 0, reason: 'Trim the open.', decision }
		],
		words: [
			{ text: 'Um', start: 0, end: 0.4 },
			{ text: 'hello', start: 0.4, end: 0.9 }
		],
		audio_url: '/media/user-stem',
		render_audio_url: ''
	};
}

describe('live draft decisions', () => {
	it('posts the stored proposal id when a live cut is reverted', async () => {
		const posted: string[] = [];
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
				if (init?.method === 'POST') {
					posted.push(String(init.body));
					return Response.json({});
				}
				return Response.json(detail('accepted'));
			})
		);
		try {
			const controller = new DraftController({ episodeId: 'live-1', onChange: () => {} });
			controller.mount('');
			await vi.waitFor(() => expect(controller.snapshot.ready).toBe(true));
			controller.revertCut('cut-a');
			await vi.waitFor(() => expect(posted.length).toBe(1));
			expect(JSON.parse(posted[0] ?? '{}').proposal_id).toBe('cut-a');
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it('loads a stored reverted cut as not applied', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json(detail('reverted'))));
		try {
			const controller = new DraftController({ episodeId: 'live-1', onChange: () => {} });
			controller.mount('');
			await vi.waitFor(() => expect(controller.snapshot.ready).toBe(true));
			expect(controller.snapshot.appliedCount).toBe(0);
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});
});
