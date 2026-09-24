// Review pin. A restart marks a running editorial pass interrupted and
// moves its draft to failed. The transcript pass behind it still reads
// done. The gallery card must name the stopped editorial pass, never
// tell the owner the failed episode is ready.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { GalleryController, galleryPass, parseEpisodeDetail, type GallerySnapshot } from './threads';

const failedDetail = {
	episode: { id: 'e1', number: 1, title: 'Take', state: 'failed', visibility: 'private' },
	proposals: [],
	transcript_outcome: { job_id: 'job-7', status: 'done', error: '' },
	editorial_outcome: { job_id: 'job-9', status: 'interrupted', error: '' },
	words: [],
	audio_url: '/media/user-blob',
	render_audio_url: ''
};

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('gallery card after a restart stops the editorial pass', () => {
	it('names the interrupted editorial pass for the failed episode', () => {
		const pass = galleryPass(parseEpisodeDetail(JSON.stringify(failedDetail)));
		expect(pass?.note).toBe(
			'The editorial pass stopped when the server restarted. It does not rerun on its own.'
		);
	});

	it('never shows the failed episode card as ready', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
				if (url.endsWith('/api/episodes')) {
					return Response.json({
						episodes: [{ id: 'e1', number: 1, title: 'Take', state: 'failed', visibility: 'private' }]
					});
				}
				if (url.endsWith('/api/episodes/e1')) return Response.json(failedDetail);
				return new Response('', { status: 404 });
			})
		);
		const snaps: GallerySnapshot[] = [];
		const controller = new GalleryController((snap) => snaps.push(snap));
		controller.mount('');
		await vi.waitFor(() => expect(['job-7', 'job-9']).toContain(snaps.at(-1)?.rows[0]?.jobId));
		const jobId = snaps.at(-1)?.rows[0]?.jobId ?? '';
		await new Promise((settle) => setTimeout(settle, 1200));
		expect(controller.cardFor(jobId).detail).not.toBe('Ready. Open the episode.');
		expect(controller.cardFor(jobId).detail).toContain('restarted');
		controller.destroy();
	});
});
