// Review probe. Copy into web/src/routes/threads/ and run
// `npx vitest run src/routes/threads/gallery-queued-poll.probe.test.ts`
// from web/. A queued card reads its detail again every four refresh
// ticks until the detail names a pass that is not queued. If the detail
// starts refusing, for an episode that is gone, the card keeps asking for
// as long as the page stays open. The probe drives fake timers, so it
// counts ticks and never measures elapsed time.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { GalleryController } from './threads';

function outcome(jobId: string, status: string): Record<string, string> {
	return { job_id: jobId, status, error: '' };
}

const queuedBody = {
	episode: { id: 'e1', number: 1, title: 'Take', state: 'rendering', visibility: 'private' },
	proposals: [{ id: 'title-1', kind: 'title', start_word: 0, end_word: 0, reason: 'Take', decision: '' }],
	transcript_outcome: outcome('job-t', 'done'),
	editorial_outcome: outcome('job-e', 'done'),
	words: [],
	audio_url: '/media/user-blob',
	render_audio_url: ''
};

afterEach(() => {
	vi.useRealTimers();
	vi.unstubAllGlobals();
});

describe('probe: a queued gallery card', () => {
	it('stops asking for a detail that refuses', async () => {
		vi.useFakeTimers();
		let reads = 0;
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
				if (url.endsWith('/api/episodes')) return Response.json({ episodes: [queuedBody.episode] });
				if (url.endsWith('/api/episodes/e1')) {
					reads += 1;
					// The first read queues the card. The episode is gone after it.
					return reads === 1 ? Response.json(queuedBody) : new Response('', { status: 404 });
				}
				if (url.endsWith('/events')) {
					return new Response(new ReadableStream({ start() {} }), {
						headers: { 'content-type': 'text/event-stream' }
					});
				}
				const job = url.match(/\/api\/jobs\/([^/]+)$/);
				if (job) return Response.json({ jobId: job[1], status: 'done' });
				return new Response('', { status: 404 });
			})
		);
		const controller = new GalleryController(() => {});
		controller.mount('');
		await vi.waitFor(() => expect(reads).toBe(1));
		// 400 half second ticks is 100 queued rechecks.
		await vi.advanceTimersByTimeAsync(400 * 500);
		const refused = reads - 1;
		controller.destroy();
		expect(refused).toBeLessThan(10);
	});
});
