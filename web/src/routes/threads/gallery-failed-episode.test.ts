// A restart marks a running editorial pass interrupted and moves its
// draft to failed. The transcript pass behind it still reads done. The
// gallery card must name the pass that stopped and its reason. It must
// never tell the owner that a failed episode is ready.
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

const titled = [{ id: 'title-1', kind: 'title', start_word: 0, end_word: 0, reason: 'Take', decision: '' }];

function failed(body: Record<string, unknown>): ReturnType<typeof parseEpisodeDetail> {
	return parseEpisodeDetail(JSON.stringify({ ...failedDetail, ...body }));
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('gallery pass for a failed episode', () => {
	it('names the interrupted editorial pass', () => {
		expect(galleryPass(failed({}))).toEqual({
			jobId: 'job-9',
			status: 'interrupted',
			note: 'The editorial pass stopped when the server restarted. It does not rerun on its own.',
			proposed: false
		});
	});

	it('names a failed editorial pass with its reason', () => {
		const pass = galleryPass(failed({ editorial_outcome: { job_id: 'job-9', status: 'error', error: 'quota' } }));
		expect(pass?.note).toBe('The editorial pass failed: quota.');
	});

	it('names a failed transcript pass with its reason', () => {
		const pass = galleryPass(
			failed({ transcript_outcome: { job_id: 'job-7', status: 'error', error: 'upload refused' } })
		);
		expect(pass).toEqual({
			jobId: 'job-7',
			status: 'error',
			note: 'The transcript pass failed: upload refused.',
			proposed: false
		});
	});

	it('names an interrupted transcript pass', () => {
		const pass = galleryPass(failed({ transcript_outcome: { job_id: 'job-7', status: 'interrupted', error: '' } }));
		expect(pass?.note).toBe(
			'The transcript pass stopped when the server restarted. It does not rerun on its own.'
		);
	});

	it('says the episode failed when every pass finished', () => {
		const pass = galleryPass(
			failed({ proposals: titled, editorial_outcome: { job_id: 'job-9', status: 'done', error: '' } })
		);
		expect(pass?.note).toBe('The episode failed, and no pass stored a reason.');
	});

	it('says the editorial pass never started', () => {
		const pass = galleryPass(failed({ editorial_outcome: null }));
		expect(pass?.note).toBe('The transcript is stored, but the editorial pass never started.');
	});

	it('keeps a ready episode on its done transcript pass', () => {
		const pass = galleryPass(failed({ episode: { ...failedDetail.episode, state: 'ready' } }));
		expect(pass).toEqual({ jobId: 'job-7', status: 'done', note: '', proposed: false });
	});
});

describe('gallery card for a failed episode', () => {
	it('never shows the failed episode as ready', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
				if (url.endsWith('/api/episodes')) return Response.json({ episodes: [failedDetail.episode] });
				if (url.endsWith('/api/episodes/e1')) return Response.json(failedDetail);
				return new Response('', { status: 404 });
			})
		);
		const snaps: GallerySnapshot[] = [];
		const controller = new GalleryController((snap) => snaps.push(snap));
		controller.mount('');
		await vi.waitFor(() => expect(snaps.at(-1)?.rows[0]?.jobId).toBe('job-9'));
		await vi.waitFor(() => expect(controller.cardFor('job-9').detail).toContain('restarted'), {
			timeout: 2000
		});
		expect(controller.cardFor('job-9').detail).not.toBe('Ready. Open the episode.');
		controller.destroy();
	});
});
