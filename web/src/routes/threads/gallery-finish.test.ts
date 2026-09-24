// Mark done moves a draft to rendering, a finished render moves it to
// analysing, and the episode reads ready once marking stops. The
// transcript and editorial passes behind it still read done the whole
// time. The gallery card follows the pass the episode waits on, and it
// never tells the owner the episode is ready before the episode says so.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { GalleryController, galleryPass, parseEpisodeDetail, type GallerySnapshot } from './threads';

const READY = 'Ready. Open the episode.';

const titled = [{ id: 'title-1', kind: 'title', start_word: 0, end_word: 0, reason: 'Take', decision: '' }];

function outcome(jobId: string, status: string, error = ''): Record<string, string> {
	return { job_id: jobId, status, error };
}

function detailBody(state: string, passes: Record<string, unknown> = {}): Record<string, unknown> {
	return {
		episode: { id: 'e1', number: 1, title: 'Take', state, visibility: 'private' },
		proposals: titled,
		transcript_outcome: outcome('job-t', 'done'),
		editorial_outcome: outcome('job-e', 'done'),
		words: [],
		audio_url: '/media/user-blob',
		render_audio_url: '',
		...passes
	};
}

function detail(state: string, passes: Record<string, unknown> = {}): ReturnType<typeof parseEpisodeDetail> {
	return parseEpisodeDetail(JSON.stringify(detailBody(state, passes)));
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('gallery pass after mark done', () => {
	it('follows a running render and carries the line its done state shows', () => {
		expect(galleryPass(detail('rendering', { render_outcome: outcome('job-r', 'running') }))).toEqual({
			jobId: 'job-r',
			status: 'running',
			note: 'The render is done. The analysis starts next.',
			proposed: true
		});
	});

	it('names a failed render on a rendering episode', () => {
		const pass = galleryPass(detail('rendering', { render_outcome: outcome('job-r', 'error', 'ffmpeg refused') }));
		expect(pass?.note).toBe('The render pass failed: ffmpeg refused.');
	});

	it('waits on the render when the detail names none yet', () => {
		expect(galleryPass(detail('rendering'))).toEqual({
			jobId: 'job-t',
			status: 'done',
			note: 'The episode is rendering. The render pass has not reported yet.',
			proposed: true
		});
	});

	it('follows the analysis on an analysing episode', () => {
		const pass = galleryPass(
			detail('analysing', {
				render_outcome: outcome('job-r', 'done'),
				analysis_outcome: outcome('job-a', 'running')
			})
		);
		expect(pass).toEqual({
			jobId: 'job-a',
			status: 'running',
			note: 'The analysis is done. Marking commitments comes next.',
			proposed: true
		});
	});

	it('follows the marking once the analysis is done', () => {
		const pass = galleryPass(
			detail('analysing', {
				render_outcome: outcome('job-r', 'done'),
				analysis_outcome: outcome('job-a', 'done'),
				memory_outcome: outcome('job-m', 'running')
			})
		);
		expect(pass?.jobId).toBe('job-m');
		expect(pass?.note).toBe('Commitments are marked. The episode is nearly ready.');
	});

	it('says a failed analysis still ships', () => {
		const pass = galleryPass(
			detail('analysing', {
				render_outcome: outcome('job-r', 'done'),
				analysis_outcome: outcome('job-a', 'error', 'batch refused')
			})
		);
		expect(pass?.note).toBe('The analysis pass failed: batch refused. The episode still ships without chapters.');
	});

	it('waits on the analysis when the detail names none yet', () => {
		const pass = galleryPass(detail('analysing', { render_outcome: outcome('job-r', 'done') }));
		expect(pass).toEqual({
			jobId: 'job-r',
			status: 'done',
			note: 'The render is done. The analysis pass has not reported yet.',
			proposed: true
		});
	});

	it('names the render that failed a failed episode', () => {
		const pass = galleryPass(detail('failed', { render_outcome: outcome('job-r', 'interrupted') }));
		expect(pass?.jobId).toBe('job-r');
		expect(pass?.note).toBe('The render pass stopped when the server restarted. It does not rerun on its own.');
	});

	it('keeps a ready episode on its done transcript pass', () => {
		const pass = galleryPass(
			detail('ready', {
				render_outcome: outcome('job-r', 'done'),
				analysis_outcome: outcome('job-a', 'done'),
				memory_outcome: outcome('job-m', 'done')
			})
		);
		expect(pass).toEqual({ jobId: 'job-t', status: 'done', note: '', proposed: true });
	});
});

// Serve one episode whose detail walks through the given bodies, one per
// read, and holds the last. Every named job reads done, and every job
// stream stays open with no events.
function serveWalk(bodies: Array<Record<string, unknown>>): void {
	let reads = 0;
	vi.stubGlobal(
		'fetch',
		vi.fn(async (input: RequestInfo | URL) => {
			const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
			if (url.endsWith('/api/episodes')) {
				const first = bodies[0] as { episode: unknown };
				return Response.json({ episodes: [first.episode] });
			}
			if (url.endsWith('/api/episodes/e1')) {
				const body = bodies[Math.min(reads, bodies.length - 1)];
				reads += 1;
				return Response.json(body);
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
}

describe('live gallery after mark done', () => {
	it('walks a rendering card through analysis and marking to ready', async () => {
		serveWalk([
			detailBody('rendering', { render_outcome: outcome('job-r', 'running') }),
			detailBody('analysing', {
				render_outcome: outcome('job-r', 'done'),
				analysis_outcome: outcome('job-a', 'running')
			}),
			detailBody('analysing', {
				render_outcome: outcome('job-r', 'done'),
				analysis_outcome: outcome('job-a', 'done'),
				memory_outcome: outcome('job-m', 'running')
			}),
			detailBody('ready', {
				render_outcome: outcome('job-r', 'done'),
				analysis_outcome: outcome('job-a', 'done'),
				memory_outcome: outcome('job-m', 'done')
			})
		]);
		// The rows mutate in place, so each snapshot is copied as drawn.
		const snaps: GallerySnapshot[] = [];
		const controller = new GalleryController((snap) => snaps.push(structuredClone(snap)));
		controller.mount('');
		await vi.waitFor(() => expect(snaps.at(-1)?.rows[0]?.state).toBe('ready'), { timeout: 8000 });
		await vi.waitFor(() => {
			const row = snaps.at(-1)?.rows[0];
			expect(controller.cardFor(row?.jobId ?? '').detail).toBe(READY);
		});
		expect(controller.cardFor('job-r').detail).toBe('The render is done. The analysis starts next.');
		expect(controller.cardFor('job-a').detail).toBe('The analysis is done. Marking commitments comes next.');
		expect(controller.cardFor('job-m').detail).toBe('Commitments are marked. The episode is nearly ready.');
		const early = snaps.filter((snap) => {
			const row = snap.rows[0];
			return row !== undefined && row.state !== 'ready' && snap.progress[row.jobId]?.detail === READY;
		});
		expect(early).toEqual([]);
		expect(snaps.some((snap) => snap.rows[0]?.state === 'analysing')).toBe(true);
		controller.destroy();
	});

	it('never reads ready on a rendering card whose render has not reported', async () => {
		serveWalk([detailBody('rendering')]);
		const snaps: GallerySnapshot[] = [];
		const controller = new GalleryController((snap) => snaps.push(snap));
		controller.mount('');
		await vi.waitFor(() => expect(snaps.at(-1)?.rows[0]?.jobId).toBe('job-t'));
		await vi.waitFor(() =>
			expect(controller.cardFor('job-t').detail).toBe(
				'The episode is rendering. The render pass has not reported yet.'
			)
		);
		expect(snaps.at(-1)?.rows[0]?.state).toBe('rendering');
		controller.destroy();
	});
});
