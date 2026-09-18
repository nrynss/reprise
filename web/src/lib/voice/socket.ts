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
	private endWaiters: Array<() => void> = [];
	private droppedAudio = 0;

	constructor(handle: SocketHandle, config: SessionConfig, events: VoiceEvents) {
		this.handle = handle;
		this.config = config;
		this.events = events;
		this.handle.onOpen(() => {
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
	 * take the page already closed never hangs.
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
				if (typeof record['audio'] !== 'string') return;
				let bytes: Uint8Array;
				try {
					bytes = decodeBase64(record['audio']);
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
			default:
				break;
		}
	}
}
