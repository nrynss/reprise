// The one call the record route makes after both stem uploads finish. The
// uploader stays as wired. This module posts the stored media pair with its
// sample rates and turns the answer into words the page shows. It throws a
// loud error on failure, so the page never stalls silent.

import { api, ApiError } from '@nrynss/chaaya/api';
import { SOCKET_RATE } from '$lib/voice/pcm';

// StemPair names the two stored blobs one completion links. Both blobs arrive
// through the chunked upload first, so the completion only links them.
export interface StemPair {
	userMediaId: string;
	hostMediaId: string;
	userSampleRate: number;
	hostSampleRate: number;
}

// StemsCompleteAnswer mirrors the completion answer. Unknown fields pass
// through unread, so a newer answer still parses here.
export interface StemsCompleteAnswer {
	episodeId: string;
	moved: boolean;
	scheduled: boolean;
	jobId: string;
	state: string;
}

// CompletionStatus names what the page shows while the move runs.
export type CompletionStatus = 'posting' | 'ready' | 'failed';

// CompletionView carries one render of the completion outcome. A failure
// offers a retry, because the stored stems survive it.
export interface CompletionView {
	status: CompletionStatus;
	text: string;
	canRetry: boolean;
}

// completionPair builds the pair the completion posts. The host stem plays at
// the provider rate, so the host rate rides on that constant.
export function completionPair(
	userMediaId: string,
	hostMediaId: string,
	userSampleRate: number
): StemPair {
	return { userMediaId, hostMediaId, userSampleRate, hostSampleRate: SOCKET_RATE };
}

// postStemsComplete posts the media pair for one episode and reads the
// answer. It throws when the pair is incomplete and when the server refuses,
// so the page surfaces the throw instead of stalling.
export async function postStemsComplete(
	episodeId: string,
	pair: StemPair
): Promise<StemsCompleteAnswer> {
	if (episodeId.length === 0) throw new Error('a completion names its episode');
	if (pair.userMediaId.length === 0 || pair.hostMediaId.length === 0) {
		throw new Error('a completion names both stem blobs');
	}
	if (!Number.isInteger(pair.userSampleRate) || pair.userSampleRate <= 0) {
		throw new Error('a completion names a positive user sample rate');
	}
	if (!Number.isInteger(pair.hostSampleRate) || pair.hostSampleRate <= 0) {
		throw new Error('a completion names a positive host sample rate');
	}
	const value = await api<unknown>(`/api/episodes/${encodeURIComponent(episodeId)}/stems/complete`, {
		method: 'POST',
		headers: { 'content-type': 'application/json' },
		body: JSON.stringify({
			user_media_id: pair.userMediaId,
			host_media_id: pair.hostMediaId,
			user_sample_rate: pair.userSampleRate,
			host_sample_rate: pair.hostSampleRate
		})
	});
	return parseCompletionAnswer(value);
}

// parseCompletionAnswer reads the completion answer from the wire. It keeps
// the episode, the two flags, the job, and the state, and it ignores the
// rest, so a newer answer still parses.
export function parseCompletionAnswer(value: unknown): StemsCompleteAnswer {
	if (typeof value !== 'object' || value === null || Array.isArray(value)) {
		throw new Error('the completion answer holds no object');
	}
	const record = value as Record<string, unknown>;
	const episodeId = record['episode_id'];
	const moved = record['moved'];
	const scheduled = record['scheduled'];
	const jobId = record['job_id'];
	const state = record['state'];
	if (typeof episodeId !== 'string' || episodeId.length === 0) {
		throw new Error('the completion answer names no episode');
	}
	if (typeof moved !== 'boolean') throw new Error('the completion answer names no move flag');
	if (typeof scheduled !== 'boolean') {
		throw new Error('the completion answer names no schedule flag');
	}
	if (typeof state !== 'string' || state.length === 0) {
		throw new Error('the completion answer names no state');
	}
	return {
		episodeId,
		moved,
		scheduled,
		jobId: typeof jobId === 'string' ? jobId : '',
		state
	};
}

// CompletionSink receives every completion render. The page shows the
// latest view and nothing else.
export type CompletionSink = (view: CompletionView) => void;

// CompletionDriver runs the completion and remembers the last request for
// its retry. The page builds one and renders every view it sends.
export interface CompletionDriver {
	run(episodeId: string, pair: StemPair): Promise<void>;
	retry(): Promise<void>;
}

// createCompletionDriver builds the driver around one render callback. A
// retry reposts the last pair, so it never uploads twice.
export function createCompletionDriver(notify: CompletionSink): CompletionDriver {
	let lastEpisode: string | null = null;
	let lastPair: StemPair | null = null;
	let canRetryNow = false;

	async function run(episodeId: string, pair: StemPair): Promise<void> {
		lastEpisode = episodeId;
		lastPair = pair;
		canRetryNow = false;
		notify({ status: 'posting', text: 'Moving the take to draft.', canRetry: false });
		try {
			const answer = await postStemsComplete(episodeId, pair);
			notify({ status: 'ready', text: describeCompletion(answer), canRetry: false });
		} catch (error) {
			canRetryNow = true;
			notify({ status: 'failed', text: describeCompletionFailure(error), canRetry: true });
		}
	}

	function retry(): Promise<void> {
		if (lastEpisode === null || lastPair === null || !canRetryNow) return Promise.resolve();
		canRetryNow = false;
		return run(lastEpisode, lastPair);
	}

	return { run, retry };
}

// StemsHarness supplies the mock take stem ids and rates. It mirrors the
// fields the mock harness exposes, so the mock handle reads ids off it.
export interface StemsHarness {
	userUploadId(): string | undefined;
	hostUploadId(): string | undefined;
	rate(): number;
}

// exposeStemsMock installs the mock window handle the proofs drive. It posts
// the harness pair for the fixed mock episode. Production never installs it.
export function exposeStemsMock(
	driver: CompletionDriver,
	readHarness: () => StemsHarness | null
): void {
	const target = window as unknown as Record<string, unknown>;
	target['__stems'] = {
		complete: () => {
			const harness = readHarness();
			if (harness === null) throw new Error('the mock take has no harness yet');
			const userId = harness.userUploadId();
			const hostId = harness.hostUploadId();
			if (userId === undefined || hostId === undefined) {
				throw new Error('the mock take opened no uploads yet');
			}
			// The mock take opens a fixed episode, so the mock handle names
			// it. Production learns the episode from its own session.
			return driver.run('mock-episode', completionPair(userId, hostId, harness.rate()));
		},
		retry: () => driver.retry()
	};
}

// describeCompletion turns one answer into words the page shows. A first
// completion reports the move and the job. A repeat reports the standing
// outcome instead of claiming fresh work.
export function describeCompletion(answer: StemsCompleteAnswer): string {
	if (answer.moved && answer.scheduled) {
		if (answer.jobId.length > 0) return `Draft ready. Transcript job ${answer.jobId} runs now.`;
		return 'Draft ready. The transcript job runs now.';
	}
	if (answer.moved) return `Draft ready in state ${answer.state}. No new edit job started.`;
	if (answer.jobId.length > 0) {
		return `Already a draft. Standing job ${answer.jobId} holds the outcome.`;
	}
	return `Already a draft in state ${answer.state}. No edit job stands.`;
}

// describeCompletionFailure turns one throw into loud words with a retry.
// Every branch keeps the stored stems safe, so a retry never uploads twice.
export function describeCompletionFailure(error: unknown): string {
	if (error instanceof ApiError) {
		if (error.code === 'pipeline_busy') {
			if (error.retryAfterSeconds !== undefined) {
				return `The edit queue is full. Wait ${error.retryAfterSeconds} seconds, then press retry. Your stems stay stored.`;
			}
			return 'The edit queue is full. Wait a little, then press retry. Your stems stay stored.';
		}
		if (error.code === 'stems_not_found') {
			return 'The server finds no stored stems under these ids. Your uploads stay stored. Press retry.';
		}
		return `The draft move failed (${error.code}). Your stems stay stored. Press retry.`;
	}
	if (error instanceof Error) {
		return `The draft move failed. ${error.message} Your stems stay stored. Press retry.`;
	}
	return 'The draft move failed. Your stems stay stored. Press retry.';
}
