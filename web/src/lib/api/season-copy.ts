// The server copies the starter season into a new visitor's diary on the
// welcome read. It copies only while the visitor holds no episode, so the
// read must land before the first list and before the first take. A visitor
// who opens the gallery or the record page first would otherwise never get
// the season, and the host would open with no callback.
//
// Every page asks once per load and shares the answer. The read never
// throws and never holds a page for long: a refusal, a network error or a
// slow answer all resolve, and the page carries on with what it has.

// SEASON_COPY_TIMEOUT_MS caps how long a page waits on the copy. The copy
// is a few row inserts, so a longer wait means the read is stuck.
export const SEASON_COPY_TIMEOUT_MS = 4000;

type FetchFn = (url: string, init?: RequestInit) => Promise<Response>;

let pending: Promise<void> | null = null;

// ensureSeasonCopy asks the server for the visitor's season copy once per
// page load. Later calls return the same promise. It always resolves.
export function ensureSeasonCopy(fetchFn: FetchFn = fetch): Promise<void> {
	if (pending === null) {
		pending = askForCopy(fetchFn);
	}
	return pending;
}

async function askForCopy(fetchFn: FetchFn): Promise<void> {
	const controller = new AbortController();
	const timer = setTimeout(() => controller.abort(), SEASON_COPY_TIMEOUT_MS);
	try {
		const response = await fetchFn('/api/welcome', { signal: controller.signal });
		// The body is the welcome teaser, which only the welcome page reads.
		await response.body?.cancel();
	} catch {
		// A failed copy leaves the visitor with an empty season, which every
		// page already handles.
	} finally {
		clearTimeout(timer);
	}
}

// resetSeasonCopy forgets the shared answer, so each test starts fresh.
export function resetSeasonCopy(): void {
	pending = null;
}
