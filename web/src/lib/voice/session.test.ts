import { describe, expect, it } from 'vitest';
import { parseSessionStart, socketUrl } from './session';

const BODY = JSON.stringify({
	session_id: 's1',
	episode_id: 'e1',
	token: 'tok',
	expires_in_seconds: 60,
	max_session_duration_seconds: 1200,
	config: { system_prompt: 'prompt', greeting: 'hello', keyterms: ['Mara', 'the loft'] }
});

describe('parseSessionStart', () => {
	it('reads the broker body field for field', () => {
		const parsed = parseSessionStart(BODY);
		expect(parsed.session_id).toBe('s1');
		expect(parsed.episode_id).toBe('e1');
		expect(parsed.token).toBe('tok');
		expect(parsed.expires_in_seconds).toBe(60);
		expect(parsed.max_session_duration_seconds).toBe(1200);
		expect(parsed.config.greeting).toBe('hello');
		expect(parsed.config.keyterms).toEqual(['Mara', 'the loft']);
	});

	it('throws on a missing token instead of opening an anonymous socket', () => {
		const without = JSON.parse(BODY) as Record<string, unknown>;
		delete without['token'];
		expect(() => parseSessionStart(JSON.stringify(without))).toThrow();
	});

	it('throws on keyterms that are not a string list', () => {
		const broken = JSON.parse(BODY) as { config: { keyterms: unknown } };
		broken.config.keyterms = ['Mara', 7];
		expect(() => parseSessionStart(JSON.stringify(broken))).toThrow();
	});

	it('throws on a body that is not JSON', () => {
		expect(() => parseSessionStart('not json')).toThrow();
	});
});

describe('socketUrl', () => {
	it('carries the token as the only query value', () => {
		expect(socketUrl('tok')).toBe('wss://agents.assemblyai.com/v1/ws?token=tok');
	});

	it('escapes token characters', () => {
		expect(socketUrl('a+b')).toContain('a%2Bb');
	});
});
