// Review probe. Mark done moves a draft to rendering. The transcript pass
// behind it still reads done, and nothing stamps the render job with its
// episode. The gallery card follows the done transcript pass, so it tells
// the owner a rendering episode is ready. The task base did the same once
// the job stream read done. Run from web/src/routes/threads/.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { GalleryController, type GallerySnapshot } from './threads';

const renderingDetail = {
	episode: { id: 'e1', number: 1, title: 'Take', state: 'rendering', visibility: 'private' },
	proposals: [{ id: 'title-1', kind: 'title', start_word: 0, end_word: 0, reason: 'Take', decision: '' }],
	transcript_outcome: { job_id: 'job-7', status: 'done', error: '' },
	editorial_outcome: { job_id: 'job-9', status: 'done', error: '' },
	words: [],
	audio_url: '/media/user-blob',
	render_audio_url: ''
};

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('gallery card for a rendering episode', () => {
	it('never tells the owner a rendering episode is ready', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
				if (url.endsWith('/api/episodes')) return Response.json({ episodes: [renderingDetail.episode] });
				if (url.endsWith('/api/episodes/e1')) return Response.json(renderingDetail);
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
		const snaps: GallerySnapshot[] = [];
		const controller = new GalleryController((snap) => snaps.push(snap));
		controller.mount('');
		await vi.waitFor(() => expect(snaps.at(-1)?.rows[0]?.jobId).toBeTruthy());
		const jobId = snaps.at(-1)?.rows[0]?.jobId ?? '';
		await new Promise((settle) => setTimeout(settle, 1200));
		expect(controller.cardFor(jobId).detail).not.toBe('Ready. Open the episode.');
		controller.destroy();
	});
});
