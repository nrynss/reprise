// A live draft writes its reverts against the stored proposals and
// loads the stored decisions back, so the editor shows what the render
// will do. A refused revert puts the proposal back and says so.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { DraftController } from './draft';

interface StoredRow {
	id: string;
	kind: string;
	start_word: number;
	end_word: number;
	reason: string;
	decision: string;
}

function detail(proposals: StoredRow[]) {
	return {
		episode: { id: 'live-1', number: 2, title: 'Live take', state: 'draft', visibility: 'private' },
		proposals,
		words: [
			{ text: 'Um', start: 0, end: 0.4 },
			{ text: 'hello', start: 0.4, end: 0.9 },
			{ text: 'there', start: 0.9, end: 1.3 }
		],
		audio_url: '/media/user-stem',
		render_audio_url: ''
	};
}

function cut(decision: string): StoredRow {
	return { id: 'cut-a', kind: 'cut', start_word: 0, end_word: 0, reason: 'Trim the open.', decision };
}

function coldOpen(decision: string): StoredRow {
	return { id: 'cold-9', kind: 'cold_open', start_word: 1, end_word: 2, reason: 'Strong line.', decision };
}

function title(decision: string): StoredRow {
	return { id: 'title-9', kind: 'title', start_word: 0, end_word: 0, reason: 'Live take', decision };
}

// Serve the detail on GET and answer every POST with postStatus. Each
// posted body lands in posted.
function serve(body: unknown, postStatus: number, posted: string[]): void {
	vi.stubGlobal(
		'fetch',
		vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
			if (init?.method === 'POST') {
				posted.push(String(init.body));
				return new Response(JSON.stringify({}), { status: postStatus });
			}
			return Response.json(body);
		})
	);
}

async function mounted(): Promise<DraftController> {
	const controller = new DraftController({ episodeId: 'live-1', onChange: () => {} });
	controller.mount('');
	await vi.waitFor(() => expect(controller.snapshot.ready).toBe(true));
	return controller;
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('live draft decisions', () => {
	it('posts the stored proposal id when a live cut is reverted', async () => {
		const posted: string[] = [];
		serve(detail([cut('accepted')]), 200, posted);
		const controller = await mounted();
		controller.revertCut('cut-a');
		await vi.waitFor(() => expect(posted.length).toBe(1));
		expect(JSON.parse(posted[0] ?? '{}')).toEqual({ proposal_id: 'cut-a', decision: 'reverted' });
		expect(controller.snapshot.appliedCount).toBe(0);
		controller.destroy();
	});

	it('posts the stored proposal ids when a live cold open and title are reverted', async () => {
		const posted: string[] = [];
		serve(detail([coldOpen(''), title('')]), 200, posted);
		const controller = await mounted();
		controller.revertColdOpen();
		controller.revertTitle();
		await vi.waitFor(() => expect(posted.length).toBe(2));
		expect(posted.map((body) => JSON.parse(body).proposal_id)).toEqual(['cold-9', 'title-9']);
		controller.destroy();
	});

	it('puts a live cut back and says so when the server refuses the revert', async () => {
		const posted: string[] = [];
		serve(detail([cut('accepted')]), 404, posted);
		const controller = await mounted();
		controller.revertCut('cut-a');
		expect(controller.snapshot.appliedCount).toBe(0);
		await vi.waitFor(() => expect(controller.snapshot.appliedCount).toBe(1));
		expect(controller.snapshot.decisions).toEqual([]);
		expect(controller.snapshot.notice).toContain('did not store that revert');
		expect(controller.snapshot.cutCards.map((card) => card.proposalId)).toEqual(['cut-a']);
		controller.destroy();
	});

	it('puts a live cold open back when the server refuses the revert', async () => {
		serve(detail([coldOpen('')]), 500, []);
		const controller = await mounted();
		controller.revertColdOpen();
		expect(controller.snapshot.coldOpenReverted).toBe(true);
		await vi.waitFor(() => expect(controller.snapshot.coldOpenReverted).toBe(false));
		expect(controller.snapshot.decisions).toEqual([]);
		expect(controller.snapshot.notice).toContain('did not store that revert');
		controller.destroy();
	});

	it('loads a stored reverted cut as not applied', async () => {
		serve(detail([cut('reverted')]), 200, []);
		const controller = await mounted();
		expect(controller.snapshot.appliedCount).toBe(0);
		expect(controller.snapshot.cutOf).toEqual([null, null, null]);
		expect(controller.snapshot.decisions.map((row) => row.proposalId)).toEqual(['cut-a']);
		controller.destroy();
	});

	it('loads a stored reverted cold open and title as reverted', async () => {
		serve(detail([coldOpen('reverted'), title('reverted')]), 200, []);
		const controller = await mounted();
		expect(controller.snapshot.coldOpenReverted).toBe(true);
		expect(controller.snapshot.titleReverted).toBe(true);
		expect(controller.snapshot.title).toBe('Episode live-1');
		expect(controller.snapshot.decisions.map((row) => row.proposalId)).toEqual(['cold-9', 'title-9']);
		controller.destroy();
	});
});
