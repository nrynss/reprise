// Unit pins for the record route JSON guard. Every case runs against a stub
// fetch with fixed answers, so no proof below waits on a real duration.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '@nrynss/chaaya/api';
import { isJsonContentType, readJsonAnswer } from './api-guard';

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

function stubFetch(handler: () => Response | Promise<Response>): void {
	vi.stubGlobal(
		'fetch',
		vi.fn(async () => handler())
	);
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('isJsonContentType', () => {
	it('accepts JSON with or without a charset suffix', () => {
		expect(isJsonContentType('application/json')).toBe(true);
		expect(isJsonContentType('application/json; charset=utf-8')).toBe(true);
		expect(isJsonContentType('Application/JSON')).toBe(true);
	});

	it('refuses challenge pages and missing headers', () => {
		expect(isJsonContentType('text/html; charset=utf-8')).toBe(false);
		expect(isJsonContentType('text/html')).toBe(false);
		expect(isJsonContentType('')).toBe(false);
		expect(isJsonContentType(null)).toBe(false);
	});
});

describe('readJsonAnswer', () => {
	it('passes a JSON answer through untouched', async () => {
		stubFetch(() => jsonResponse(200, { episode_id: 'ep-1', moved: true }));
		const value = await readJsonAnswer('draft move', '/api/episodes/ep-1/stems/complete', {
			method: 'POST'
		});
		expect(value).toEqual({ episode_id: 'ep-1', moved: true });
	});

	it('names the call when a challenge page answers with success status', async () => {
		stubFetch(() => htmlResponse(200, '<html><body>challenge</body></html>'));
		const failure = await readJsonAnswer('draft move', '/api/x', { method: 'POST' }).then(
			() => null,
			(error: unknown) => error
		);
		expect(failure).toBeInstanceOf(Error);
		expect(failure).not.toBeInstanceOf(ApiError);
		expect((failure as Error).message).toContain('draft move');
		expect((failure as Error).message).toContain('text/html');
	});

	it('names the call when a challenge page answers with refusal status', async () => {
		stubFetch(() => htmlResponse(403, '<html><body>forbidden</body></html>'));
		const failure = await readJsonAnswer('draft move', '/api/x', { method: 'POST' }).then(
			() => null,
			(error: unknown) => error
		);
		expect(failure).toBeInstanceOf(Error);
		expect((failure as Error).message).toContain('draft move');
		expect((failure as Error).message).toContain('text/html');
	});

	it('names a missing content type instead of trusting the body', async () => {
		stubFetch(
			() =>
				new Response(JSON.stringify({ moved: true }), {
					status: 200,
					headers: {}
				})
		);
		const failure = await readJsonAnswer('draft move', '/api/x', { method: 'POST' }).then(
			() => null,
			(error: unknown) => error
		);
		expect(failure).toBeInstanceOf(Error);
		expect((failure as Error).message).toContain('draft move');
	});

	it('names a malformed JSON body instead of reading it as empty', async () => {
		stubFetch(
			() =>
				new Response('not json{{{', {
					status: 200,
					headers: { 'content-type': 'application/json' }
				})
		);
		const failure = await readJsonAnswer('draft move', '/api/x', { method: 'POST' }).then(
			() => null,
			(error: unknown) => error
		);
		expect(failure).toBeInstanceOf(Error);
		expect((failure as Error).message).toContain('draft move');
	});

	it('keeps the shared refusal shape on a JSON error', async () => {
		stubFetch(() =>
			jsonResponse(
				429,
				{ error: { code: 'pipeline_busy', message: 'the edit queue is full' } },
				{ 'Retry-After': '10' }
			)
		);
		const failure = await readJsonAnswer('draft move', '/api/x', { method: 'POST' }).then(
			() => null,
			(error: unknown) => error
		);
		expect(failure).toBeInstanceOf(ApiError);
		expect((failure as ApiError).code).toBe('pipeline_busy');
		expect((failure as ApiError).retryAfterSeconds).toBe(10);
	});

	it('reads a refused request without a network error', async () => {
		stubFetch(() => {
			throw new TypeError('the network dropped');
		});
		const failure = await readJsonAnswer('draft move', '/api/x', { method: 'POST' }).then(
			() => null,
			(error: unknown) => error
		);
		expect(failure).toBeInstanceOf(ApiError);
		expect((failure as ApiError).code).toBe('network');
		expect((failure as ApiError).message).toContain('draft move');
	});
});
