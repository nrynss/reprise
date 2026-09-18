// The session the broker starts. The shapes mirror the broker response by
// hand, and the parser pins that mirror. A drifted field fails loudly here
// instead of opening a socket with the wrong config.

// SessionConfig carries the host setup the socket forwards unchanged.
export interface SessionConfig {
	system_prompt: string;
	greeting: string;
	keyterms: string[];
}

// SessionStart carries one minted session: the provider token beside the
// host setup and the ids the pages hand back when the take ends.
export interface SessionStart {
	session_id: string;
	episode_id: string;
	token: string;
	expires_in_seconds: number;
	max_session_duration_seconds: number;
	config: SessionConfig;
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function readString(record: Record<string, unknown>, name: string): string {
	const value = record[name];
	if (typeof value !== 'string' || value.length === 0) {
		throw new Error(`the session body carries no ${name} string`);
	}
	return value;
}

function readNumber(record: Record<string, unknown>, name: string): number {
	const value = record[name];
	if (typeof value !== 'number' || !Number.isFinite(value)) {
		throw new Error(`the session body carries no ${name} number`);
	}
	return value;
}

/** Parse one broker session body. It throws and never returns a half shape. */
export function parseSessionStart(text: string): SessionStart {
	let value: unknown;
	try {
		value = JSON.parse(text);
	} catch {
		throw new Error('the session body holds no JSON object');
	}
	if (!isRecord(value)) throw new Error('the session body holds no JSON object');
	const rawConfig = value['config'];
	if (!isRecord(rawConfig)) throw new Error('the session body carries no config object');
	const rawKeyterms = rawConfig['keyterms'];
	if (!Array.isArray(rawKeyterms) || rawKeyterms.some((entry) => typeof entry !== 'string')) {
		throw new Error('the session body carries no keyterms list');
	}
	return {
		session_id: readString(value, 'session_id'),
		episode_id: readString(value, 'episode_id'),
		token: readString(value, 'token'),
		expires_in_seconds: readNumber(value, 'expires_in_seconds'),
		max_session_duration_seconds: readNumber(value, 'max_session_duration_seconds'),
		config: {
			system_prompt: readString(rawConfig, 'system_prompt'),
			greeting: readString(rawConfig, 'greeting'),
			keyterms: [...rawKeyterms] as string[]
		}
	};
}

/** Build the provider socket address for one minted token. */
export function socketUrl(token: string): string {
	return `wss://agents.assemblyai.com/v1/ws?token=${encodeURIComponent(token)}`;
}
