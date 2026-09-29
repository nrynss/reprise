// Unit pins for the Google sign-in pages. The query parsing never
// touches the network, so plain strings drive every case. The copy
// pin keeps guest words plain.
import { describe, expect, it } from 'vitest';
import {
	ACCOUNT_PAGE,
	BACK,
	CALLBACK_API,
	CALLBACK_PAGE,
	CONTINUE,
	CONFLICT_BODY,
	CONFLICT_HEADING,
	DONE,
	FAILED,
	GOOGLE_PAGE,
	HEADING,
	KEEP,
	START_PATH,
	SUB,
	SWITCH,
	TRY_AGAIN,
	VIEW_ACCOUNT,
	callbackState,
	outcome,
	switchHref
} from './google';

// Words the build uses for itself. A guest notice never carries one.
const BUILD_WORDS = ['endpoint', 'wired', 'fixture', 'scripted', 'job', 'backend', 'detail'];

const COPY = [
	HEADING,
	SUB,
	CONTINUE,
	BACK,
	DONE,
	VIEW_ACCOUNT,
	CONFLICT_HEADING,
	CONFLICT_BODY,
	KEEP,
	SWITCH,
	FAILED,
	TRY_AGAIN
];

describe('copy', () => {
	it('keeps guest words plain', () => {
		for (const line of COPY) {
			for (const word of BUILD_WORDS) {
				expect(line.toLowerCase()).not.toContain(word);
			}
		}
	});
});

describe('paths', () => {
	it('names the server routes and pages', () => {
		expect(START_PATH).toBe('/api/login/google/start');
		expect(CALLBACK_API).toBe('/api/login/google/callback');
		expect(CALLBACK_PAGE).toBe('/account/google/callback');
		expect(ACCOUNT_PAGE).toBe('/account');
		expect(GOOGLE_PAGE).toBe('/account/google');
	});
});

describe('outcome', () => {
	it('reads each flag', () => {
		expect(outcome('?done=1')).toBe('done');
		expect(outcome('?conflict=1&state=abc')).toBe('conflict');
		expect(outcome('?error=signin_failed')).toBe('failed');
		expect(outcome('')).toBe('start');
		expect(outcome('?other=1')).toBe('start');
	});

	it('prefers done over a stray conflict', () => {
		expect(outcome('?done=1&conflict=1')).toBe('done');
	});
});

describe('callbackState', () => {
	it('reads the state or null', () => {
		expect(callbackState('?conflict=1&state=abc')).toBe('abc');
		expect(callbackState('?conflict=1')).toBeNull();
		expect(callbackState('')).toBeNull();
	});
});

describe('switchHref', () => {
	it('retries the callback with the state and the switch choice', () => {
		expect(switchHref('abc 123')).toBe(
			'/api/login/google/callback?state=abc%20123&choice=switch'
		);
	});
});
