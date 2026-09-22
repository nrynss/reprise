// Unit pins for the guarded take calls. Every case runs against a stub fetch
// with fixed answers, so no proof below waits on a real duration.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '@nrynss/chaaya/api';
import {
	closeSession,
	describeSessionEndFailure,
	describeUploadFailure,
	mintSession,
	readJobState
} from './session-calls';

const SESSION_BODY = {
	session_id: 's1',
	episode_id: 'e1',
	token: 'tok',
	expires_in_seconds: 60,
	max_session_duration_seconds: 1200,
	config: { system_prompt: 'prompt', greeting: 'hello', keyterms: ['the loft'] }
};

function jsonResponse(status: number, value: unknown, headers: Record<string, string> = {}): Response {
	return new Response(JSON.stringify(value), {
		status,
		headers: { 'content-type': 'application/json', ...headers }
	});
}

function htmlResponse(status: number, body: string): Response {
	return new Response(body, {
		status,
		headers: { 'content-type': 'text/html; charset=utf-8' }
	});
}

function stubFetchSequence(handlers: Array<() => Response>): ReturnType<typeof vi.fn> {
	const queue = [...handlers];
	const calls: string[] = [];
	const stub = vi.fn(async (input: unknown) => {
		calls.push(typeof input === 'string' ? input : '(request)');
		const next = queue.shift();
		if (next === undefined) throw new Error('the stub ran out of answers');
		return next();
	});
	vi.stubGlobal('fetch', stub);
	return stub;
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('mintSession', () => {
	it('passes a JSON answer through to the session shape', async () => {
		stubFetchSequence([() => jsonResponse(200, SESSION_BODY)]);
		const session = await mintSession();
		expect(session.session_id).toBe('s1');
		expect(session.episode_id).toBe('e1');
		expect(session.token).toBe('tok');
	});

	it('names the call when a challenge page answers and never retries the mint', async () => {
		const stub = stubFetchSequence([() => htmlResponse(200, '<html><body>challenge</body></html>')]);
		const failure = await mintSession().then(
			() => null,
			(error: unknown) => error
		);
		expect(failure).toBeInstanceOf(Error);
		expect((failure as Error).message).toContain('session mint');
		expect((failure as Error).message).toContain('text/html');
		expect(stub).toHaveBeenCalledTimes(1);
	});

	it('throws once on a dropped mint with no second reservation', async () => {
		const stub = stubFetchSequence([
			() => {
				throw new TypeError('the network dropped');
			}
		]);
		const failure = await mintSession().then(
			() => null,
			(error: unknown) => error
		);
		expect(failure).toBeInstanceOf(ApiError);
		expect((failure as ApiError).code).toBe('network');
		expect(stub).toHaveBeenCalledTimes(1);
	});
});

function closeBody(stub: ReturnType<typeof vi.fn>, index: number): string {
	const call = stub.mock.calls[index] as unknown[] | undefined;
	const init = call?.[1] as RequestInit | undefined;
	return typeof init?.body === 'string' ? init.body : '';
}

describe('closeSession', () => {
	it('resolves when the close record lands', async () => {
		const stub = stubFetchSequence([() => jsonResponse(200, { ok: true })]);
		await expect(closeSession('s1', 'prov-9')).resolves.toBeUndefined();
		expect(closeBody(stub, 0)).toBe(JSON.stringify({ provider_session_id: 'prov-9' }));
	});

	it('retries once after a network drop and sends the same provider id', async () => {
		const stub = stubFetchSequence([
			() => {
				throw new TypeError('the network dropped');
			},
			() => jsonResponse(200, { ok: true })
		]);
		await expect(closeSession('s1', 'prov-9')).resolves.toBeUndefined();
		expect(stub).toHaveBeenCalledTimes(2);
		const want = JSON.stringify({ provider_session_id: 'prov-9' });
		expect(closeBody(stub, 0)).toBe(want);
		expect(closeBody(stub, 1)).toBe(want);
		expect(closeBody(stub, 0)).not.toBe(JSON.stringify({ provider_session_id: '' }));
	});

	it('names the call when a challenge page answers, with no retry loop', async () => {
		const stub = stubFetchSequence([() => htmlResponse(200, '<html><body>challenge</body></html>')]);
		const failure = await closeSession('s1', 'prov-9').then(
			() => null,
			(error: unknown) => error
		);
		expect(failure).toBeInstanceOf(Error);
		expect((failure as Error).message).toContain('session end');
		expect(stub).toHaveBeenCalledTimes(1);
	});

	it('keeps the shared refusal shape on a JSON error', async () => {
		const stub = stubFetchSequence([
			() => jsonResponse(429, { error: { code: 'busy', message: 'slow down' } }, { 'Retry-After': '5' })
		]);
		const failure = await closeSession('s1', 'prov-9').then(
			() => null,
			(error: unknown) => error
		);
		expect(failure).toBeInstanceOf(ApiError);
		expect((failure as ApiError).code).toBe('busy');
		expect(stub).toHaveBeenCalledTimes(1);
	});
});

describe('readJobState', () => {
	it('passes a JSON row through untouched', async () => {
		stubFetchSequence([() => jsonResponse(200, { jobId: 'j1', status: 'running' })]);
		const row = await readJobState<{ jobId: string }>('j1');
		expect(row.jobId).toBe('j1');
	});

	it('names the poll when a challenge page answers instead of landing empty', async () => {
		stubFetchSequence([() => htmlResponse(200, '<html><body>challenge</body></html>')]);
		const failure = await readJobState('j1').then(
			() => null,
			(error: unknown) => error
		);
		expect(failure).toBeInstanceOf(Error);
		expect((failure as Error).message).toContain('job poll');
	});
});

describe('describeUploadFailure', () => {
	it('reads null when both uploads stream', () => {
		expect(describeUploadFailure({ state: 'streaming' }, { state: 'done' })).toBeNull();
		expect(describeUploadFailure(null, null)).toBeNull();
	});

	it('names the failed side with its code', () => {
		expect(
			describeUploadFailure({ state: 'failed', error: { code: 'invalid_response' } }, { state: 'done' })
		).toBe('The user stem upload failed (invalid_response).');
		expect(describeUploadFailure({ state: 'done' }, { state: 'failed' })).toContain('host stem upload');
	});

	it('names a missing code instead of crashing', () => {
		expect(describeUploadFailure({ state: 'failed' }, null)).toContain('unknown');
	});
});

describe('describeSessionEndFailure', () => {
	it('keeps a guard message that already names the call to one mention', () => {
		const text = describeSessionEndFailure(
			new Error('The session end answer was not JSON. The edge answered text/html.')
		);
		expect(text).toContain('session end');
		expect(text.match(/session end/g) ?? []).toHaveLength(1);
		expect(text).toContain('retry');
	});

	it('wraps a foreign throw with the call name once', () => {
		const text = describeSessionEndFailure(new TypeError('the network dropped'));
		expect(text).toContain('session end');
		expect(text.match(/session end/g) ?? []).toHaveLength(1);
	});
});
