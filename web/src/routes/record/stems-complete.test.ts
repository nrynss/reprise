// Unit pins for the stem completion caller. Every case runs against a stub
// fetch with fixed answers, so no proof below waits on a real duration.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '@nrynss/chaaya/api';
import { SOCKET_RATE } from '$lib/voice/pcm';
import {
	completionPair,
	createCompletionDriver,
	describeCompletion,
	describeCompletionFailure,
	parseCompletionAnswer,
	postStemsComplete,
	type CompletionView,
	type StemsCompleteAnswer
} from './stems-complete';

function jsonResponse(status: number, value: unknown, headers: Record<string, string> = {}): Response {
	return new Response(JSON.stringify(value), {
		status,
		headers: { 'content-type': 'application/json', ...headers }
	});
}

function stubFetch(handler: (url: string, init?: RequestInit) => Response): void {
	vi.stubGlobal(
		'fetch',
		vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
			const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
			return handler(url, init);
		})
	);
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('completionPair', () => {
	it('rides the host stem on the provider rate', () => {
		expect(completionPair('user-1', 'host-1', 48000)).toEqual({
			userMediaId: 'user-1',
			hostMediaId: 'host-1',
			userSampleRate: 48000,
			hostSampleRate: SOCKET_RATE
		});
		expect(SOCKET_RATE).toBe(24000);
	});
});

describe('postStemsComplete', () => {
	it('posts the media pair with sample rates to the episode route', async () => {
		const seen: Array<{ url: string; init?: RequestInit }> = [];
		stubFetch((url, init) => {
			seen.push({ url, init });
			return jsonResponse(200, {
				episode_id: 'ep-1',
				moved: true,
				scheduled: true,
				job_id: 'tj-1',
				state: 'draft'
			});
		});
		const answer = await postStemsComplete('ep-1', completionPair('user-1', 'host-1', 48000));
		expect(answer).toEqual({
			episodeId: 'ep-1',
			moved: true,
			scheduled: true,
			jobId: 'tj-1',
			state: 'draft'
		});
		expect(seen.length).toBe(1);
		expect(seen[0].url).toBe('/api/episodes/ep-1/stems/complete');
		expect(seen[0].init?.method).toBe('POST');
		expect(seen[0].init?.headers).toEqual({ 'content-type': 'application/json' });
		expect(JSON.parse(String(seen[0].init?.body))).toEqual({
			user_media_id: 'user-1',
			host_media_id: 'host-1',
			user_sample_rate: 48000,
			host_sample_rate: 24000
		});
	});

	it('reads a repeat answer with no fresh job', async () => {
		stubFetch(() =>
			jsonResponse(200, {
				episode_id: 'ep-1',
				moved: false,
				scheduled: false,
				job_id: 'tj-1',
				state: 'draft'
			})
		);
		const answer = await postStemsComplete('ep-1', completionPair('user-1', 'host-1', 48000));
		expect(answer.moved).toBe(false);
		expect(answer.scheduled).toBe(false);
		expect(answer.jobId).toBe('tj-1');
	});

	it('throws before the network when a stem id is missing', async () => {
		const fetchMock = vi.fn(async () => jsonResponse(200, {}));
		vi.stubGlobal('fetch', fetchMock);
		await expect(postStemsComplete('ep-1', completionPair('', 'host-1', 48000))).rejects.toThrow(
			'a completion names both stem blobs'
		);
		expect(fetchMock).not.toHaveBeenCalled();
	});

	it('throws before the network when a rate is not positive', async () => {
		const fetchMock = vi.fn(async () => jsonResponse(200, {}));
		vi.stubGlobal('fetch', fetchMock);
		await expect(postStemsComplete('ep-1', completionPair('user-1', 'host-1', 0))).rejects.toThrow(
			'a completion names a positive user sample rate'
		);
		expect(fetchMock).not.toHaveBeenCalled();
	});

	it('surfaces the server refusal code on failure', async () => {
		stubFetch(() =>
			jsonResponse(429, { error: { code: 'pipeline_busy', message: 'the edit queue is full' } }, {
				'Retry-After': '10'
			})
		);
		const failure = await postStemsComplete('ep-1', completionPair('user-1', 'host-1', 48000)).then(
			() => null,
			(error: unknown) => error
		);
		expect(failure).toBeInstanceOf(ApiError);
		expect((failure as ApiError).code).toBe('pipeline_busy');
		expect((failure as ApiError).retryAfterSeconds).toBe(10);
	});
});

describe('parseCompletionAnswer', () => {
	it('throws when the answer holds no object', () => {
		expect(() => parseCompletionAnswer(null)).toThrow('the completion answer holds no object');
		expect(() => parseCompletionAnswer([])).toThrow('the completion answer holds no object');
	});

	it('throws when a flag is missing', () => {
		expect(() =>
			parseCompletionAnswer({ episode_id: 'ep-1', moved: true, scheduled: true, state: 'draft' })
		).not.toThrow();
		expect(() =>
			parseCompletionAnswer({ episode_id: 'ep-1', moved: 'yes', scheduled: true, state: 'draft' })
		).toThrow('the completion answer names no move flag');
	});

	it('reads an empty job id as no fresh job', () => {
		const answer = parseCompletionAnswer({
			episode_id: 'ep-1',
			moved: false,
			scheduled: false,
			state: 'draft'
		});
		expect(answer.jobId).toBe('');
	});
});

describe('describeCompletion', () => {
	it('reports the move and the fresh job', () => {
		const answer: StemsCompleteAnswer = {
			episodeId: 'ep-1',
			moved: true,
			scheduled: true,
			jobId: 'tj-1',
			state: 'draft'
		};
		expect(describeCompletion(answer)).toBe('Draft ready. Transcript job tj-1 runs now.');
	});

	it('reports the standing job on a repeat', () => {
		const answer: StemsCompleteAnswer = {
			episodeId: 'ep-1',
			moved: false,
			scheduled: false,
			jobId: 'tj-1',
			state: 'draft'
		};
		expect(describeCompletion(answer)).toBe('Already a draft. Standing job tj-1 holds the outcome.');
	});

	it('reports the standing state when no job stands', () => {
		const answer: StemsCompleteAnswer = {
			episodeId: 'ep-1',
			moved: false,
			scheduled: false,
			jobId: '',
			state: 'draft'
		};
		expect(describeCompletion(answer)).toBe('Already a draft in state draft. No edit job stands.');
	});
});

describe('describeCompletionFailure', () => {	it('names the queue wait with its hint', () => {
		const error = new ApiError('the edit queue is full', 'pipeline_busy', 429, {}, 10);
		expect(describeCompletionFailure(error)).toContain('Wait 10 seconds');
		expect(describeCompletionFailure(error)).toContain('retry');
	});

	it('names the refusal code on other server errors', () => {
		const error = new ApiError('both stems name stored audio', 'stems_not_found', 404);
		expect(describeCompletionFailure(error)).toContain('stems');
		expect(describeCompletionFailure(error)).toContain('retry');
	});

	it('keeps a plain throw loud with a retry', () => {
		expect(describeCompletionFailure(new Error('the network dropped'))).toContain('retry');
	});
});

describe('createCompletionDriver', () => {
	it('announces posting, then the draft move', async () => {
		stubFetch(() =>
			jsonResponse(200, {
				episode_id: 'ep-1',
				moved: true,
				scheduled: true,
				job_id: 'tj-1',
				state: 'draft'
			})
		);
		const seen: CompletionView[] = [];
		const driver = createCompletionDriver((view) => {
			seen.push(view);
		});
		await driver.run('ep-1', completionPair('user-1', 'host-1', 48000));
		expect(seen.length).toBe(2);
		expect(seen[0]).toEqual({ status: 'posting', text: 'Moving the take to draft.', canRetry: false });
		expect(seen[1]).toEqual({
			status: 'ready',
			text: 'Draft ready. Transcript job tj-1 runs now.',
			canRetry: false
		});
	});

	it('reposts the last pair on retry after a failure', async () => {
		let posts = 0;
		stubFetch(() => {
			posts += 1;
			if (posts === 1) {
				return jsonResponse(500, { error: { code: 'overloaded', message: 'the server is busy' } });
			}
			return jsonResponse(200, {
				episode_id: 'ep-1',
				moved: true,
				scheduled: true,
				job_id: 'tj-1',
				state: 'draft'
			});
		});
		const seen: CompletionView[] = [];
		const driver = createCompletionDriver((view) => {
			seen.push(view);
		});
		await driver.run('ep-1', completionPair('user-1', 'host-1', 48000));
		expect(seen[seen.length - 1].status).toBe('failed');
		expect(seen[seen.length - 1].canRetry).toBe(true);
		await driver.retry();
		expect(posts).toBe(2);
		expect(seen[seen.length - 1].status).toBe('ready');
		expect(seen[seen.length - 1].text).toBe('Draft ready. Transcript job tj-1 runs now.');
	});

	it('ignores a retry with no run behind it', async () => {
		const fetchMock = vi.fn(async () => jsonResponse(200, {}));
		vi.stubGlobal('fetch', fetchMock);
		const seen: CompletionView[] = [];
		const driver = createCompletionDriver((view) => {
			seen.push(view);
		});
		await driver.retry();
		expect(fetchMock).not.toHaveBeenCalled();
		expect(seen.length).toBe(0);
	});
});

describe('challenge page answers', () => {
	function htmlResponse(status: number): Response {
		return new Response('<html><body>challenge</body></html>', {
			status,
			headers: { 'content-type': 'text/html; charset=utf-8' }
		});
	}

	it('names the failed call instead of reading the page as empty', async () => {
		stubFetch(() => htmlResponse(200));
		const failure = await postStemsComplete('ep-1', completionPair('user-1', 'host-1', 48000)).then(
			() => null,
			(error: unknown) => error
		);
		expect(failure).toBeInstanceOf(Error);
		expect((failure as Error).message).toContain('draft move');
		expect((failure as Error).message).toContain('text/html');
	});

	it('refuses loudly with one request and a working retry', async () => {
		let posts = 0;
		stubFetch(() => {
			posts += 1;
			if (posts === 1) return htmlResponse(200);
			return jsonResponse(200, {
				episode_id: 'ep-1',
				moved: true,
				scheduled: true,
				job_id: 'tj-1',
				state: 'draft'
			});
		});
		const seen: CompletionView[] = [];
		const driver = createCompletionDriver((view) => {
			seen.push(view);
		});
		await driver.run('ep-1', completionPair('user-1', 'host-1', 48000));
		expect(posts).toBe(1);
		expect(seen[seen.length - 1].status).toBe('failed');
		expect(seen[seen.length - 1].canRetry).toBe(true);
		expect(seen[seen.length - 1].text).toContain('draft move');
		expect(seen[seen.length - 1].text).toContain('retry');
		await driver.retry();
		expect(posts).toBe(2);
		expect(seen[seen.length - 1].status).toBe('ready');
	});
});
