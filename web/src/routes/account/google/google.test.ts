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

	it('reads the done flag only on its set value', () => {
		expect(outcome('?done=1')).toBe('done');
		expect(outcome('?done=0')).toBe('start');
		expect(outcome('?done=')).toBe('start');
		expect(outcome('?done=2')).toBe('start');
	});

	it('reads the conflict flag only on its set value', () => {
		expect(outcome('?conflict=1')).toBe('conflict');
		expect(outcome('?conflict=0')).toBe('start');
		expect(outcome('?conflict=')).toBe('start');
	});

	it('reads any error flag as failed', () => {
		expect(outcome('?error=signin_failed')).toBe('failed');
		expect(outcome('?error=')).toBe('failed');
		expect(outcome('?error=access_denied&state=abc')).toBe('failed');
	});

	it('reads a state alone as a fresh start', () => {
		expect(outcome('?state=abc')).toBe('start');
	});

	it('reads an absent or empty query as a fresh start', () => {
		expect(outcome('')).toBe('start');
		expect(outcome('?')).toBe('start');
		expect(outcome('?other=1')).toBe('start');
	});

	it('prefers done over a stray conflict', () => {
		expect(outcome('?done=1&conflict=1')).toBe('done');
	});

	it('prefers done and conflict over an error', () => {
		expect(outcome('?done=1&error=signin_failed')).toBe('done');
		expect(outcome('?conflict=1&error=signin_failed')).toBe('conflict');
	});

	it('reads the first value on a repeated key', () => {
		expect(outcome('?done=0&done=1')).toBe('start');
		expect(outcome('?conflict=0&conflict=1')).toBe('start');
	});
});

describe('callbackState', () => {
	it('reads the state or null', () => {
		expect(callbackState('?conflict=1&state=abc')).toBe('abc');
		expect(callbackState('?conflict=1')).toBeNull();
		expect(callbackState('')).toBeNull();
	});

	it('reads an empty or absent state as null', () => {
		expect(callbackState('?state=')).toBeNull();
		expect(callbackState('?')).toBeNull();
		expect(callbackState('?other=1')).toBeNull();
	});

	it('reads a state without other flags', () => {
		expect(callbackState('?state=abc')).toBe('abc');
	});

	it('reads the first value on a repeated state', () => {
		expect(callbackState('?state=first&state=second')).toBe('first');
	});
});

describe('switchHref', () => {
	it('retries the callback with the state and the switch choice', () => {
		expect(switchHref('abc 123')).toBe(
			'/api/login/google/callback?state=abc%20123&choice=switch'
		);
	});
});
