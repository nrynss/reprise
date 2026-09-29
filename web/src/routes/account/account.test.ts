// Unit pins for the sign-in client. Stub fetch answers each route, so
// no mail and no server take part. The copy pin keeps guest words plain.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '@nrynss/chaaya/api';
import {
	ACCOUNT_EVENT,
	ACCOUNT_HEADING,
	ACCOUNT_PATH,
	ACCOUNT_SUB,
	CHECK_CODE,
	CODE_LABEL,
	CONFLICT_BODY,
	CONFLICT_HEADING,
	DEFAULT_RESEND_WAIT,
	DELETE_ACCOUNT,
	DIARY_CONFLICT,
	EMAIL_HELP,
	EMAIL_LABEL,
	ENTER_ADDRESS,
	ENTER_CODE,
	INVALID_CODE,
	INVALID_REQUEST,
	KEEP_DIARY,
	KEEP_HELP,
	KEPT_DIARY,
	NAME_FAILED,
	NAME_HELP,
	NAME_INVALID,
	NAME_LABEL,
	NAME_PATH,
	NAME_SAVED,
	REQUEST_FAILED,
	RESEND_CODE,
	SAVE_NAME,
	SEND_CODE,
	SEND_LIMITED,
	SIGN_OUT,
	SIGN_OUT_FAILED,
	SIGNOUT_PATH,
	SIGNED_OUT,
	SWITCH_ACCOUNT,
	SWITCH_HELP,
	UNAUTHORIZED,
	WRONG_CODE,
	codeSentNotice,
	fetchAccount,
	forgetSignedInEmail,
	normalizeEmail,
	readRetryAfterSeconds,
	readSignedInEmail,
	rememberSignedInEmail,
	requestCode,
	retryWaitNotice,
	saveDisplayName,
	signedInNotice,
	signOut,
	verifyCode,
	type FetchFn
} from './account';

// Words the build uses for itself. A guest notice never carries one.
const BUILD_WORDS = ['endpoint', 'wired', 'fixture', 'scripted', 'job', 'backend', 'detail'];

function jsonAnswer(body: unknown, status = 200, headers: Record<string, string> = {}): Response {
	return new Response(JSON.stringify(body), {
		status,
		headers: { 'content-type': 'application/json', ...headers }
	});
}

function envelope(code: string): Record<string, unknown> {
	return { error: { code, message: code } };
}

afterEach(() => {
	vi.unstubAllGlobals();
	window.localStorage.clear();
});

describe('requestCode', () => {
	it('posts the address as JSON and resolves on accept', async () => {
		const seen: Array<{ url: string; body: unknown }> = [];
		const fetchFn: FetchFn = async (url, init) => {
			seen.push({ url, body: JSON.parse(String(init?.body)) });
			return jsonAnswer({ ok: true }, 202);
		};
		await requestCode(fetchFn, 'friend@example.com');
		expect(seen).toHaveLength(1);
		expect(seen[0]?.url).toBe('/api/login/code');
		expect(seen[0]?.body).toEqual({ email: 'friend@example.com' });
	});

	it('carries the wait from a capped answer', async () => {
		const fetchFn: FetchFn = async () => jsonAnswer(envelope(SEND_LIMITED), 429, { 'retry-after': '45' });
		const failure = await requestCode(fetchFn, 'friend@example.com').catch((error: unknown) => error);
		expect(failure).toBeInstanceOf(ApiError);
		const apiError = failure as ApiError;
		expect(apiError.code).toBe(SEND_LIMITED);
		expect(apiError.retryAfterSeconds).toBe(45);
	});

	it('reports an unreachable server as a network refusal', async () => {
		const fetchFn: FetchFn = async () => {
			throw new TypeError('lost');
		};
		const failure = await requestCode(fetchFn, 'friend@example.com').catch((error: unknown) => error);
		expect(failure).toBeInstanceOf(ApiError);
		expect((failure as ApiError).code).toBe('network');
	});
});

describe('verifyCode', () => {
	it('returns signed in on accept', async () => {
		const fetchFn: FetchFn = async () => jsonAnswer({ ok: true }, 200);
		await expect(verifyCode(fetchFn, 'friend@example.com', '123456')).resolves.toBe('signedin');
	});

	it('returns a conflict instead of throwing one', async () => {
		const fetchFn: FetchFn = async () => jsonAnswer(envelope(DIARY_CONFLICT), 409);
		await expect(verifyCode(fetchFn, 'friend@example.com', '123456')).resolves.toBe('conflict');
	});

	it('throws the invalid code on a refused code', async () => {
		const fetchFn: FetchFn = async () => jsonAnswer(envelope(INVALID_CODE), 401);
		const failure = await verifyCode(fetchFn, 'friend@example.com', '000000').catch(
			(error: unknown) => error
		);
		expect(failure).toBeInstanceOf(ApiError);
		expect((failure as ApiError).code).toBe(INVALID_CODE);
	});

	it('carries the switch choice on a repeat check', async () => {
		const seen: unknown[] = [];
		const fetchFn: FetchFn = async (_url, init) => {
			seen.push(JSON.parse(String(init?.body)));
			return jsonAnswer({ ok: true }, 200);
		};
		await verifyCode(fetchFn, 'friend@example.com', '123456', 'switch');
		expect(seen).toEqual([{ email: 'friend@example.com', code: '123456', choice: 'switch' }]);
	});
});

describe('signOut', () => {
	it('posts to the sign-out route with no body of note', async () => {
		const seen: string[] = [];
		const fetchFn: FetchFn = async (url) => {
			seen.push(url);
			return jsonAnswer({ ok: true }, 200);
		};
		await signOut(fetchFn);
		expect(seen).toEqual([SIGNOUT_PATH]);
	});
});

describe('shared name', () => {
	it('reads the stored name beside the address', async () => {
		const seen: string[] = [];
		const fetchFn: FetchFn = async (url) => {
			seen.push(url);
			return jsonAnswer({ email: 'friend@example.com', display_name: 'Mara' }, 200);
		};
		const account = await fetchAccount(fetchFn);
		expect(seen).toEqual([ACCOUNT_PATH]);
		expect(account).toEqual({ email: 'friend@example.com', displayName: 'Mara' });
	});

	it('writes the name with PUT and answers what the server kept', async () => {
		const seen: Array<{ url: string; method?: string; body: unknown }> = [];
		const fetchFn: FetchFn = async (url, init) => {
			seen.push({ url, method: init?.method, body: JSON.parse(String(init?.body)) });
			return jsonAnswer({ email: 'friend@example.com', display_name: 'Mara' }, 200);
		};
		const account = await saveDisplayName(fetchFn, 'Mara');
		expect(seen).toEqual([
			{ url: NAME_PATH, method: 'PUT', body: { display_name: 'Mara' } }
		]);
		expect(account.displayName).toBe('Mara');
	});

	it('throws the unauthorized code for a guest read', async () => {
		const fetchFn: FetchFn = async () => jsonAnswer(envelope(UNAUTHORIZED), 401);
		const failure = await fetchAccount(fetchFn).catch((error: unknown) => error);
		expect(failure).toBeInstanceOf(ApiError);
		expect((failure as ApiError).code).toBe(UNAUTHORIZED);
	});

	it('throws the invalid code for a refused name', async () => {
		const fetchFn: FetchFn = async () => jsonAnswer(envelope(INVALID_REQUEST), 400);
		const failure = await saveDisplayName(fetchFn, 'x'.repeat(61)).catch(
			(error: unknown) => error
		);
		expect(failure).toBeInstanceOf(ApiError);
		expect((failure as ApiError).code).toBe(INVALID_REQUEST);
	});
});

describe('retry wait', () => {
	it('reads seconds from the header and falls back without one', () => {
		const named = new Response('x', { headers: { 'retry-after': '30' } });
		expect(readRetryAfterSeconds(named)).toBe(30);
		const bare = new Response('x');
		expect(readRetryAfterSeconds(bare)).toBeUndefined();
		const dated = new Response('x', { headers: { 'retry-after': 'soon' } });
		expect(readRetryAfterSeconds(dated)).toBeUndefined();
	});

	it('fills the default wait when the capped answer names none', () => {
		expect(DEFAULT_RESEND_WAIT).toBeGreaterThan(0);
		expect(retryWaitNotice(DEFAULT_RESEND_WAIT)).toContain(String(DEFAULT_RESEND_WAIT));
	});
});

describe('signed in store', () => {
	it('remembers, reads, and forgets one address', () => {
		expect(readSignedInEmail()).toBeNull();
		rememberSignedInEmail('  Friend@Example.com ');
		expect(readSignedInEmail()).toBe('friend@example.com');
		forgetSignedInEmail();
		expect(readSignedInEmail()).toBeNull();
	});

	it('normalizes an address the way the server reads it', () => {
		expect(normalizeEmail('  Friend@Example.com ')).toBe('friend@example.com');
	});

	it('announces its own writes on this tab', () => {
		const events: string[] = [];
		window.addEventListener(ACCOUNT_EVENT, () => {
			events.push(ACCOUNT_EVENT);
		});
		rememberSignedInEmail('friend@example.com');
		forgetSignedInEmail();
		expect(events).toEqual([ACCOUNT_EVENT, ACCOUNT_EVENT]);
	});
});

describe('account copy', () => {
	it('keeps every guest string free of build words', () => {
		const samples = [
			ACCOUNT_HEADING,
			ACCOUNT_SUB,
			EMAIL_LABEL,
			EMAIL_HELP,
			SEND_CODE,
			ENTER_ADDRESS,
			CODE_LABEL,
			CHECK_CODE,
			ENTER_CODE,
			WRONG_CODE,
			RESEND_CODE,
			REQUEST_FAILED,
			SIGN_OUT,
			SIGNED_OUT,
			SIGN_OUT_FAILED,
			DELETE_ACCOUNT,
			NAME_LABEL,
			NAME_HELP,
			SAVE_NAME,
			NAME_SAVED,
			NAME_INVALID,
			NAME_FAILED,
			CONFLICT_HEADING,
			CONFLICT_BODY,
			KEEP_DIARY,
			KEEP_HELP,
			SWITCH_ACCOUNT,
			SWITCH_HELP,
			KEPT_DIARY,
			codeSentNotice('friend@example.com'),
			signedInNotice('friend@example.com'),
			retryWaitNotice(45)
		];
		for (const text of samples) {
			const lower = text.toLowerCase();
			for (const word of BUILD_WORDS) {
				expect(lower.includes(word), `"${text}" carries "${word}"`).toBe(false);
			}
		}
	});

	it('names the capped request code, so the page honours the wait', () => {
		expect(SEND_LIMITED).toBe('send_limited');
		expect(INVALID_REQUEST).toBe('invalid_request');
	});

	it('names the deletion entry the signed in screen links', () => {
		expect(DELETE_ACCOUNT).toBe('Delete account');
	});
});
