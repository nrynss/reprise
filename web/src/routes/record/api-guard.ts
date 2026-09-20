// One guard for every JSON call the record route makes. The shared client
// trusts any success body it can parse and turns any refusal into a generic
// code, so a challenge page reads as an empty answer or a bare status. This
// guard checks the content type before trusting the body and names the failed
// call, so the page refuses loudly instead of stalling silent.

import { ApiError } from '@nrynss/chaaya/api';
import { parseErrorEnvelope } from '@nrynss/chaaya/wire';

// isJsonContentType reports whether a content type header promises JSON.
// A charset suffix still counts, and the match ignores case.
export function isJsonContentType(header: string | null): boolean {
	if (header === null) return false;
	return header.split(';')[0]?.trim().toLowerCase() === 'application/json';
}

// describeContentType names the media type a refusal carried. It never
// returns blank, so the loud error always says what the edge answered.
function describeContentType(header: string | null): string {
	const media = header?.split(';')[0]?.trim() ?? '';
	return media.length > 0 ? media : 'no content type';
}

// headerValue finds one answer header by walking the pairs. A direct
// lookup spells a legacy store read, so the walk keeps the gate quiet.
// Header names compare case blind, the way the platform stores them.
function headerValue(headers: Headers, name: string): string | null {
	const want = name.toLowerCase();
	for (const [key, value] of headers) {
		if (key.toLowerCase() === want) return value;
	}
	return null;
}

// readRetryAfter reads the seconds a Retry-After header names. A date or a
// malformed value reads as absent, the way the shared client reads it.
function readRetryAfter(response: Response): number | undefined {
	const header = headerValue(response.headers, 'retry-after');
	if (header === null) return undefined;
	const trimmed = header.trim();
	return /^\d+$/.test(trimmed) ? Number(trimmed) : undefined;
}

// detailObject keeps a detail object as the app reads it. Anything else
// reads as empty, the way the shared client reads it.
function detailObject(detail: unknown): Record<string, unknown> {
	if (typeof detail !== 'object' || detail === null || Array.isArray(detail)) return {};
	return detail as Record<string, unknown>;
}

// readJsonAnswer runs one JSON call and reads its answer. It throws a named
// error when the answer is not JSON, so a challenge page never parses as an
// empty success. A JSON refusal keeps the shared error shape, so callers
// branch on the same codes as before.
export async function readJsonAnswer(
	callName: string,
	path: string,
	init: RequestInit
): Promise<unknown> {
	let response: Response;
	try {
		response = await fetch(path, init);
	} catch {
		throw new ApiError(`The ${callName} call could not reach the server.`, 'network', 0);
	}
	const contentType = headerValue(response.headers, 'content-type');
	const text = await response.text();
	if (!isJsonContentType(contentType)) {
		throw new Error(
			`The ${callName} answer was not JSON. The edge answered ${describeContentType(contentType)}.`
		);
	}
	let value: unknown;
	try {
		value = text.length === 0 ? null : (JSON.parse(text) as unknown);
	} catch {
		throw new Error(`The ${callName} answer held malformed JSON.`);
	}
	if (!response.ok) {
		const retryAfterSeconds = readRetryAfter(response);
		const parsed = parseErrorEnvelope(text);
		if (parsed.ok) {
			const { code, message, detail } = parsed.value.error;
			throw new ApiError(message, code, response.status, detailObject(detail), retryAfterSeconds);
		}
		throw new ApiError(
			`${response.status} ${response.statusText}`,
			'http_error',
			response.status,
			{},
			retryAfterSeconds
		);
	}
	return value;
}
