// Guarded JSON calls for the take path. The shared client trusts any success
// body it can parse, so a challenge page with a success status reads as an
// empty answer there. Every call here checks the content type before trusting
// the body and names the failed call, so the page refuses loudly instead of
// stalling silent. The record route guard owns the check. This module reuses
// it and adds the retry rules the take needs.

import { ApiError } from '@nrynss/chaaya/api';
import { readJsonAnswer } from '../../routes/record/api-guard';
import { parseSessionStart, type SessionStart } from './session';

// UploadSlot names the uploader fields the take reads. The library owns the
// class. This shape keeps the failure prose testable with no store behind it.
export interface UploadSlot {
	readonly state: string;
	readonly error?: { readonly code: string } | undefined;
}

// mintSession opens one broker session and reads its body. A second call
// would mint a second session and reserve budget twice, so a failure throws
// with no retry. The page stays preflight and the start control is the retry.
export async function mintSession(): Promise<SessionStart> {
	const value = await readJsonAnswer('session mint', '/api/sessions', { method: 'POST' });
	return parseSessionStart(JSON.stringify(value));
}

// closeSession records the provider close for one session. The body carries
// the provider id the socket learned. The socket close already stopped the
// billing clock and this record is idempotent, so one network drop earns
// one more attempt with the same id.
export async function closeSession(sessionId: string, providerSessionId: string): Promise<void> {
	const path = `/api/sessions/${encodeURIComponent(sessionId)}/end`;
	const init: RequestInit = {
		method: 'POST',
		headers: { 'content-type': 'application/json' },
		body: JSON.stringify({ provider_session_id: providerSessionId })
	};
	try {
		await readJsonAnswer('session end', path, init);
	} catch (error) {
		if (error instanceof ApiError && error.code === 'network') {
			await readJsonAnswer('session end', path, init);
			return;
		}
		throw error;
	}
}

// readJobState reads one job row for the processing follower. A challenge
// page throws instead of landing as an empty row, and the follower keeps its
// last reading the way it does on any dropped poll.
export async function readJobState<Snapshot>(jobId: string): Promise<Snapshot> {
	const value = await readJsonAnswer('job poll', `/api/jobs/${encodeURIComponent(jobId)}`, {});
	return value as Snapshot;
}

// describeSessionEndFailure turns a close throw into loud words with a retry.
// The socket close already stopped the billing clock, so reposting the record
// never double closes the provider side.
export function describeSessionEndFailure(error: unknown): string {
	if (error instanceof Error) {
		if (error.message.includes('session end')) return `${error.message} Press retry.`;
		return `The session end call failed. ${error.message} Press retry.`;
	}
	return 'The session end call failed. Press retry.';
}

// describeUploadFailure names a failed stem upload for the loud notice. It
// reads null when both uploads left the failure state clear, so the take
// proceeds silent then. Callers add the retry words for their own stage.
export function describeUploadFailure(
	user: UploadSlot | null,
	host: UploadSlot | null
): string | null {
	return uploadSideFailure('user', user) ?? uploadSideFailure('host', host);
}

// uploadSideFailure names one failed side or reads null when it streams.
function uploadSideFailure(side: string, upload: UploadSlot | null): string | null {
	if (upload === null || upload.state !== 'failed') return null;
	const code = upload.error?.code ?? 'unknown';
	return `The ${side} stem upload failed (${code}).`;
}
