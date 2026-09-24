// Review probe. Copy into web/src/lib/editor/ and run
// `npx vitest run src/lib/editor/draft-queued.probe.test.ts` from web/.
// A live draft marked done while another render holds the slot gets a
// 202 with queued true and an empty job id. The editor must not call that
// the fixture render. A refused mark done must not read as running.
import { describe, expect, it, vi } from 'vitest';
import { DraftController } from './draft';

const detail = {
	episode: { id: 'live-1', number: 2, title: 'Live take', state: 'draft', visibility: 'private' },
	proposals: [],
	words: [
		{ text: 'Hello', start: 0, end: 0.4 },
		{ text: 'there', start: 0.4, end: 0.9 }
	],
	audio_url: '/media/user-stem',
	render_audio_url: ''
};

async function markDoneAnswered(done: Response): Promise<string> {
	vi.stubGlobal(
		'fetch',
		vi.fn((url: string) => {
			if (String(url).endsWith('/done')) return Promise.resolve(done);
			return Promise.resolve(Response.json(detail));
		})
	);
	try {
		const controller = new DraftController({ episodeId: 'live-1', onChange: () => {} });
		controller.mount('');
		await vi.waitFor(() => {
			expect(controller.snapshot.ready).toBe(true);
		});
		controller.markDone();
		controller.markDone();
		await vi.waitFor(() => {
			expect(controller.snapshot.renderStage).not.toBe('starting');
		});
		const line = controller.snapshot.renderDetail;
		controller.destroy();
		return line;
	} finally {
		vi.unstubAllGlobals();
	}
}

describe('probe: the editor after a live mark done', () => {
	it('never calls a queued render the fixture render', async () => {
		const line = await markDoneAnswered(
			Response.json({ episode_id: 'live-1', job_id: '', queued: true, state: 'rendering' }, { status: 202 })
		);
		expect(line).not.toContain('fixture');
		expect(line.toLowerCase()).toContain('queue');
	});

	it('never says a refused render is running', async () => {
		const line = await markDoneAnswered(
			Response.json({ error: { code: 'render_unavailable', message: 'x' } }, { status: 503 })
		);
		expect(line).not.toContain('Render running');
	});
});
