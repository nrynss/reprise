// The session screen that follows a take. It draws the upload and the two
// post jobs through the same job follower the gallery card reads, so the
// screen and the card never drift into two mechanisms. Like the take, it
// reports through snapshots the page renders.

import { ChunkUploader } from '@nrynss/chaaya/audio';
import { JobStream, type JobSnapshot } from '@nrynss/chaaya/job';
import { readJobState } from './session-calls';
import { completionPair, postStemsComplete } from '../../routes/record/stems-complete';
import {
	MockUploadServer,
	progressFrame,
	rehydrateServerFromBrowser,
	sseResponse,
	statusFrame
} from '$lib/voice/mock';

export type ProcessingState = 'waiting' | 'running' | 'done' | 'failed';

export interface ProcessingStep {
	name: string;
	detail: string;
	state: ProcessingState;
	percent: number;
}

// ProcessingSnapshot carries the four steps and the episode they belong to.
export interface ProcessingSnapshot {
	episode: string;
	upload: ProcessingStep;
	transcription: ProcessingStep;
	editorial: ProcessingStep;
	draft: ProcessingStep;
}

// ProcessingHandoff is an upload the record page already started. The
// processing screen keeps that same upload, so leaving the record page
// does not drop bytes that are still in flight.
export interface ProcessingHandoff {
	progress(): { stored: number; captured: number };
	settle(): Promise<{ userBytes: number; hostBytes: number; transcriptJob: string }>;
}

// PassOutcome is one job id read off an episode detail.
interface PassOutcome {
	id: string;
	status: string;
	error: string;
}

let pendingHandoff: ProcessingHandoff | null = null;

// depositProcessingHandoff holds one in-flight upload for the next screen.
// A second deposit replaces the first, which only happens when a newer take
// ends before the screen has read the previous one.
export function depositProcessingHandoff(handoff: ProcessingHandoff): void {
	pendingHandoff = handoff;
}

function takeProcessingHandoff(): ProcessingHandoff | null {
	const held = pendingHandoff;
	pendingHandoff = null;
	return held;
}

function idleStep(name: string, detail: string): ProcessingStep {
	return { name, detail, state: 'waiting', percent: 0 };
}

export function emptyProcessing(): ProcessingSnapshot {
	return {
		episode: '',
		upload: idleStep('Upload', 'Waiting for the take.'),
		transcription: idleStep('Transcription', 'Waiting.'),
		editorial: idleStep('Editorial pass', 'Waiting.'),
		draft: idleStep('Draft', 'Waiting.')
	};
}

// uploadDetail is the sentence under the upload bar while bytes are moving.
export function uploadDetail(stored: number, captured: number): string {
	if (captured <= 0) return 'Uploading the take.';
	return `Uploading the take. ${stored} of ${captured} bytes.`;
}

// resumedUploadDetail names a resumed upload from its stored bytes. A zero
// total means the receipts are missing, so the count would mislead.
export function resumedUploadDetail(count: number, totalBytes: number): string {
	if (totalBytes <= 0) return 'Both stems durable.';
	return `${count} stems, ${totalBytes} bytes durable.`;
};

// uploadPercent is the share of captured bytes the server has acknowledged.
export function uploadPercent(stored: number, captured: number): number {
	if (captured <= 0) return 0;
	return Math.min(100, Math.round((stored / captured) * 100));
}

// stoppedDetail names a pass that will not finish. A restart does not
// rerun a paid job, so the sentence says that instead of offering one.
export function stoppedDetail(pass: 'transcript' | 'editorial', status: string, errorMessage: string): string {
	if (status === 'interrupted') {
		return `The ${pass} pass stopped when the server restarted. It does not rerun on its own.`;
	}
	if (status === 'cancelled') return `The ${pass} pass was cancelled.`;
	if (errorMessage.length > 0) return `The ${pass} pass failed: ${errorMessage}.`;
	return `The ${pass} pass failed.`;
}

// readingFromJob folds one stream reading into a step. A terminal step
// stays put while the stream is still catching up, so a late frame cannot
// drag a finished pass backwards.
export function readingFromJob(
	step: ProcessingStep,
	status: string,
	connection: string,
	current: number | undefined,
	total: number | undefined,
	errorMessage: string,
	doneDetail: string,
	pass: 'transcript' | 'editorial'
): ProcessingStep {
	const terminal = status === 'done' || status === 'error' || status === 'cancelled' || status === 'interrupted';
	if ((step.state === 'done' || step.state === 'failed') && !terminal && connection !== 'failed') {
		return step;
	}
	if (status === 'done') return { ...step, state: 'done', detail: doneDetail, percent: 100 };
	if (status === 'error' || status === 'cancelled' || status === 'interrupted') {
		return { ...step, state: 'failed', detail: stoppedDetail(pass, status, errorMessage), percent: step.percent };
	}
	if (connection === 'failed') {
		return { ...step, state: 'failed', detail: 'The job stream failed.', percent: step.percent };
	}
	if (current !== undefined && total !== undefined && total > 0) {
		return {
			...step,
			state: 'running',
			detail: `${current} of ${total}.`,
			percent: Math.min(100, Math.round((current / total) * 100))
		};
	}
	return step;
}

// draftStep is ready only after the upload and both passes have finished.
// A missing pass stays waiting, so the draft cannot read done early.
export function draftStep(
	upload: ProcessingState,
	transcription: ProcessingState,
	editorial: ProcessingState
): ProcessingStep {
	if (upload === 'failed') {
		return idleFailed('The upload stopped, so the draft is not ready.');
	}
	if (transcription === 'failed' || editorial === 'failed') {
		return idleFailed('A pass stopped, so the draft is not ready.');
	}
	if (upload === 'done' && transcription === 'done' && editorial === 'done') {
		return { name: 'Draft', detail: 'The draft is ready.', state: 'done', percent: 100 };
	}
	return idleStep('Draft', 'Waiting.');
}

function idleFailed(detail: string): ProcessingStep {
	return { name: 'Draft', detail, state: 'failed', percent: 0 };
}

function outcomeOf(value: unknown): PassOutcome {
	if (typeof value !== 'object' || value === null || Array.isArray(value)) {
		return { id: '', status: '', error: '' };
	}
	const record = value as Record<string, unknown>;
	const id = record['job_id'];
	const status = record['status'];
	const error = record['error'];
	return {
		id: typeof id === 'string' ? id : '',
		status: typeof status === 'string' ? status : '',
		error: typeof error === 'string' ? error : ''
	};
}

// ProcessingController draws one finished take until the draft is ready.
// Query flags carry the handoff: the episode, the two job ids, the upload
// totals, and the mock flag that swaps both servers for doubles.
export class ProcessingController {
	private readonly onChange: (snapshot: ProcessingSnapshot) => void;
	private readonly mockMode: boolean;
	private transcriptJob: string;
	private editorialJob: string;
	private snapshot: ProcessingSnapshot;
	private transcriptStream: JobStream | null = null;
	private editorialStream: JobStream | null = null;
	private timer: number | null = null;
	private uploadTimer: number | null = null;
	private cleanups: Array<() => void> = [];
	private watched = new Set<string>();
	private handoff: ProcessingHandoff | null = null;
	private readingOutcomes = false;
	private alive = true;
	private readonly userMediaId: string;
	private readonly hostMediaId: string;
	private readonly userSampleRate: number;

	constructor(query: URLSearchParams, onChange: (snapshot: ProcessingSnapshot) => void) {
		this.onChange = onChange;
		this.snapshot = emptyProcessing();
		const params = Object.fromEntries(query.entries());
		this.snapshot.episode = params['episode'] ?? '';
		this.mockMode = params['mock'] === '1';
		this.transcriptJob = params['transcript'] ?? '';
		this.editorialJob = params['editorial'] ?? '';
		this.userMediaId = params['userMedia'] ?? '';
		this.hostMediaId = params['hostMedia'] ?? '';
		const parsedRate = Number(params['userRate'] ?? '');
		this.userSampleRate = Number.isInteger(parsedRate) && parsedRate > 0 ? parsedRate : 0;
		if (params['uploads'] === 'done') {
			const userBytes = Number(params['userBytes'] ?? '0');
			const hostBytes = Number(params['hostBytes'] ?? '0');
			this.snapshot.upload = {
				name: 'Upload',
				state: 'done',
				detail: `Both stems durable: ${userBytes} user bytes, ${hostBytes} host bytes.`,
				percent: 100
			};
		}
	}

	/** Follow the jobs named in the handoff and expose the steps for tests. */
	mount(): void {
		if (this.mockMode) this.installMockJobs();
		const handoff = takeProcessingHandoff();
		if (handoff !== null) {
			this.handoff = handoff;
			void this.followHandoff(handoff);
		} else if (this.snapshot.upload.state === 'waiting') {
			void this.resumeUploads().then(() => this.refresh());
		}
		if (this.transcriptJob !== '') this.watchJob(this.transcriptJob, 'transcript');
		if (this.editorialJob !== '') this.watchJob(this.editorialJob, 'editorial');
		this.emit();
		this.timer = window.setInterval(() => this.refresh(), 500);
		const target = window as unknown as Record<string, unknown>;
		target['__processing'] = {
			steps: () => this.expose()
		};
	}

	/** Stop following every job. */
	destroy(): void {
		this.alive = false;
		if (this.timer !== null) {
			window.clearInterval(this.timer);
			this.timer = null;
		}
		this.stopUploadTimer();
		for (const cleanup of this.cleanups) cleanup();
		this.cleanups = [];
		this.transcriptStream?.close();
		this.editorialStream?.close();
	}

	/** Post the draft move again after the stems are already stored. */
	retrySettle(): void {
		if (!this.alive || this.handoff === null) return;
		void this.followHandoff(this.handoff);
	}

	private expose(): unknown[] {
		return [
			{ ...this.snapshot.upload },
			{ ...this.snapshot.transcription },
			{ ...this.snapshot.editorial },
			{ ...this.snapshot.draft },
			this.snapshot.episode
		];
	}

	private emit(): void {
		this.onChange({
			episode: this.snapshot.episode,
			upload: { ...this.snapshot.upload },
			transcription: { ...this.snapshot.transcription },
			editorial: { ...this.snapshot.editorial },
			draft: { ...this.snapshot.draft }
		});
	}

	private watchJob(id: string, kind: 'transcript' | 'editorial'): void {
		if (id === '') return;
		if (kind === 'transcript') this.transcriptJob = id;
		else this.editorialJob = id;
		if (this.watched.has(id)) return;
		this.watched.add(id);
		const stream = new JobStream({
			url: `/api/jobs/${id}/events`,
			fetchState: () => readJobState<JobSnapshot>(id)
		});
		const current = kind === 'transcript' ? this.snapshot.transcription : this.snapshot.editorial;
		// Replace a waiting or failed step. A failed step would otherwise ignore
		// every frame until the job ends.
		if (current.state === 'waiting' || current.state === 'failed') {
			const next = { ...current, state: 'running' as const, detail: 'Following the job.' };
			if (kind === 'transcript') this.snapshot.transcription = next;
			else this.snapshot.editorial = next;
			this.snapshot.draft = draftStep(
				this.snapshot.upload.state,
				this.snapshot.transcription.state,
				this.snapshot.editorial.state
			);
		}
		if (kind === 'transcript') this.transcriptStream = stream;
		else this.editorialStream = stream;
		stream.attach((task) => {
			this.cleanups.push(task());
		});
		this.emit();
	}

	private async followHandoff(handoff: ProcessingHandoff): Promise<void> {
		this.handoff = handoff;
		const paint = () => {
			if (!this.alive || this.snapshot.upload.state === 'done' || this.snapshot.upload.state === 'failed') {
				return;
			}
			const progress = handoff.progress();
			this.snapshot.upload = {
				name: 'Upload',
				state: 'running',
				detail: uploadDetail(progress.stored, progress.captured),
				percent: uploadPercent(progress.stored, progress.captured)
			};
			this.emit();
		};
		paint();
		this.stopUploadTimer();
		this.uploadTimer = window.setInterval(paint, 200);
		try {
			const settled = await handoff.settle();
			if (!this.alive) return;
			this.stopUploadTimer();
			this.snapshot.upload = {
				name: 'Upload',
				state: 'done',
				detail: `Both stems durable: ${settled.userBytes} user bytes, ${settled.hostBytes} host bytes.`,
				percent: 100
			};
			if (settled.transcriptJob !== '') {
				// A failed transcript step stays on screen until a terminal frame,
				// because a non-terminal reading keeps it. Clear it before the new
				// job is watched.
				if (this.snapshot.transcription.state === 'failed') {
					this.snapshot.transcription = idleStep('Transcription', 'Waiting.');
					this.snapshot.draft = draftStep(
						this.snapshot.upload.state,
						this.snapshot.transcription.state,
						this.snapshot.editorial.state
					);
				}
				this.watchJob(settled.transcriptJob, 'transcript');
			}
			this.refresh();
			await this.readOutcomes();
		} catch (error) {
			if (!this.alive) return;
			this.stopUploadTimer();
			const detail = error instanceof Error ? error.message : 'The upload did not finish.';
			if (detail.includes('stem upload')) {
				this.snapshot.upload = {
					name: 'Upload',
					state: 'failed',
					detail,
					percent: this.snapshot.upload.percent
				};
			} else {
				if (this.snapshot.upload.state === 'running') {
					this.snapshot.upload = {
						name: 'Upload',
						state: 'done',
						detail: 'The stems are stored.',
						percent: 100
					};
				}
				this.snapshot.transcription = {
					name: 'Transcription',
					state: 'failed',
					detail,
					percent: 0
				};
			}
			this.snapshot.draft = draftStep(
				this.snapshot.upload.state,
				this.snapshot.transcription.state,
				this.snapshot.editorial.state
			);
			this.emit();
		}
	}

	private stopUploadTimer(): void {
		if (this.uploadTimer === null) return;
		window.clearInterval(this.uploadTimer);
		this.uploadTimer = null;
	}

	// pairFromResume names the stem pair a reload can post. A receipt covers
	// a session this page just finished. An id that was not resumed already
	// left the store, so that stem finished before the reload.
	private pairFromResume(
		receipts: ReadonlyArray<{ id: string; sizeBytes: number }>,
		resumedIds: readonly string[],
		failedIds: readonly string[]
	): { userId: string; hostId: string; userBytes: number; hostBytes: number; sizesKnown: boolean } | null {
		const userId = this.userMediaId;
		const hostId = this.hostMediaId;
		if (userId === '' || hostId === '' || userId === hostId) return null;
		if (this.userSampleRate <= 0 || this.snapshot.episode === '') return null;
		if (failedIds.includes(userId) || failedIds.includes(hostId)) return null;
		const userReceipt = receipts.find((item) => item.id === userId);
		const hostReceipt = receipts.find((item) => item.id === hostId);
		const userSeen = resumedIds.includes(userId);
		const hostSeen = resumedIds.includes(hostId);
		if (userSeen && userReceipt === undefined) return null;
		if (hostSeen && hostReceipt === undefined) return null;
		if (!userSeen && !hostSeen) return null;
		return {
			userId,
			hostId,
			userBytes: userReceipt?.sizeBytes ?? 0,
			hostBytes: hostReceipt?.sizeBytes ?? 0,
			sizesKnown: userReceipt !== undefined && hostReceipt !== undefined
		};
	}

	// pairFromAddress names the stem pair the address carries. A reload has
	// no uploader left, and the store is empty once both stems finished,
	// so the address is what still names the pair the draft move needs.
	private pairFromAddress(): { userId: string; hostId: string } | null {
		const userId = this.userMediaId;
		const hostId = this.hostMediaId;
		if (userId === '' || hostId === '' || userId === hostId) return null;
		if (this.userSampleRate <= 0 || this.snapshot.episode === '') return null;
		return { userId, hostId };
	}

	private async resumeUploads(): Promise<void> {
		this.snapshot.upload = {
			...this.snapshot.upload,
			state: 'running',
			detail: 'Completing the persisted upload.',
			percent: 0
		};
		this.emit();
		if (this.mockMode) {
			const server = new MockUploadServer('/api/uploads');
			const realFetch = window.fetch.bind(window);
			window.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
				const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
				if (url.includes('/api/uploads')) return server.handle(url, init);
				return realFetch(input, init);
			}) as typeof window.fetch;
			await rehydrateServerFromBrowser(server);
		}
		let total = 0;
		let count = 0;
		const receipts: Array<{ id: string; sizeBytes: number }> = [];
		const resumedIds: string[] = [];
		const failedIds: string[] = [];
		for (;;) {
			const resumed = await ChunkUploader.resume();
			if (resumed === undefined) break;
			const id = resumed.id ?? '';
			if (id !== '' && resumedIds.includes(id)) {
				failedIds.push(id);
				break;
			}
			if (id !== '') resumedIds.push(id);
			await resumed.finish();
			if (resumed.receipt !== undefined) {
				receipts.push({ id: resumed.receipt.id, sizeBytes: resumed.receipt.sizeBytes });
			} else if (id !== '') {
				failedIds.push(id);
			}
			total += resumed.receipt?.sizeBytes ?? 0;
			count += 1;
		}
		if (count === 0) {
			const address = this.pairFromAddress();
			if (address === null) {
				this.snapshot.upload = {
					...this.snapshot.upload,
					state: 'waiting',
					detail: 'No persisted upload waits.',
					percent: 0
				};
				this.emit();
				return;
			}
			// Both stems finished before the reload, so the store is empty.
			// Rebuild the finished upload from the address and post the pair
			// again. A repeat reports the standing outcome instead of
			// scheduling twice. A refusal marks transcription failed, which
			// is what offers the retry.
			this.snapshot.upload = {
				name: 'Upload',
				state: 'done',
				detail: 'Both stems durable.',
				percent: 100
			};
			this.emit();
			const handoff: ProcessingHandoff = {
				progress: () => ({ stored: 0, captured: 0 }),
				settle: async () => {
					const answer = await postStemsComplete(
						this.snapshot.episode,
						completionPair(address.userId, address.hostId, this.userSampleRate)
					);
					return { userBytes: 0, hostBytes: 0, transcriptJob: answer.jobId };
				}
			};
			await this.followHandoff(handoff);
			if (!this.alive || this.snapshot.upload.state !== 'done') return;
			if (this.snapshot.upload.detail.includes('0 user bytes')) {
				this.snapshot.upload = { ...this.snapshot.upload, detail: 'Both stems durable.' };
				this.emit();
			}
			return;
		}
		const pair = this.pairFromResume(receipts, resumedIds, failedIds);
		if (pair === null) {
			this.snapshot.upload = {
				...this.snapshot.upload,
				state: 'done',
				detail: resumedUploadDetail(count, total),
				percent: 100
			};
			this.emit();
			return;
		}
		this.snapshot.upload = {
			name: 'Upload',
			state: 'done',
			detail: pair.sizesKnown
				? `Both stems durable: ${pair.userBytes} user bytes, ${pair.hostBytes} host bytes.`
				: 'Both stems durable.',
			percent: 100
		};
		this.emit();
		const handoff: ProcessingHandoff = {
			progress: () => ({
				stored: pair.userBytes + pair.hostBytes,
				captured: pair.userBytes + pair.hostBytes
			}),
			settle: async () => {
				const answer = await postStemsComplete(
					this.snapshot.episode,
					completionPair(pair.userId, pair.hostId, this.userSampleRate)
				);
				return {
					userBytes: pair.userBytes,
					hostBytes: pair.hostBytes,
					transcriptJob: answer.jobId
				};
			}
		};
		await this.followHandoff(handoff);
		if (!this.alive || pair.sizesKnown || this.snapshot.upload.state !== 'done') return;
		this.snapshot.upload = { ...this.snapshot.upload, detail: 'Both stems durable.' };
		this.emit();
	}

	private installMockJobs(): void {
		const realFetch = window.fetch.bind(window);
		window.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
			const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
			const match = url.match(/\/api\/jobs\/([^/]+)(\/events)?$/);
			if (match !== null) {
				const id = match[1] ?? '';
				if (match[2] === '/events') {
					return sseResponse([
						progressFrame(id, 'running', 1, 2, 1),
						progressFrame(id, 'running', 2, 2, 2),
						statusFrame(id, 'done')
					]);
				}
				return new Response(
					JSON.stringify({ jobId: id, status: 'running', stage: 'running', current: 1, total: 2 }),
					{ status: 200, headers: { 'content-type': 'application/json' } }
				);
			}
			return realFetch(input, init);
		}) as typeof window.fetch;
	}

	private async readOutcomes(): Promise<void> {
		if (!this.alive || this.readingOutcomes) return;
		if (this.snapshot.episode === '') return;
		if (this.transcriptJob !== '' && this.editorialJob !== '') return;
		this.readingOutcomes = true;
		try {
			const response = await fetch(`/api/episodes/${encodeURIComponent(this.snapshot.episode)}`);
			let contentType = '';
			for (const [key, value] of response.headers.entries()) {
				if (key.toLowerCase() === 'content-type') contentType = value;
			}
			if (!response.ok || !contentType.toLowerCase().includes('application/json')) return;
			const body: unknown = await response.json();
			if (typeof body !== 'object' || body === null || Array.isArray(body)) return;
			const record = body as Record<string, unknown>;
			const transcript = outcomeOf(record['transcript_outcome']);
			const editorial = outcomeOf(record['editorial_outcome']);
			if (this.transcriptJob === '' && transcript.id !== '') {
				this.transcriptJob = transcript.id;
				this.watchJob(transcript.id, 'transcript');
				this.snapshot.transcription = readingFromJob(
					this.snapshot.transcription,
					transcript.status,
					'live',
					undefined,
					undefined,
					transcript.error,
					'Transcript ready.',
					'transcript'
				);
			}
			if (this.editorialJob === '' && editorial.id !== '') {
				this.editorialJob = editorial.id;
				this.watchJob(editorial.id, 'editorial');
				this.snapshot.editorial = readingFromJob(
					this.snapshot.editorial,
					editorial.status,
					'live',
					undefined,
					undefined,
					editorial.error,
					'Proposals ready.',
					'editorial'
				);
			}
			this.snapshot.draft = draftStep(
				this.snapshot.upload.state,
				this.snapshot.transcription.state,
				this.snapshot.editorial.state
			);
			this.emit();
		} catch {
			// A missing detail leaves the steps waiting. The next tick tries again.
		} finally {
			this.readingOutcomes = false;
		}
	}

	private refresh(): void {
		if (!this.alive) return;
		if (this.transcriptStream !== null) {
			this.snapshot.transcription = readingFromJob(
				this.snapshot.transcription,
				this.transcriptStream.status,
				this.transcriptStream.connection,
				this.transcriptStream.current,
				this.transcriptStream.total,
				this.transcriptStream.error?.message ?? '',
				'Transcript ready.',
				'transcript'
			);
		}
		if (this.editorialStream !== null) {
			this.snapshot.editorial = readingFromJob(
				this.snapshot.editorial,
				this.editorialStream.status,
				this.editorialStream.connection,
				this.editorialStream.current,
				this.editorialStream.total,
				this.editorialStream.error?.message ?? '',
				'Proposals ready.',
				'editorial'
			);
		}
		this.snapshot.draft = draftStep(
			this.snapshot.upload.state,
			this.snapshot.transcription.state,
			this.snapshot.editorial.state
		);
		this.emit();
		if (this.transcriptJob === '' || this.editorialJob === '') void this.readOutcomes();
	}
}
