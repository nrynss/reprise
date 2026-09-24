// A live draft whose pass still runs keeps its progress card on the
// gallery. The transcript pass reads done before the editorial pass
// stores its proposals. So the card opens the editor only once a title
// proposal is stored, and a stopped editorial pass says why.
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
	galleryCardKind,
	GalleryController,
	galleryPass,
	parseEpisodeDetail,
	jobCardHref,
	type GalleryCardProgress,
	type GallerySnapshot,
	type SeasonRow
} from './threads';

function row(partial: Partial<SeasonRow>): SeasonRow {
	return {
		id: 'e1',
		number: 1,
		title: 'Take',
		state: 'draft',
		visibility: 'private',
		meta: 'Private',
		duration: null,
		jobId: '',
		fixture: false,
		proposed: false,
		...partial
	};
}

function card(status: GalleryCardProgress['status']): GalleryCardProgress {
	return { jobId: 'job-7', percent: 40, detail: 'Working', running: status === 'running', status };
}

const titled = [{ id: 'title-1', kind: 'title', start_word: 0, end_word: 0, reason: 'Take', decision: '' }];

// Serve one draft episode whose detail names a transcript pass in the
// given state, with the given proposals and editorial pass.
function serveDraft(status: string, proposals: unknown[] = [], editorial?: Record<string, string>): void {
	vi.stubGlobal(
		'fetch',
		vi.fn(async (input: RequestInfo | URL) => {
			const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
			if (url.endsWith('/api/episodes')) {
				return Response.json({
					episodes: [{ id: 'e1', number: 1, title: 'Take', state: 'draft', visibility: 'private' }]
				});
			}
			if (url.endsWith('/api/episodes/e1')) {
				return Response.json({
					episode: { id: 'e1', number: 1, title: 'Take', state: 'draft', visibility: 'private' },
					proposals,
					transcript_outcome: { job_id: 'job-7', status, error: '' },
					...(editorial ? { editorial_outcome: editorial } : {}),
					words: [],
					audio_url: '/media/user-blob',
					render_audio_url: ''
				});
			}
			return new Response('', { status: 404 });
		})
	);
}

// Mount the live gallery and wait until the one row names its job.
async function mountedGallery(
	jobId = 'job-7'
): Promise<{ controller: GalleryController; latest: () => GallerySnapshot }> {
	const snaps: GallerySnapshot[] = [];
	const controller = new GalleryController((snap) => snaps.push(snap));
	controller.mount('');
	await vi.waitFor(() => expect(snaps.at(-1)?.rows[0]?.jobId).toBe(jobId));
	return { controller, latest: () => snaps.at(-1) as GallerySnapshot };
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('gallery card kind', () => {
	it('keeps the progress card on a draft whose pass has not finished', () => {
		expect(galleryCardKind(row({ jobId: 'job-7' }), card('running'))).toBe('job');
		expect(galleryCardKind(row({ jobId: 'job-7' }), card('queued'))).toBe('job');
		expect(galleryCardKind(row({ jobId: 'job-7' }), card('error'))).toBe('job');
		expect(galleryCardKind(row({ jobId: 'job-7' }), null)).toBe('job');
	});

	it('opens the editor for a draft whose pass is done or names no job', () => {
		expect(galleryCardKind(row({ jobId: 'job-7', proposed: true }), card('done'))).toBe('editor');
		expect(galleryCardKind(row({}), null)).toBe('editor');
		expect(galleryCardKind(row({ id: 'ep-5', fixture: true }), null)).toBe('editor');
	});

	it('keeps the progress card on a done draft whose proposals are not stored', () => {
		expect(galleryCardKind(row({ jobId: 'job-7' }), card('done'))).toBe('job');
	});

	it('keeps every other row as before', () => {
		expect(galleryCardKind(row({ state: 'rendering', jobId: 'job-r' }), card('done'))).toBe('job');
		expect(galleryCardKind(row({ state: 'ready' }), null)).toBe('episode');
	});

	it('links a running live draft card to the episode, which shows the pass', () => {
		expect(jobCardHref(row({ jobId: 'job-7' }))).toBe('/episode/e1');
		expect(jobCardHref(row({ id: 'ep-5', state: 'rendering', jobId: 'job-r', fixture: true }))).toBe(
			'/processing?episode=ep-5'
		);
		expect(jobCardHref(row({ state: 'ready', jobId: 'job-7' }))).toBe('/episode/e1');
	});
});

describe('live gallery with a draft pass', () => {
	it('shows the progress card while the pass runs', async () => {
		serveDraft('running');
		const { controller, latest } = await mountedGallery();
		const drawn = latest().rows[0] as SeasonRow;
		expect(galleryCardKind(drawn, controller.cardFor(drawn.jobId))).toBe('job');
		controller.destroy();
	});

	it('opens the editor once the pass is done and the proposals are stored', async () => {
		serveDraft('done', titled, { job_id: 'job-9', status: 'done', error: '' });
		const { controller, latest } = await mountedGallery();
		const drawn = latest().rows[0] as SeasonRow;
		expect(galleryCardKind(drawn, controller.cardFor(drawn.jobId))).toBe('editor');
		controller.destroy();
	});
});

describe('live gallery with a draft editorial pass', () => {
	it('follows the editorial pass while it runs', async () => {
		serveDraft('done', [], { job_id: 'job-9', status: 'running', error: '' });
		const { controller, latest } = await mountedGallery('job-9');
		const drawn = latest().rows[0] as SeasonRow;
		expect(drawn.proposed).toBe(false);
		expect(galleryCardKind(drawn, controller.cardFor(drawn.jobId))).toBe('job');
		controller.destroy();
	});

	it('says the editorial pass failed, with its reason', async () => {
		serveDraft('done', [], { job_id: 'job-9', status: 'error', error: 'budget refused' });
		const { controller, latest } = await mountedGallery('job-9');
		const drawn = latest().rows[0] as SeasonRow;
		expect(galleryCardKind(drawn, controller.cardFor(drawn.jobId))).toBe('job');
		await vi.waitFor(
			() => expect(controller.cardFor('job-9').detail).toBe('The editorial pass failed: budget refused.'),
			{ timeout: 2000 }
		);
		controller.destroy();
	});

	it('says the editorial pass never started when the detail names none', async () => {
		serveDraft('done');
		const { controller, latest } = await mountedGallery();
		const drawn = latest().rows[0] as SeasonRow;
		expect(galleryCardKind(drawn, controller.cardFor(drawn.jobId))).toBe('job');
		await vi.waitFor(
			() =>
				expect(controller.cardFor('job-7').detail).toBe(
					'The transcript is stored, but the editorial pass never started.'
				),
			{ timeout: 2000 }
		);
		controller.destroy();
	});
});

describe('gallery pass', () => {
	function detail(body: Record<string, unknown>): ReturnType<typeof parseEpisodeDetail> {
		return parseEpisodeDetail(
			JSON.stringify({
				episode: { id: 'e1', number: 1, title: 'Take', state: 'draft', visibility: 'private' },
				proposals: [],
				transcript_outcome: { job_id: 'job-7', status: 'done', error: '' },
				...body
			})
		);
	}

	it('follows the transcript pass until it is done', () => {
		expect(galleryPass(detail({ transcript_outcome: { job_id: 'job-7', status: 'running' } }))).toEqual({
			jobId: 'job-7',
			status: 'running',
			note: '',
			proposed: false
		});
	});

	it('stays on the transcript pass once the title is stored', () => {
		expect(
			galleryPass(detail({ proposals: titled, editorial_outcome: { job_id: 'job-9', status: 'done' } }))
		).toEqual({ jobId: 'job-7', status: 'done', note: '', proposed: true });
	});

	it('names an interrupted editorial pass honestly', () => {
		expect(galleryPass(detail({ editorial_outcome: { job_id: 'job-9', status: 'interrupted' } }))?.note).toBe(
			'The editorial pass stopped when the server restarted. It does not rerun on its own.'
		);
	});

	it('names an editorial pass that finished with no proposals', () => {
		expect(galleryPass(detail({ editorial_outcome: { job_id: 'job-9', status: 'done' } }))).toEqual({
			jobId: 'job-9',
			status: 'done',
			note: 'The editorial pass finished but stored no proposals.',
			proposed: false
		});
	});

	it('names no pass when the detail names no transcript pass', () => {
		expect(galleryPass(detail({ transcript_outcome: null }))).toBeNull();
	});
});
