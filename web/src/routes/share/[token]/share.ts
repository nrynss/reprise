// Share client for the public episode page. The shapes mirror the
// server handler by hand, so drift fails in the tests below instead
// of hiding. A token opens the finished render and its cover only.
// Stems, transcripts, and threads never cross this client.
import { parseErrorEnvelope } from '@nrynss/chaaya/wire';

// SharePayload is the metadata one token resolves to. AudioID is the
// public render blob the page streams from the media store. CoverPath
// is the cover endpoint behind the same token.
export interface SharePayload {
	episode_id: string;
	title: string;
	number: number;
	audio_media_id: string;
	cover_path: string;
}

// SHARE_MISSING is the envelope code an unknown, revoked, or
// unpublished token answers with. The page branches on it to render
// the missing state instead of mistaking denial for a dead route.
export const SHARE_MISSING = 'not_found';

export type FetchFn = (url: string, init?: RequestInit) => Promise<Response>;

// parseShare decodes a share body. It returns the payload or throws
// an error naming the envelope code the server refused with.
export function parseShare(raw: string): SharePayload {
	let decoded: unknown;
	try {
		decoded = JSON.parse(raw);
	} catch {
		throw new Error(SHARE_MISSING);
	}
	if (typeof decoded !== 'object' || decoded === null) throw new Error(SHARE_MISSING);
	const body = decoded as Record<string, unknown>;
	if (
		typeof body['episode_id'] !== 'string' ||
		typeof body['title'] !== 'string' ||
		typeof body['number'] !== 'number' ||
		typeof body['audio_media_id'] !== 'string' ||
		typeof body['cover_path'] !== 'string'
	) {
		const parsed = parseErrorEnvelope(raw);
		if (parsed.ok) throw new Error(parsed.value.error.code);
		throw new Error(SHARE_MISSING);
	}
	return {
		episode_id: body['episode_id'] as string,
		title: body['title'] as string,
		number: body['number'] as number,
		audio_media_id: body['audio_media_id'] as string,
		cover_path: body['cover_path'] as string
	};
}

// fetchShare resolves one token through the server. It throws an
// error naming SHARE_MISSING for a link that opens nothing.
export async function fetchShare(fetchFn: FetchFn, token: string): Promise<SharePayload> {
	const response = await fetchFn(`/api/share/${token}`);
	const raw = await response.text();
	if (!response.ok) {
		const parsed = parseErrorEnvelope(raw);
		if (parsed.ok) throw new Error(parsed.value.error.code);
		throw new Error(SHARE_MISSING);
	}
	return parseShare(raw);
}

// shareAudioUrl builds the streaming URL for one payload. The media
// store serves the public render blob with no session behind it.
export function shareAudioUrl(payload: SharePayload): string {
	return `/media/${payload.audio_media_id}`;
}

// formatEpisodeNumber renders the episode counter the page shows
// beside the title, such as EP.04.
export function formatEpisodeNumber(number: number): string {
	return `EP.${String(number).padStart(2, '0')}`;
}

// fixtureShare returns the scripted payload the page renders when the
// fixture flag names a published link. End-to-end proofs run on it
// with no backend behind the page.
export function fixtureShare(): SharePayload {
	return {
		episode_id: 'episode-fixture',
		title: 'Three weeks of almost',
		number: 4,
		audio_media_id: 'audio-fixture',
		cover_path: '/api/share/fixture-token/cover'
	};
}
