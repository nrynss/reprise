// Pins for the processing steps. The draft stays waiting until the upload
// and both passes have finished, including when a pass has no job yet.
// A retry of a failed draft move must not leave transcription failed while
// the new job is still running.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { progressFrame } from './mock';
import {
	depositProcessingHandoff,
	draftStep,
	emptyProcessing,
	ProcessingController,
	readingFromJob,
	resumedUploadDetail,
	stoppedDetail,
	TRANSCRIBING_DETAIL,
	uploadDetail,
	uploadPercent,
	type ProcessingSnapshot,
	type ProcessingStep
} from './processing-state';

const waiting: ProcessingStep = { name: 'Transcription', detail: 'Waiting.', state: 'waiting', percent: 0 };

describe('draftStep', () => {
	it('stays waiting when either pass has not finished', () => {
		expect(draftStep('done', 'waiting', 'waiting').state).toBe('waiting');
		expect(draftStep('done', 'done', 'waiting').state).toBe('waiting');
		expect(draftStep('running', 'done', 'done').state).toBe('waiting');
	});

	it('reads done only after the upload and both passes', () => {
		expect(draftStep('done', 'done', 'done')).toEqual({
			name: 'Draft',
			detail: 'The draft is ready.',
			state: 'done',
			percent: 100
		});
	});

	it('reads failed when a pass or the upload stops', () => {
		expect(draftStep('failed', 'done', 'done').state).toBe('failed');
		expect(draftStep('done', 'failed', 'waiting').detail).toBe('A pass stopped, so the draft is not ready.');
		expect(draftStep('done', 'done', 'failed').state).toBe('failed');
	});
});

describe('readingFromJob', () => {
	it('does not drag a finished pass back to a catch-up frame', () => {
		const done: ProcessingStep = {
			name: 'Transcription',
			detail: 'Transcript ready.',
			state: 'done',
			percent: 100
		};
		const next = readingFromJob(done, 'queued', 'connecting', undefined, undefined, '', 'Transcript ready.', 'transcript');
		expect(next.state).toBe('done');
	});

	it('names an interrupted pass and a failed pass', () => {
		expect(stoppedDetail('editorial', 'interrupted', '')).toBe(
			'The editorial pass stopped when the server restarted. It does not rerun on its own.'
		);
		const failed = readingFromJob(waiting, 'error', 'live', undefined, undefined, 'budget refused', 'Transcript ready.', 'transcript');
		expect(failed.state).toBe('failed');
		expect(failed.detail).toBe('The transcript pass failed: budget refused.');
	});
});

describe('uploadDetail', () => {
	it('names the bytes the server has acknowledged', () => {
		expect(uploadDetail(0, 0)).toBe('Uploading the take.');
		expect(uploadDetail(10, 40)).toBe('Uploading the take. 10 of 40 bytes.');
		expect(uploadPercent(10, 40)).toBe(25);
		expect(uploadPercent(0, 0)).toBe(0);
	});
});

describe('resumedUploadDetail', () => {
	it('reads durable when the resumed receipts carry no bytes', () => {
		expect(resumedUploadDetail(2, 0)).toBe('Both stems durable.');
	});

	it('names the stored stems and bytes', () => {
		expect(resumedUploadDetail(2, 8164824)).toBe('2 stems, 8164824 bytes durable.');
	});
});

function requestUrl(input: RequestInfo | URL): string {
	if (typeof input === 'string') return input;
	if (input instanceof URL) return input.href;
	return input.url;
}

function openJobStream(): Response {
	const body = new ReadableStream<Uint8Array>({
		start(controller) {
			controller.enqueue(new TextEncoder().encode(progressFrame('tj-1', 'running', 1, 4, 1)));
		}
	});
	return new Response(body, {
		status: 200,
		headers: { 'content-type': 'text/event-stream' }
	});
}

async function waitUntil(read: () => boolean): Promise<void> {
	const start = Date.now();
	while (!read()) {
		if (Date.now() - start > 2000) throw new Error('the processing step did not update');
		await new Promise((resolve) => setTimeout(resolve, 10));
	}
}

describe('watching detail', () => {
	it('names the take while a watched pass runs', () => {
		expect(TRANSCRIBING_DETAIL).toBe('Transcribing your take.');
		let controller: ProcessingController | null = null;
		vi.stubGlobal(
			'fetch',
			vi.fn(async () => new Response('{"error":"slow"}', { status: 500 }))
		);
		try {
			let latest = '';
			controller = new ProcessingController(
				new URLSearchParams('episode=ep-1&transcript=tj-1&uploads=done&userBytes=10&hostBytes=20'),
				(snap) => {
					latest = snap.transcription.detail;
				}
			);
			controller.mount();
			expect(latest).toBe(TRANSCRIBING_DETAIL);
		} finally {
			controller?.destroy();
			vi.unstubAllGlobals();
		}
	});
});

describe('retry after a failed draft move', () => {
	let controller: ProcessingController | null = null;

	afterEach(() => {
		controller?.destroy();
		controller = null;
		vi.unstubAllGlobals();
	});

	it('clears a failed transcript step while the new job is still running', async () => {
		let settles = 0;
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = requestUrl(input);
				if (url.includes('/events')) return openJobStream();
				if (url.includes('/api/jobs/')) {
					return new Response(
						JSON.stringify({ jobId: 'tj-1', status: 'running', stage: 'running', current: 1, total: 4 }),
						{ status: 200, headers: { 'content-type': 'application/json' } }
					);
				}
				return new Response(JSON.stringify({ transcript_outcome: null, editorial_outcome: null }), {
					status: 200,
					headers: { 'content-type': 'application/json' }
				});
			})
		);
		depositProcessingHandoff({
			progress: () => ({ stored: 4, captured: 8 }),
			settle: async () => {
				settles += 1;
				if (settles === 1) throw new Error('draft move refused');
				return { userBytes: 10, hostBytes: 20, transcriptJob: 'tj-1' };
			}
		});
		let latest: ProcessingSnapshot = emptyProcessing();
		controller = new ProcessingController(new URLSearchParams('episode=ep-1'), (snap) => {
			latest = snap;
		});
		controller.mount();
		await waitUntil(() => latest.transcription.state === 'failed');
		expect(latest.draft.state).toBe('failed');
		controller.retrySettle();
		const started = Date.now();
		while (latest.transcription.state === 'failed' && Date.now() - started < 2000) {
			await new Promise((resolve) => setTimeout(resolve, 10));
		}
		expect(latest.transcription.state).toBe('running');
		expect(latest.draft.state).not.toBe('failed');
		expect(latest.upload.state).not.toBe('failed');
		expect(latest.editorial.state).not.toBe('failed');
	});
});

describe('detail polling', () => {
	let controller: ProcessingController | null = null;

	afterEach(() => {
		controller?.destroy();
		controller = null;
		vi.unstubAllGlobals();
		vi.useRealTimers();
	});

	it('reads the detail at most every few seconds while a job id stays unknown', async () => {
		vi.useFakeTimers();
		let detailReads = 0;
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = requestUrl(input);
				if (url.includes('/events')) return openJobStream();
				if (url.includes('/api/jobs/')) {
					return new Response(
						JSON.stringify({ jobId: 'tj-1', status: 'running', stage: 'running', current: 1, total: 4 }),
						{ status: 200, headers: { 'content-type': 'application/json' } }
					);
				}
				if (url.includes('/api/episodes/')) {
					detailReads += 1;
					return new Response(
						JSON.stringify({
							transcript_outcome: { job_id: 'tj-1', status: 'running', error: '' },
							editorial_outcome: null
						}),
						{ status: 200, headers: { 'content-type': 'application/json' } }
					);
				}
				return new Response('nope', { status: 404 });
			})
		);
		controller = new ProcessingController(
			new URLSearchParams('episode=ep-1&transcript=tj-1&uploads=done&userBytes=10&hostBytes=20'),
			() => {}
		);
		controller.mount();
		await vi.advanceTimersByTimeAsync(10000);
		expect(detailReads).toBeGreaterThan(0);
		expect(detailReads).toBeLessThanOrEqual(4);
	});
});
