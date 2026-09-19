// Checks the hand mirror against server written goldens. The goldens come
// from the server test, so this file decodes server output and never a copy.
import { describe, expect, it } from 'vitest';
import { parseErrorEnvelope } from '@nrynss/chaaya/wire';
import { API_ROUTES, NOT_IMPLEMENTED, isNotImplemented } from './types';
import routesGolden from './testdata/routes.json';
import stubGolden from './testdata/stub-refusal.json';

describe('API_ROUTES', () => {
	it('mirrors the server route table', () => {
		expect([...API_ROUTES]).toEqual(routesGolden);
	});

	it('lists the admin patterns the owner page calls', () => {
		const patterns = API_ROUTES.map((route) => `${route.method} ${route.pattern}`);
		expect(patterns).toContain('GET /api/admin/limits');
		expect(patterns).toContain('POST /api/admin/limits/pause');
		expect(patterns).toContain('POST /api/admin/limits/owner');
	});
});

describe('stub refusal', () => {
	it('parses through the shared envelope parser', () => {
		const parsed = parseErrorEnvelope(JSON.stringify(stubGolden));
		expect(parsed.ok).toBe(true);
		if (!parsed.ok) return;
		expect(parsed.value.error.code).toBe(NOT_IMPLEMENTED);
		expect(parsed.value.error.message.length).toBeGreaterThan(0);
		expect(isNotImplemented(parsed.value)).toBe(true);
	});

	it('rejects any other code', () => {
		expect(isNotImplemented({ error: { code: 'gone', message: 'no such episode' } })).toBe(false);
	});
});
