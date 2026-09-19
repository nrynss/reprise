// Route shapes the browser shares with the server. The server owns the
// table and the browser mirrors it by hand. A test decodes server written
// goldens against this mirror, so drift fails loudly instead of hiding.
import type { ErrorEnvelope } from '@nrynss/chaaya/wire';

// HttpMethod names the methods the table uses. An empty method on a route
// means every method, which suits a subtree such as the upload prefix.
export type HttpMethod = 'GET' | 'POST' | 'DELETE';

// ApiRoute names one entry of the route table. Method and pattern follow
// the server table field for field.
export interface ApiRoute {
	method: HttpMethod | '';
	pattern: string;
}

// API_ROUTES mirrors the server route table by hand. Human pages stay
// singular. API routes stay plural under /api.
export const API_ROUTES: readonly ApiRoute[] = [
	{ method: 'POST', pattern: '/api/sessions' },
	{ method: 'POST', pattern: '/api/sessions/{id}/end' },
	{ method: '', pattern: '/api/uploads/' },
	{ method: 'GET', pattern: '/api/episodes' },
	{ method: 'GET', pattern: '/api/episodes/{id}' },
	{ method: 'POST', pattern: '/api/episodes/{id}/decisions' },
	{ method: 'POST', pattern: '/api/episodes/{id}/done' },
	{ method: 'GET', pattern: '/api/jobs/{id}/events' },
	{ method: 'POST', pattern: '/api/episodes/{id}/publish' },
	{ method: 'DELETE', pattern: '/api/episodes/{id}/publish' },
	{ method: 'DELETE', pattern: '/api/episodes/{id}' },
	{ method: 'GET', pattern: '/api/threads' },
	{ method: 'GET', pattern: '/api/admin/limits' },
	{ method: 'POST', pattern: '/api/admin/limits/pause' },
	{ method: 'POST', pattern: '/api/admin/limits/owner' },
	{ method: 'GET', pattern: '/media/{id}' }
];

// NOT_IMPLEMENTED is the envelope code every stub handler answers with.
// Screens branch on it to explain that a control has no handler yet.
export const NOT_IMPLEMENTED = 'not_implemented';

// isNotImplemented reports whether the envelope is a stub refusal.
// Callers branch on the code and never on the message.
export function isNotImplemented(envelope: ErrorEnvelope): boolean {
	return envelope.error.code === NOT_IMPLEMENTED;
}
