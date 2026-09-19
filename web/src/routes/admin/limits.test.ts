// Checks the admin client against fixed bodies. Every case decodes a
// literal, so no clock and no network stand anywhere near these tests.
import { describe, expect, it } from 'vitest';
import {
	formatCents,
	formatMinutes,
	formatSpend,
	guestSpendNotice,
	GUEST_QUOTA_REACHED,
	isOwnerRequired,
	OWNER_REQUIRED,
	parsePause,
	parseSnapshot,
	pausedLabel,
	SESSIONS_PAUSED
} from './limits';

const SNAPSHOT = JSON.stringify({
	sessions_paused: false,
	caps: { guest_max_sessions: 10, session_max_seconds: 1800, daily_spend_cents: 2000 },
	global: { ceiling_nd: 2000000000, remaining_nd: 2000000000, spent_nd: 0 }
});

const DENIED = JSON.stringify({
	error: { code: OWNER_REQUIRED, message: 'the admin page needs the owner login' }
});

describe('refusal codes', () => {
	it('names the mint refusals the preflight screen branches on', () => {
		expect(SESSIONS_PAUSED).toBe('sessions_paused');
		expect(GUEST_QUOTA_REACHED).toBe('guest_quota_reached');
	});

	it('recognises the stub denial', () => {
		expect(isOwnerRequired(DENIED)).toBe(true);
		expect(isOwnerRequired(SNAPSHOT)).toBe(false);
		expect(isOwnerRequired('not json')).toBe(false);
	});
});

describe('parseSnapshot', () => {
	it('decodes the caps, the switch, and the spend', () => {
		const parsed = parseSnapshot(SNAPSHOT);
		expect(parsed.ok).toBe(true);
		if (!parsed.ok) return;
		expect(parsed.value.sessions_paused).toBe(false);
		expect(parsed.value.caps.guest_max_sessions).toBe(10);
		expect(parsed.value.global.spent_nd).toBe(0);
	});

	it('returns the envelope code on refusal', () => {
		const parsed = parseSnapshot(DENIED);
		expect(parsed).toEqual({ ok: false, code: OWNER_REQUIRED });
	});

	it('refuses a broken body', () => {
		expect(parseSnapshot('{')).toEqual({ ok: false, code: 'invalid_request' });
	});
});

describe('parsePause', () => {
	it('decodes the read-back switch value', () => {
		expect(parsePause('{"paused":true}')).toEqual({ ok: true, paused: true });
		expect(parsePause('{"paused":false}')).toEqual({ ok: true, paused: false });
	});

	it('returns the envelope code on refusal', () => {
		expect(parsePause(DENIED)).toEqual({ ok: false, code: OWNER_REQUIRED });
	});
});

describe('labels', () => {
	it('names the switch state the way guests see it', () => {
		const paused = parseSnapshot(SNAPSHOT);
		if (!paused.ok) return;
		expect(pausedLabel({ ...paused.value, sessions_paused: true })).toBe('Recording paused');
		expect(pausedLabel(paused.value)).toBe('Recording open');
	});

	it('renders the owner lookup line only when asked', () => {
		const parsed = parseSnapshot(SNAPSHOT);
		if (!parsed.ok) return;
		expect(guestSpendNotice(parsed.value, '')).toBe('');
		const withOwner = {
			...parsed.value,
			owner: { ceiling_nd: 10000000000, remaining_nd: 7750000000, spent_nd: 2250000000 }
		};
		expect(guestSpendNotice(withOwner, 'guest-1')).toBe('That guest spent $2.25 of its ceiling.');
	});
});

describe('formatting', () => {
	it('renders nanodollars as dollars', () => {
		expect(formatSpend(2250000000)).toBe('$2.25');
		expect(formatSpend(0)).toBe('$0.00');
	});

	it('renders the daily ceiling from cents', () => {
		expect(formatCents(2000)).toBe('$20.00');
	});

	it('renders the session cap as minutes', () => {
		expect(formatMinutes(1800)).toBe('30 min');
	});
});
