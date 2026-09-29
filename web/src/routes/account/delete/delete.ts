// Deletion client behind the delete page. The shapes mirror the delete
// route by hand, so drift fails in the tests beside this file instead
// of hiding. Screens branch on the envelope codes and never on wording.
import { ApiError } from '@nrynss/chaaya/api';
import { parseErrorEnvelope } from '@nrynss/chaaya/wire';
import type { FetchFn } from '../account';

// DELETE_PATH names the route that removes the account. The server
// starts a deletion and answers the deletion id at once.
export const DELETE_PATH = '/api/account/delete';

// INVALID_CODE answers a refused code. It never says which guard
// tripped, so the page never names an account.
export const INVALID_CODE = 'invalid_code';

// INVALID_REQUEST answers a malformed body.
export const INVALID_REQUEST = 'invalid_request';

// DELETE_HEADING names the page. Every screen on the page keeps it.
export const DELETE_HEADING = 'Delete your account';

// DELETE_SUB says what deletion does on every device.
export const DELETE_SUB = 'This removes your takes, ends your sessions, and drops your address.';

// DELETE_BODY says what goes and what stays. Nothing stays, so the
// screen says so in plain words.
export const DELETE_BODY =
	'Deletion removes every take on your account and signs every device out. ' +
	'Your address leaves with them. Nothing stays behind.';

// DELETE_CONFIRM names the button that removes everything.
export const DELETE_CONFIRM = 'Delete everything';

// DELETE_CANCEL names the button that keeps the account.
export const DELETE_CANCEL = 'Keep my account';

// DELETE_BACK names the button that returns to the account page.
export const DELETE_BACK = 'Back to account';

// DELETE_DONE confirms the account is gone and a fresh diary starts.
export const DELETE_DONE = 'Your account is gone. This device starts a fresh diary.';

// DELETE_CODE_HELP says the code must be fresh.
export const DELETE_CODE_HELP = 'Type the fresh code from your mail. It expires in 10 minutes.';

// DELETE_FAILED answers a refusal the page has no words for.
export const DELETE_FAILED = 'That did not work. Try again.';

// postDelete runs one deletion call and reads its answer. It throws an
// ApiError when the request never leaves, when the answer is not JSON,
// or when the server refuses. A JSON refusal keeps the envelope code,
// so the page branches on the same codes the route answers with.
async function postDelete(fetchFn: FetchFn, body: unknown): Promise<{ jobId: string }> {
	let response: Response;
	try {
		response = await fetchFn(DELETE_PATH, {
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
			throw new ApiError(parsed.value.error.message, parsed.value.error.code, response.status);
		}
		throw new ApiError(`The server answered ${response.status}.`, 'http_error', response.status);
	}
	try {
		const parsed = JSON.parse(raw) as { job_id?: unknown };
		if (typeof parsed.job_id !== 'string' || parsed.job_id.length === 0) {
			throw new Error('missing job id');
		}
		return { jobId: parsed.job_id };
	} catch {
		throw new ApiError('The server answer held malformed JSON.', 'http_error', response.status);
	}
}

// deleteAccount removes the account behind one fresh code. It resolves
// with the deletion id once the server starts the work.
export async function deleteAccount(
	fetchFn: FetchFn,
	email: string,
	code: string
): Promise<string> {
	const answer = await postDelete(fetchFn, { email, code });
	return answer.jobId;
}
