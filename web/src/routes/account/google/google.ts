// Google sign-in pages beside the account page. The start page links
// out to the sign-in route, which redirects to the provider. The
// callback page renders the flag the sign-in route lands on: done,
// conflict, or failed. The conflict choice mirrors the account screen:
// keeping links back to the account screen, and switching retries the
// callback with the switch choice on the same state. The signed in
// address never appears here, because the subject alone keys the
// account.

// START_PATH names the route that redirects to the provider. The
// server holds the state, the nonce and the PKCE verifier, so the
// page carries nothing but this link.
export const START_PATH = '/api/login/google/start';

// CALLBACK_API names the route the provider returns to. The conflict
// retry adds the state with the switch choice.
export const CALLBACK_API = '/api/login/google/callback';

// CALLBACK_PAGE names this outcome page. The server redirects here
// with one flag after the provider answers.
export const CALLBACK_PAGE = '/account/google/callback';

// ACCOUNT_PAGE names the account screen. Keeping a guest diary links
// there, where the sign-in screens live.
export const ACCOUNT_PAGE = '/account';

// GOOGLE_PAGE names the start page. Failures link back here for a
// fresh attempt.
export const GOOGLE_PAGE = '/account/google';

// HEADING names the start page.
export const HEADING = 'Sign in with Google';

// SUB says what signing in does on this device.
export const SUB = 'Google confirms it is you. The diary on this device stays until you switch.';

// CONTINUE names the button that starts the flow.
export const CONTINUE = 'Continue with Google';

// BACK names the link to the account screen.
export const BACK = 'Back to account';

// DONE confirms the device signed in through Google.
export const DONE = 'Signed in with Google on this device.';

// VIEW_ACCOUNT names the link that opens the account screen.
export const VIEW_ACCOUNT = 'View your account';

// CONFLICT_HEADING names the choice between two diaries.
export const CONFLICT_HEADING = 'This device already holds takes';

// CONFLICT_BODY says why signing in pauses here.
export const CONFLICT_BODY =
	'Your account lives elsewhere, and this device holds takes it has never seen. ' +
	'Keep this device as it is, or switch it to your account.';

// KEEP names the choice that leaves the device alone.
export const KEEP = "Keep this device's diary";

// SWITCH names the choice that joins the account.
export const SWITCH = 'Switch to your account';

// FAILED answers a sign-in the provider or the server refused.
export const FAILED = 'Google sign-in did not work. Try again.';

// TRY_AGAIN names the link that restarts the flow.
export const TRY_AGAIN = 'Try Google sign-in again';

// Outcome names what the callback flag decided. Done signed in,
// conflict asks for a diary choice, failed needs a retry, and start
// means no flag arrived at all.
export type Outcome = 'done' | 'conflict' | 'failed' | 'start';

// queryFlags snapshots one query string into a plain record. One
// snapshot keeps every flag read in the same shape. The first value
// wins on a repeated key, matching a single read.
function queryFlags(search: string): Record<string, string> {
	const params = new URLSearchParams(search.startsWith('?') ? search : `?${search}`);
	const flags: Record<string, string> = {};
	for (const [key, value] of params.entries()) {
		if (flags[key] === undefined) flags[key] = value;
	}
	return flags;
}

// outcome reads one outcome from a query string. Unknown flags read
// as a fresh start, so a stray link never strands the page.
export function outcome(search: string): Outcome {
	const flags = queryFlags(search);
	if (flags['done'] === '1') return 'done';
	if (flags['conflict'] === '1') return 'conflict';
	if (flags['error'] !== undefined) return 'failed';
	return 'start';
}

// callbackState reads the state the conflict retry needs. It returns
// null without one, and the switch link stays hidden then.
export function callbackState(search: string): string | null {
	const state = queryFlags(search)['state'];
	return state === undefined || state.length === 0 ? null : state;
}

// switchHref builds the switch retry link for one state. The retry
// carries no code, because the first callback already spent it.
export function switchHref(state: string): string {
	return `${CALLBACK_API}?state=${encodeURIComponent(state)}&choice=switch`;
}
