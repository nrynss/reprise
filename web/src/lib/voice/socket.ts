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

// VoiceEvents reports what the provider said. Host audio arrives as floats
// at the provider rate. The page plays them and stores them.
export interface VoiceEvents {
	onHostAudio(samples: Float32Array): void;
	onReplyDone(interrupted: boolean): void;
	onUserTranscript(text: string, itemId: string): void;
	onHostTranscript(text: string, startMs: number, endMs: number): void;
	onEnded(): void;
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

	constructor(handle: SocketHandle, config: SessionConfig, events: VoiceEvents) {
		this.handle = handle;
		this.config = config;
		this.events = events;
		this.handle.onOpen(() => {
			if (this.endSent) {
				this.settleEnd();
				return;
			}
			this.opened = true;
			this.handle.send(
				JSON.stringify({
					type: 'session.update',
					system_prompt: config.system_prompt,
					greeting: config.greeting,
					keyterms: config.keyterms,
					tools: []
				})
			);
		});
		this.handle.onMessage((text: string) => this.route(text));
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
				this.handle.send(JSON.stringify({ type: 'session.end' }));
			}
		}
		return waited;
	}

	// rememberProviderId keeps the first non-empty provider id. A later
	// frame must not clear an id the socket already learned.
	private rememberProviderId(value: unknown): void {
		if (this.learnedProviderId !== '') return;
		if (typeof value !== 'string' || value === '') return;
		this.learnedProviderId = value;
	}

	private settleEnd(): void {
		if (this.endedOk) return;
		this.endedOk = true;
		const waiters = this.endWaiters;
		this.endWaiters = [];
		for (const resolve of waiters) resolve();
		this.handle.close();
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
				break;
			}
			case 'session.updated': {
				// The same id also arrives under the config. Keep the first one learned.
				const config = record['config'];
				if (typeof config === 'object' && config !== null && !Array.isArray(config)) {
					this.rememberProviderId((config as Record<string, unknown>)['id']);
				}
				break;
			}
			default:
				break;
		}
	}
}
