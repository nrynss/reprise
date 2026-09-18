// The session screen that follows a take. It draws the upload and the two
// post jobs through the same job follower the gallery card reads, so the
// screen and the card never drift into two mechanisms. Like the take, it
// reports through snapshots the page renders.

import { api } from '@nrynss/chaaya/api';
import { ChunkUploader } from '@nrynss/chaaya/audio';
import { JobStream, type JobSnapshot } from '@nrynss/chaaya/job';
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
}

// ProcessingSnapshot carries the four steps and the episode they belong to.
export interface ProcessingSnapshot {
	episode: string;
	upload: ProcessingStep;
	transcription: ProcessingStep;
	editorial: ProcessingStep;
	draft: ProcessingStep;
}

function idleStep(name: string, detail: string): ProcessingStep {
	return { name, detail, state: 'waiting' };
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

// ProcessingController draws one finished take until the draft is ready.
// Query flags carry the handoff: the episode, the two job ids, the upload
// totals, and the mock flag that swaps both servers for doubles.
export class ProcessingController {
	private readonly onChange: (snapshot: ProcessingSnapshot) => void;
	private readonly mockMode: boolean;
	private readonly transcriptJob: string;
	private readonly editorialJob: string;
	private snapshot: ProcessingSnapshot;
	private transcriptStream: JobStream | null = null;
	private editorialStream: JobStream | null = null;
	private timer: number | null = null;

	constructor(query: URLSearchParams, onChange: (snapshot: ProcessingSnapshot) => void) {
		this.onChange = onChange;
		this.snapshot = emptyProcessing();
		this.snapshot.episode = query.get('episode') ?? '';
		this.mockMode = query.get('mock') === '1';
		this.transcriptJob = query.get('transcript') ?? '';
		this.editorialJob = query.get('editorial') ?? '';
		if (query.get('uploads') === 'done') {
			const userBytes = Number(query.get('userBytes') ?? '0');
			const hostBytes = Number(query.get('hostBytes') ?? '0');
			this.snapshot.upload = {
				name: 'Upload',
				state: 'done',
				detail: `Both stems durable: ${userBytes} user bytes, ${hostBytes} host bytes.`
			};
		}
	}

	/** Follow the jobs named in the handoff and expose the steps for tests. */
	mount(): void {
		if (this.mockMode && this.transcriptJob !== '' && this.editorialJob !== '') {
			this.installMockJobs();
		}
		if (this.snapshot.upload.state === 'waiting') {
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
		if (this.timer !== null) {
			window.clearInterval(this.timer);
			this.timer = null;
		}
		this.transcriptStream?.close();
		this.editorialStream?.close();
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
		const stream = new JobStream({
			url: `/api/jobs/${id}/events`,
			fetchState: () => api<JobSnapshot>(`/api/jobs/${id}`)
		});
		if (kind === 'transcript') {
			this.transcriptStream = stream;
			this.snapshot.transcription = {
				...this.snapshot.transcription,
				state: 'running',
				detail: 'Following the job.'
			};
		} else {
			this.editorialStream = stream;
			this.snapshot.editorial = {
				...this.snapshot.editorial,
				state: 'running',
				detail: 'Following the job.'
			};
		}
		stream.attach();
		this.emit();
	}

	private async resumeUploads(): Promise<void> {
		this.snapshot.upload = {
			...this.snapshot.upload,
			state: 'running',
			detail: 'Completing the persisted upload.'
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
		for (;;) {
			const resumed = await ChunkUploader.resume();
			if (resumed === undefined) break;
			await resumed.finish();
			total += resumed.receipt?.sizeBytes ?? 0;
			count += 1;
		}
		if (count === 0) {
			this.snapshot.upload = {
				...this.snapshot.upload,
				state: 'waiting',
				detail: 'No persisted upload waits.'
			};
		} else {
			this.snapshot.upload = {
				...this.snapshot.upload,
				state: 'done',
				detail: `${count} stems, ${total} bytes durable.`
			};
		}
		this.emit();
	}

	private installMockJobs(): void {
		const realFetch = window.fetch.bind(window);
		window.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
			const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
			const match = url.match(/\/api\/jobs\/([^/]+)(\/events)?$/);
			if (match !== null) {
				const id = match[1];
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

	private refresh(): void {
		const next = this.snapshot;
		if (this.transcriptStream !== null) {
			if (this.transcriptStream.status === 'done') {
				next.transcription = { ...next.transcription, state: 'done', detail: 'Transcript ready.' };
			} else if (this.transcriptStream.connection === 'failed') {
				next.transcription = {
					...next.transcription,
					state: 'failed',
					detail: 'The job stream failed.'
				};
			} else if (
				this.transcriptStream.current !== undefined &&
				this.transcriptStream.total !== undefined
			) {
				next.transcription = {
					...next.transcription,
					state: 'running',
					detail: `${this.transcriptStream.current} of ${this.transcriptStream.total}.`
				};
			}
		}
		if (this.editorialStream !== null) {
			if (this.editorialStream.status === 'done') {
				next.editorial = { ...next.editorial, state: 'done', detail: 'Proposals ready.' };
			} else if (this.editorialStream.connection === 'failed') {
				next.editorial = { ...next.editorial, state: 'failed', detail: 'The job stream failed.' };
			}
		}
		if (
			next.upload.state === 'done' &&
			(this.transcriptJob === '' || next.transcription.state === 'done') &&
			(this.editorialJob === '' || next.editorial.state === 'done')
		) {
			next.draft = { ...next.draft, state: 'done', detail: 'The draft is ready.' };
		}
		this.emit();
	}
}
