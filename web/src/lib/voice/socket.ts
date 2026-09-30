// The duplex loop to the voice provider. The page owns the audio clock and
// the stems. This module owns the message order on the socket and nothing
// else, so tests drive it through a fake handle with no network.

// SocketHandle hides the transport. The browser wraps a WebSocket. Tests
// and the mock harness hand in a fake with the same four calls.
export interface SocketHandle {
	send(text: string): void;
	close(): void;
	onOpen(task: () => void): void;
	onMessage(task: (text: string) => void): void;
	onClose(task: () => void): void;
}

/** Wrap a browser WebSocket in the handle the loop needs. */
export function browserSocket(url: string): SocketHandle {
	const socket = new WebSocket(url);
	return {
		send: (text: string) => socket.send(text),
		close: () => socket.close(),
		onOpen: (task: () => void) => {
			socket.addEventListener('open', task);
		},
		onMessage: (task: (text: string) => void) => {
			socket.addEventListener('message', (event: MessageEvent) => {
				if (typeof event.data === 'string') task(event.data);
			});
		},
		onClose: (task: () => void) => {
			socket.addEventListener('close', task);
		}
	};
}

import type { SessionConfig } from './session';
import { bytesToPcm16, decodeBase64, encodeBase64, pcm16ToFloat } from './pcm';

// The setup refusal the start control shows when the provider does not take
// the setup it was sent. One message names the retry, so every path reads
// the same words.
export const SETUP_FAILURE = 'The host did not accept its setup. Press start to try again.';

// SessionErrorDetail carries one provider error for the notice. The code
// names the failure, the message phrases it, and the param points at the
// field the provider refused.
export interface SessionErrorDetail {
	code: string;
	message: string;
	param: string;
}

// A guest telling a story pauses to find a word. The provider's default ends the turn after 600 ms, which cut guests off mid-sentence, so the host waits longer.
export const MIN_SILENCE_MS = 1500;
export const MAX_SILENCE_MS = 4000;

// sessionUpdateFrame builds the setup frame in the shape the provider takes.
// The setup nests under session, with turn detection and keyterms under
// input and the picked voice under output. The provider reads a missing
// keyterms list as no keyterms.
export function sessionUpdateFrame(config: SessionConfig): Record<string, unknown> {
	const inner: Record<string, unknown> = {
		system_prompt: config.system_prompt,
		greeting: config.greeting,
		tools: [],
		input: {
			turn_detection: { min_silence: MIN_SILENCE_MS, max_silence: MAX_SILENCE_MS }
		},
		output: { voice: config.voice }
	};
	if (config.keyterms.length > 0) {
		(inner['input'] as Record<string, unknown>)['keyterms'] = [...config.keyterms];
	}
	return { type: 'session.update', session: inner };
}

// VoiceEvents reports what the provider said. Host audio arrives as floats
// at the provider rate. The page plays them and stores them. The provider id
// callback fires once with the first id the socket learns, so the page can
// store it while the take still runs instead of only at the end.
export interface VoiceEvents {
	onHostAudio(samples: Float32Array): void;
	onReplyDone(interrupted: boolean): void;
	onUserTranscript(text: string, itemId: string): void;
	onHostTranscript(text: string, startMs: number, endMs: number): void;
	onEnded(): void;
	onProviderId?(id: string): void;
	onSessionError?(detail: SessionErrorDetail): void;
}

// VoiceSocket runs one take over one handle. It sends the setup first, then
// routes every provider frame to the events above. Ending goes through end,
// which sends the close message once and resolves when the provider answers.
export class VoiceSocket {
	private readonly handle: SocketHandle;
	private readonly config: SessionConfig;
	private readonly events: VoiceEvents;
	private opened = false;
	private endSent = false;
	private endedOk = false;
	private learnedProviderId = '';
	private endWaiters: Array<() => void> = [];
	private droppedAudio = 0;
	// readySeen marks the first session.ready. An error before it means the
	// setup never landed, so the take must not go live.
	private readySeen = false;
	// setupSettled latches the first setup verdict. A later updated or error
	// frame only records, because the take already runs or already failed.
	private setupSettled = false;
	private setupResolve: (() => void) | null = null;
	private setupReject: ((error: Error) => void) | null = null;
	private readonly setupPromise: Promise<void>;
	private lastSetupError: SessionErrorDetail | null = null;

	constructor(handle: SocketHandle, config: SessionConfig, events: VoiceEvents) {
		this.handle = handle;
		this.config = config;
		this.events = events;
		this.setupPromise = new Promise<void>((resolve, reject) => {
			this.setupResolve = resolve;
			this.setupReject = reject;
		});
		this.handle.onOpen(() => {
			if (this.endSent) {
				this.settleEnd();
				return;
			}
			this.opened = true;
			this.handle.send(JSON.stringify(sessionUpdateFrame(config)));
		});
		this.handle.onMessage((text: string) => this.route(text));
		// A transport close inside the setup window must fail the start, not
		// strand it. The end control only acts from live, so without this the
		// waiter hangs and the guest must reload.
		this.handle.onClose(() => {
			if (!this.setupSettled) this.failSetup();
		});
	}

	/** How many audio blocks never reached a closed socket. */
	get dropped(): number {
		return this.droppedAudio;
	}

	/** True once the close message has gone. */
	get ending(): boolean {
		return this.endSent;
	}

	/** The provider session id from the socket, or empty when none has arrived. */
	get providerSessionId(): string {
		return this.learnedProviderId;
	}

	/** The last provider error, or null when no session.error has arrived. */
	get setupError(): SessionErrorDetail | null {
		return this.lastSetupError;
	}

	/**
	 * Wait for the provider to echo the sent setup. It resolves when the
	 * echoed prompt and greeting match what was sent. It rejects with the
	 * setup refusal when they differ, or when an error arrives before the
	 * session is ready. The socket already sent the close and shut itself
	 * by then, so the caller only shows the message.
	 */
	waitForSetup(): Promise<void> {
		return this.setupPromise;
	}

	/** Send one 24 kHz PCM16 block. A block before the open drops and counts. */
	sendAudio(pcm: Uint8Array): void {
		if (!this.opened || this.endSent) {
			this.droppedAudio += 1;
			return;
		}
		this.handle.send(JSON.stringify({ type: 'input.audio', audio: encodeBase64(pcm) }));
	}

	/**
	 * End the session and wait for the provider answer. The first call sends
	 * the close message. Every later call waits on the same answer, so the
	 * end control, the page hide and the destroy path still send exactly once.
	 * A send that throws did not leave, so a later call tries once more.
	 * A call after the answer already arrived resolves at once, so ending a
	 * take the page already closed never hangs. A call before the open sends
	 * nothing, and its waiter settles when the open short-circuits instead of
	 * starting a session, so no close message ever follows.
	 */
	end(): Promise<void> {
		if (this.endedOk) return Promise.resolve();
		const waited = new Promise<void>((resolve) => {
			this.endWaiters.push(resolve);
		});
		if (!this.endSent) {
			this.endSent = true;
			if (this.opened) {
				try {
					this.handle.send(JSON.stringify({ type: 'session.end' }));
				} catch {
					this.endSent = false;
				}
			}
		}
		return waited;
	}

	// rememberProviderId keeps the first non-empty provider id. A later
	// frame must not clear an id the socket already learned. Learning it
	// reports it at once, so the page stores it while the take still runs.
	private rememberProviderId(value: unknown): void {
		if (this.learnedProviderId !== '') return;
		if (typeof value !== 'string' || value === '') return;
		this.learnedProviderId = value;
		this.events.onProviderId?.(value);
	}

	private settleEnd(): void {
		if (this.endedOk) return;
		this.endedOk = true;
		const waiters = this.endWaiters;
		this.endWaiters = [];
		for (const resolve of waiters) resolve();
		this.handle.close();
	}

	// verifySetup compares one echoed config with what was sent. A frame
	// with neither field is an id carrier, not a verdict, so it passes
	// through. A matching echo lets the take go live. A mismatch ends the
	// take before it starts, because the host would run without its brief.
	private verifySetup(config: Record<string, unknown>): void {
		if (this.setupSettled) return;
		const prompt = config['system_prompt'];
		const greeting = config['greeting'];
		if (typeof prompt !== 'string' && typeof greeting !== 'string') return;
		if (prompt === this.config.system_prompt && greeting === this.config.greeting) {
			this.setupSettled = true;
			this.setupResolve?.();
			return;
		}
		console.error(
			`voice setup mismatch prompt=${prompt === this.config.system_prompt} greeting=${greeting === this.config.greeting}`
		);
		this.failSetup();
	}

	// failSetup ends a take whose setup never landed. The close message goes
	// out once, the handle shuts, and the waiter reads the one refusal.
	private failSetup(): void {
		if (this.setupSettled) return;
		this.setupSettled = true;
		try {
			this.handle.send(JSON.stringify({ type: 'session.end' }));
		} catch {
			// The close below still runs, so a dead socket fails loudly too.
		}
		this.handle.close();
		this.setupReject?.(new Error(SETUP_FAILURE));
	}

	private route(text: string): void {
		let message: unknown;
		try {
			message = JSON.parse(text);
		} catch {
			return;
		}
		if (typeof message !== 'object' || message === null || Array.isArray(message)) return;
		const record = message as Record<string, unknown>;
		switch (record['type']) {
			case 'reply.audio': {
				// The provider carries host audio under data. Older doubles sent
				// it under audio, so the loop accepts both keys for now.
				const raw = typeof record['data'] === 'string' ? record['data'] : record['audio'];
				if (typeof raw !== 'string') return;
				let bytes: Uint8Array;
				try {
					bytes = decodeBase64(raw);
				} catch {
					return;
				}
				this.events.onHostAudio(pcm16ToFloat(bytesToPcm16(bytes)));
				break;
			}
			case 'reply.done': {
				this.events.onReplyDone(record['interrupted'] === true);
				break;
			}
			case 'transcript.user': {
				if (typeof record['text'] === 'string' && typeof record['item_id'] === 'string') {
					this.events.onUserTranscript(record['text'], record['item_id']);
				}
				break;
			}
			case 'transcript.agent.delta': {
				if (
					typeof record['text'] === 'string' &&
					typeof record['start_ms'] === 'number' &&
					typeof record['end_ms'] === 'number'
				) {
					this.events.onHostTranscript(record['text'], record['start_ms'], record['end_ms']);
				}
				break;
			}
			case 'session.ended': {
				this.endedOk = true;
				const waiters = this.endWaiters;
				this.endWaiters = [];
				for (const resolve of waiters) resolve();
				this.handle.close();
				this.events.onEnded();
				break;
			}
			case 'session.ready': {
				// The provider names the session here. A later frame must not clear it.
				this.rememberProviderId(record['session_id']);
				this.readySeen = true;
				break;
			}
			case 'session.updated': {
				// The same id also arrives under the config. Keep the first one learned.
				const config = record['config'];
				if (typeof config === 'object' && config !== null && !Array.isArray(config)) {
					this.rememberProviderId((config as Record<string, unknown>)['id']);
					this.verifySetup(config as Record<string, unknown>);
				}
				break;
			}
			case 'session.error': {
				const detail = readErrorDetail(record);
				this.lastSetupError = detail;
				console.error(
					`voice session error code=${detail.code} message=${detail.message} param=${detail.param}`
				);
				this.events.onSessionError?.(detail);
				if (!this.setupSettled && !this.readySeen) {
					this.failSetup();
				}
				break;
			}
			default:
				break;
		}
	}
}

// readErrorDetail pulls the provider error triple off one frame. A missing
// field reads empty, so the notice still names the failure when the provider
// sends only a code.
function readErrorDetail(record: Record<string, unknown>): SessionErrorDetail {
	const text = (name: string): string => {
		const value = record[name];
		return typeof value === 'string' ? value : '';
	};
	return { code: text('code'), message: text('message'), param: text('param') };
}
