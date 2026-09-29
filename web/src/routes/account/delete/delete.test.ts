// Unit pins for the deletion client. Stub fetch answers the route, so
// no mail and no server take part. The copy pin keeps guest words plain.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '@nrynss/chaaya/api';
import {
	DELETE_BACK,
	DELETE_BODY,
	DELETE_CANCEL,
	DELETE_CODE_HELP,
	DELETE_CONFIRM,
	DELETE_DONE,
	DELETE_FAILED,
	DELETE_HEADING,
	DELETE_PATH,
	DELETE_SUB,
	INVALID_CODE,
	INVALID_REQUEST,
	deleteAccount
} from './delete';
import type { FetchFn } from '../account';

// Words the build uses for itself. A guest notice never carries one.
const BUILD_WORDS = ['endpoint', 'wired', 'fixture', 'scripted', 'job', 'backend', 'detail'];

function jsonAnswer(body: unknown, status = 200): Response {
	return new Response(JSON.stringify(body), {
		status,
		headers: { 'content-type': 'application/json' }
	});
}

function envelope(code: string): Record<string, unknown> {
	return { error: { code, message: code } };
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('deleteAccount', () => {
	it('posts the address and code as JSON and resolves with the deletion id', async () => {
		const seen: Array<{ url: string; body: unknown }> = [];
		const fetchFn: FetchFn = async (url, init) => {
			seen.push({ url, body: JSON.parse(String(init?.body)) });
			return jsonAnswer({ job_id: 'deletion-1' }, 202);
		};
		const jobId = await deleteAccount(fetchFn, 'friend@example.com', '123456');
		expect(jobId).toBe('deletion-1');
		expect(seen).toHaveLength(1);
		expect(seen[0]?.url).toBe(DELETE_PATH);
		expect(seen[0]?.body).toEqual({ email: 'friend@example.com', code: '123456' });
	});

	it('throws the envelope code on a refused code', async () => {
		const fetchFn: FetchFn = async () => jsonAnswer(envelope(INVALID_CODE), 401);
		const failure = await deleteAccount(fetchFn, 'friend@example.com', '000000').catch(
			(error: unknown) => error
		);
		expect(failure).toBeInstanceOf(ApiError);
		expect((failure as ApiError).code).toBe(INVALID_CODE);
	});

	it('throws the envelope code on a malformed body', async () => {
		const fetchFn: FetchFn = async () => jsonAnswer(envelope(INVALID_REQUEST), 400);
		const failure = await deleteAccount(fetchFn, '', '').catch((error: unknown) => error);
		expect(failure).toBeInstanceOf(ApiError);
		expect((failure as ApiError).code).toBe(INVALID_REQUEST);
	});

	it('throws on a malformed answer', async () => {
		const fetchFn: FetchFn = async () => jsonAnswer({ ok: true }, 202);
		const failure = await deleteAccount(fetchFn, 'friend@example.com', '123456').catch(
			(error: unknown) => error
		);
		expect(failure).toBeInstanceOf(ApiError);
	});

	it('reports an unreachable server as a network refusal', async () => {
		const fetchFn: FetchFn = async () => {
			throw new TypeError('lost');
		};
		const failure = await deleteAccount(fetchFn, 'friend@example.com', '123456').catch(
			(error: unknown) => error
		);
		expect(failure).toBeInstanceOf(ApiError);
		expect((failure as ApiError).code).toBe('network');
	});
});

describe('delete copy', () => {
	it('keeps guest words plain', () => {
		const copy = [
			DELETE_HEADING,
			DELETE_SUB,
			DELETE_BODY,
			DELETE_CONFIRM,
			DELETE_CANCEL,
			DELETE_BACK,
			DELETE_DONE,
			DELETE_CODE_HELP,
			DELETE_FAILED
		];
		for (const line of copy) {
			for (const word of BUILD_WORDS) {
				expect(line.toLowerCase()).not.toContain(word);
			}
		}
	});
});
