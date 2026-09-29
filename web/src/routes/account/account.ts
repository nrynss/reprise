// Sign-in client behind the account page. The shapes mirror the login
// routes by hand, so drift fails in the tests beside this file instead
// of hiding. Screens branch on the envelope codes and never on wording.
// The signed in address persists in local storage, because no status
// route reports it yet. A status route would replace that store.
import { ApiError } from '@nrynss/chaaya/api';
import { parseErrorEnvelope } from '@nrynss/chaaya/wire';

// FetchFn runs one request. Tests pass a stub, and the page passes fetch.
export type FetchFn = (url: string, init?: RequestInit) => Promise<Response>;

// Envelope codes the login routes answer with. They match the server
// codes exactly, so the page branches the way the routes mean.
export const INVALID_CODE = 'invalid_code';
export const DIARY_CONFLICT = 'diary_conflict';
export const INVALID_REQUEST = 'invalid_request';
export const SEND_LIMITED = 'send_limited';

// SIGNOUT_PATH names the route that revokes the session and starts a
// fresh guest. The server does not serve it yet, so signing out reports
// a contract change until it lands.
export const SIGNOUT_PATH = '/api/login/signout';

// ACCOUNT_PATH reads the caller address beside its shared name. A guest
// answers 401, because there is no account to read.
export const ACCOUNT_PATH = '/api/account';

// NAME_PATH writes the caller shared name. The server trims it, keeps at
// most 60 characters, and refuses control characters. An empty name
// clears the stored value.
export const NAME_PATH = '/api/account/name';

// UNAUTHORIZED answers a guest on either account route. Guests hold no
// address, so there is no account to read or rename.
export const UNAUTHORIZED = 'unauthorized';

// NAME_LABEL names the shared name field on the signed in screen.
export const NAME_LABEL = 'Name on shared episodes';

// NAME_HELP says where the shared name appears.
export const NAME_HELP = 'Shared episodes show this name beside the title.';

// SAVE_NAME names the button that stores the shared name.
export const SAVE_NAME = 'Save the name';

// NAME_SAVED confirms the shared name reached the server.
export const NAME_SAVED = 'Name saved. New shared episodes carry it.';

// NAME_INVALID answers a name the server refused.
export const NAME_INVALID = 'That name did not fit. Keep it short and try again.';

// NAME_FAILED answers a save the page has no words for.
export const NAME_FAILED = "Couldn't save the name. Try again.";

// DEFAULT_RESEND_WAIT fills in when a capped answer names no wait. The
// page still honours the header first, so this only covers a bare 429.
export const DEFAULT_RESEND_WAIT = 60;

// ACCOUNT_HEADING names the page. Every screen on the page keeps it.
export const ACCOUNT_HEADING = 'Your account';

// ACCOUNT_SUB says what signing in does on this device.
export const ACCOUNT_SUB = 'One code signs this device in. The diary on it stays until you switch.';

// EMAIL_LABEL names the address field.
export const EMAIL_LABEL = 'Email address';

// EMAIL_HELP says where the code goes and how long it lasts.
export const EMAIL_HELP = 'The code goes to this address. It expires in 10 minutes.';

// SEND_CODE names the button that asks for a code.
export const SEND_CODE = 'Send the code';

// ENTER_ADDRESS answers an empty address field.
export const ENTER_ADDRESS = 'Type an address first, then ask for a code.';

// CODE_LABEL names the six digit field.
export const CODE_LABEL = 'Six digit code';

// CHECK_CODE names the button that checks a typed code.
export const CHECK_CODE = 'Check the code';

// ENTER_CODE answers an empty code field.
export const ENTER_CODE = 'Type the six digit code first, then check it.';

// WRONG_CODE answers a refused code. It never says which guard tripped.
export const WRONG_CODE = 'That code did not match. Check the mail and try again.';

// RESEND_CODE names the link that asks for a fresh code.
export const RESEND_CODE = 'Send a new code';

// REQUEST_FAILED answers a refusal the page has no words for.
export const REQUEST_FAILED = 'That did not work. Try again.';

// SIGN_OUT names the button that ends the session on this device.
export const SIGN_OUT = 'Sign out';

// SIGNED_OUT confirms the session ended and a fresh diary starts here.
export const SIGNED_OUT = 'Signed out on this device. A fresh diary starts here.';

// SIGN_OUT_FAILED answers a sign-out the server refused.
export const SIGN_OUT_FAILED = 'Signing out did not work. Try again.';

// DELETE_ACCOUNT names the deletion entry on the signed in screen.
export const DELETE_ACCOUNT = 'Delete account';

// CONFLICT_HEADING names the choice between two diaries.
export const CONFLICT_HEADING = 'This device already holds takes';

// CONFLICT_BODY says why signing in pauses here.
export const CONFLICT_BODY =
	'Your account lives elsewhere, and this device holds takes it has never seen. ' +
	'Keep this device as it is, or switch it to your account.';

// KEEP_DIARY names the choice that leaves the device alone.
export const KEEP_DIARY = "Keep this device's diary";

// KEEP_HELP says keeping changes nothing and signs in nothing.
export const KEEP_HELP = 'Stay signed out here. Nothing moves, and you can sign in again later.';

// SWITCH_ACCOUNT names the choice that joins the account.
export const SWITCH_ACCOUNT = 'Switch to your account';

// SWITCH_HELP says the device diary expires like any guest diary.
export const SWITCH_HELP = 'This device joins your account. The takes on it expire like any guest takes.';

// KEPT_DIARY confirms the device kept its takes and stays signed out.
export const KEPT_DIARY = 'This device keeps its takes. You can sign in again later.';

// codeSentNotice confirms a code left for one address.
export function codeSentNotice(address: string): string {
	return `A code is on its way to ${address}. It expires in 10 minutes.`;
}

// signedInNotice names the address this device signed in as.
export function signedInNotice(address: string): string {
	return `Signed in as ${address}.`;
}

// retryWaitNotice asks for patience after a capped code request.
export function retryWaitNotice(seconds: number): string {
	return `Too many codes went out. Wait ${seconds} seconds, then ask for a new one.`;
}

// normalizeEmail trims one address the way the server reads it. The page
// stores and sends this form, so one typed variant maps to one label.
export function normalizeEmail(email: string): string {
	return email.trim().toLowerCase();
}

// headerValue finds one answer header by walking the pairs. Header names
// compare case blind, the way the platform stores them.
function headerValue(headers: Headers, name: string): string | null {
	const want = name.toLowerCase();
	for (const [key, value] of headers) {
		if (key.toLowerCase() === want) return value;
	}
	return null;
}

// readRetryAfterSeconds reads the seconds a Retry-After header names. A
// date or a malformed value reads as absent, so the caller falls back.
export function readRetryAfterSeconds(response: Response): number | undefined {
	const header = headerValue(response.headers, 'retry-after');
	if (header === null) return undefined;
	const trimmed = header.trim();
	return /^\d+$/.test(trimmed) ? Number(trimmed) : undefined;
}

// Account names the address beside its shared name. DisplayName is
// empty while the caller never set one.
export interface Account {
	email: string;
	displayName: string;
}

// parseAccount decodes an account body. It throws an ApiError naming the
// envelope code the server refused with.
export function parseAccount(raw: string, status: number): Account {
	let decoded: unknown;
	try {
		decoded = JSON.parse(raw);
	} catch {
		throw new ApiError('The server answer held malformed JSON.', 'http_error', status);
	}
	if (typeof decoded !== 'object' || decoded === null) {
		throw new ApiError('The server answer held malformed JSON.', 'http_error', status);
	}
	const body = decoded as Record<string, unknown>;
	if (typeof body['email'] !== 'string' || typeof body['display_name'] !== 'string') {
		const parsed = parseErrorEnvelope(raw);
		if (parsed.ok) {
			throw new ApiError(parsed.value.error.message, parsed.value.error.code, status);
		}
		throw new ApiError('The server answer held malformed JSON.', 'http_error', status);
	}
	return { email: body['email'], displayName: body['display_name'] };
}

// getAccount runs one account read and decodes its answer. It throws an
// ApiError when the request never leaves or when the server refuses.
async function getAccount(fetchFn: FetchFn, path: string): Promise<Account> {
	let response: Response;
	try {
		response = await fetchFn(path);
	} catch {
		throw new ApiError('The request could not reach the server.', 'network', 0);
	}
	const raw = await response.text();
	if (!response.ok) {
		const parsed = parseErrorEnvelope(raw);
		if (parsed.ok) {
			throw new ApiError(parsed.value.error.message, parsed.value.error.code, response.status);
		}
		throw new ApiError(
			`The server answered ${response.status}.`,
			'http_error',
			response.status
		);
	}
	return parseAccount(raw, response.status);
}

// putName runs one shared name write and decodes its answer. It throws
// an ApiError when the request never leaves or when the server refuses.
async function putName(fetchFn: FetchFn, name: string): Promise<Account> {
	let response: Response;
	try {
		response = await fetchFn(NAME_PATH, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ display_name: name })
		});
	} catch {
		throw new ApiError('The request could not reach the server.', 'network', 0);
	}
	const raw = await response.text();
	if (!response.ok) {
		const parsed = parseErrorEnvelope(raw);
		if (parsed.ok) {
			throw new ApiError(parsed.value.error.message, parsed.value.error.code, response.status);
		}
		throw new ApiError(
			`The server answered ${response.status}.`,
			'http_error',
			response.status
		);
	}
	return parseAccount(raw, response.status);
}

// fetchAccount reads the caller account with its shared name. A guest
// throws with the unauthorized code, so the caller leaves the field
// blank instead of showing another diary name.
export async function fetchAccount(fetchFn: FetchFn): Promise<Account> {
	return getAccount(fetchFn, ACCOUNT_PATH);
}

// saveDisplayName stores one shared name and answers what the server
// kept. An empty name clears the stored value.
export async function saveDisplayName(fetchFn: FetchFn, name: string): Promise<Account> {
	return putName(fetchFn, name);
}

// postJson runs one JSON call and reads its answer. It throws an ApiError
// when the request never leaves, when the answer is not JSON, or when the
// server refuses. A JSON refusal keeps the envelope code, so callers
// branch on the same codes the routes answer with.
async function postJson(fetchFn: FetchFn, path: string, body: unknown): Promise<unknown> {
	let response: Response;
	try {
		response = await fetchFn(path, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(body)
		});
	} catch {
		throw new ApiError('The request could not reach the server.', 'network', 0);
	}
	const raw = await response.text();
	if (!response.ok) {
		const parsed = parseErrorEnvelope(raw);
		if (parsed.ok) {
			throw new ApiError(
				parsed.value.error.message,
				parsed.value.error.code,
				response.status,
				{},
				readRetryAfterSeconds(response)
			);
		}
		throw new ApiError(
			`The server answered ${response.status}.`,
			'http_error',
			response.status,
			{},
			readRetryAfterSeconds(response)
		);
	}
	if (raw.length === 0) return {};
	try {
		return JSON.parse(raw) as unknown;
	} catch {
		throw new ApiError('The server answer held malformed JSON.', 'http_error', response.status);
	}
}

// requestCode asks for a sign-in code at one address. It answers the same
// for known and unknown addresses, because the server never names one.
export async function requestCode(fetchFn: FetchFn, email: string): Promise<void> {
	await postJson(fetchFn, '/api/login/code', { email });
}

// VerifyResult names what one code check decided. A conflict changes
// nothing, so the caller repeats the check with the switch choice.
export type VerifyResult = 'signedin' | 'conflict';

// verifyCode checks one typed code. It returns a conflict instead of
// throwing one, because the conflict is a choice and not a failure.
export async function verifyCode(
	fetchFn: FetchFn,
	email: string,
	code: string,
	choice?: 'switch'
): Promise<VerifyResult> {
	const body = choice === undefined ? { email, code } : { email, code, choice };
	try {
		await postJson(fetchFn, '/api/login/verify', body);
		return 'signedin';
	} catch (error) {
		if (error instanceof ApiError && error.code === DIARY_CONFLICT) return 'conflict';
		throw error;
	}
}

// signOut ends the session on this device through the sign-out route. The
// server revokes the session and mints a fresh guest beside the answer.
export async function signOut(fetchFn: FetchFn): Promise<void> {
	await postJson(fetchFn, SIGNOUT_PATH, {});
}

// ACCOUNT_KEY is the local storage key holding the signed in address. It
// lives beside the session cookie, which JavaScript never reads.
const ACCOUNT_KEY = 'reprise.accountEmail';

// ACCOUNT_EVENT fires on this tab after signing in or out. The storage
// event only reaches other tabs, so the store announces its own change.
export const ACCOUNT_EVENT = 'reprise:account';

// announce tells this tab the stored address changed. A locked window
// object swallows the event, and the next load reads the store itself.
function announce(): void {
	try {
		if (typeof window === 'undefined') return;
		window.dispatchEvent(new CustomEvent(ACCOUNT_EVENT));
	} catch {
		return;
	}
}

// readSignedInEmail returns the stored address, or null on a guest
// device. A locked store reads as a guest, so the page never crashes.
export function readSignedInEmail(): string | null {
	try {
		if (typeof localStorage === 'undefined') return null;
		const raw = localStorage.getItem(ACCOUNT_KEY);
		if (raw === null) return null;
		const address = normalizeEmail(raw);
		return address.length > 0 ? address : null;
	} catch {
		return null;
	}
}

// rememberSignedInEmail stores the address a verify just signed in. A
// locked store keeps the guest label, while the cookie still holds.
export function rememberSignedInEmail(email: string): void {
	try {
		if (typeof localStorage === 'undefined') return;
		localStorage.setItem(ACCOUNT_KEY, normalizeEmail(email));
	} catch {
		return;
	}
	announce();
}

// forgetSignedInEmail drops the stored address after signing out. A
// locked store keeps the old label until the next load clears it.
export function forgetSignedInEmail(): void {
	try {
		if (typeof localStorage === 'undefined') return;
		localStorage.removeItem(ACCOUNT_KEY);
	} catch {
		return;
	}
	announce();
}
