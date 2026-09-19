// Admin page client for the caps, the switch, and the spend figures.
// The shapes mirror the server handler by hand, so drift fails in the
// tests below instead of hiding. Money travels in nanodollars and the
// page formats it, because rounding belongs to display and never to the
// ledger.
import { parseErrorEnvelope } from '@nrynss/chaaya/wire';

export interface AdminCaps {
	guest_max_sessions: number;
	session_max_seconds: number;
	daily_spend_cents: number;
}

export interface AdminSpending {
	ceiling_nd: number;
	remaining_nd: number;
	spent_nd: number;
}

export interface AdminSnapshot {
	sessions_paused: boolean;
	caps: AdminCaps;
	global: AdminSpending;
	owner?: AdminSpending;
}

// OWNER_REQUIRED is the envelope code the stub auth answers with until
// the owner login lands. The page branches on it to render the sign-in
// state instead of mistaking denial for a dead route.
export const OWNER_REQUIRED = 'owner_required';

// SESSIONS_PAUSED is the mint refusal code while the switch is set. The
// preflight screen branches on it to render the recording paused state.
export const SESSIONS_PAUSED = 'sessions_paused';

// GUEST_QUOTA_REACHED is the mint refusal code past the guest session
// cap. The preflight screen branches on it to render the guest limit
// reached state. The string matches the broker refusal exactly.
export const GUEST_QUOTA_REACHED = 'guest_quota_reached';

export type FetchFn = (url: string, init?: RequestInit) => Promise<Response>;

// emptyAdminSnapshot gives the page an initial render with no ledger
// behind it. The page swaps it for the fetched snapshot on load.
export const emptyAdminSnapshot: AdminSnapshot = {
	sessions_paused: false,
	caps: { guest_max_sessions: 0, session_max_seconds: 0, daily_spend_cents: 0 },
	global: { ceiling_nd: 0, remaining_nd: 0, spent_nd: 0 }
};

// isOwnerRequired reports whether the raw body is the stub denial.
export function isOwnerRequired(raw: string): boolean {
	const parsed = parseErrorEnvelope(raw);
	if (!parsed.ok) return false;
	return parsed.value.error.code === OWNER_REQUIRED;
}

// parseSnapshot decodes a snapshot body. It returns the snapshot or the
// envelope code the server refused with.
export function parseSnapshot(raw: string): { ok: true; value: AdminSnapshot } | { ok: false; code: string } {
	let decoded: unknown;
	try {
		decoded = JSON.parse(raw);
	} catch {
		return { ok: false, code: 'invalid_request' };
	}
	if (typeof decoded !== 'object' || decoded === null || 'error' in decoded) {
		const parsed = parseErrorEnvelope(raw);
		if (parsed.ok) return { ok: false, code: parsed.value.error.code };
		return { ok: false, code: 'invalid_request' };
	}
	return { ok: true, value: decoded as AdminSnapshot };
}

// formatSpend renders nanodollars as dollars with two decimals. Fixed
// inputs only, so no clock stands anywhere near it.
export function formatSpend(nanodollars: number): string {
	return `$${(nanodollars / 1_000_000_000).toFixed(2)}`;
}

// formatCents renders the daily ceiling from settings as dollars.
export function formatCents(cents: number): string {
	return `$${(cents / 100).toFixed(2)}`;
}

// formatMinutes renders the session length cap as whole minutes.
export function formatMinutes(seconds: number): string {
	return `${Math.round(seconds / 60)} min`;
}

// parsePause decodes the pause answer body. It returns the read-back
// switch value or the envelope code the server refused with.
export function parsePause(raw: string): { ok: true; paused: boolean } | { ok: false; code: string } {
	let decoded: unknown;
	try {
		decoded = JSON.parse(raw);
	} catch {
		return { ok: false, code: 'invalid_request' };
	}
	if (typeof decoded === 'object' && decoded !== null && 'paused' in decoded) {
		const paused = (decoded as { paused: unknown }).paused;
		if (typeof paused === 'boolean') return { ok: true, paused };
	}
	const envelope = parseErrorEnvelope(raw);
	if (envelope.ok) return { ok: false, code: envelope.value.error.code };
	return { ok: false, code: 'invalid_request' };
}
// pausedLabel renders the switch heading the admin section shows.
export function pausedLabel(snapshot: AdminSnapshot): string {
	return snapshot.sessions_paused ? 'Recording paused' : 'Recording open';
}

// guestSpendNotice renders the per-owner lookup line, or nothing when no
// owner figure was asked for.
export function guestSpendNotice(snapshot: AdminSnapshot, query: string): string {
	if (snapshot && query && snapshot.owner) {
		return `That guest spent ${formatSpend(snapshot.owner.spent_nd)} of its ceiling.`;
	}
	return '';
}

// fetchSnapshot reads the admin snapshot, with an optional per-owner
// figure. It throws the envelope code on refusal.
export async function fetchSnapshot(fetchFn: FetchFn, owner?: string): Promise<AdminSnapshot> {
	const target = owner ? `/api/admin/limits?owner=${encodeURIComponent(owner)}` : '/api/admin/limits';
	const response = await fetchFn(target);
	const raw = await response.text();
	const parsed = parseSnapshot(raw);
	if (!parsed.ok) throw new Error(parsed.code);
	return parsed.value;
}

// setPaused flips the switch at once and returns the read-back value.
export async function setPaused(fetchFn: FetchFn, paused: boolean): Promise<boolean> {
	const response = await fetchFn('/api/admin/limits/pause', {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ paused })
	});
	const raw = await response.text();
	const parsed = parsePause(raw);
	if (!parsed.ok) throw new Error(parsed.code);
	return parsed.paused;
}
