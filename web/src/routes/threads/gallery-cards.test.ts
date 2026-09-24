// A live draft whose pass still runs keeps its progress card on the
// gallery. Once the pass is done the card opens the editor.
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
	galleryCardKind,
	GalleryController,
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
		...partial
	};
}

function card(status: GalleryCardProgress['status']): GalleryCardProgress {
	return { jobId: 'job-7', percent: 40, detail: 'Working', running: status === 'running', status };
}

// Serve one draft episode whose detail names a pass in the given state.
function serveDraft(status: string): void {
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
					proposals: [],
					transcript_outcome: { job_id: 'job-7', status, error: '' },
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
async function mountedGallery(): Promise<{ controller: GalleryController; latest: () => GallerySnapshot }> {
	const snaps: GallerySnapshot[] = [];
	const controller = new GalleryController((snap) => snaps.push(snap));
	controller.mount('');
	await vi.waitFor(() => expect(snaps.at(-1)?.rows[0]?.jobId).toBe('job-7'));
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
		expect(galleryCardKind(row({ jobId: 'job-7' }), card('done'))).toBe('editor');
		expect(galleryCardKind(row({}), null)).toBe('editor');
		expect(galleryCardKind(row({ id: 'ep-5', fixture: true }), null)).toBe('editor');
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

	it('opens the editor once the pass is done', async () => {
		serveDraft('done');
		const { controller, latest } = await mountedGallery();
		const drawn = latest().rows[0] as SeasonRow;
		expect(galleryCardKind(drawn, controller.cardFor(drawn.jobId))).toBe('editor');
		controller.destroy();
	});
});
