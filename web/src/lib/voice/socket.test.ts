// The socket test loads recorded frames. These names cover that read
// without node types on disk.
declare module 'node:fs' {
	export function readFileSync(path: string, encoding: 'utf8'): string;
}

declare const process: {
	cwd(): string;
};

import { readFileSync } from 'node:fs';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { closeSession } from './session-calls';
import { VoiceSocket, type SocketHandle } from './socket';
import { bytesToPcm16, encodeBase64, floatToPcm16, pcm16ToBytes, pcm16ToFloat } from './pcm';
import { drainHostBlock } from './take';
import type { SessionConfig } from './session';

class FakeHandle implements SocketHandle {
	sent: string[] = [];
	openTasks: Array<() => void> = [];
	messageTasks: Array<(text: string) => void> = [];
	closeTasks: Array<() => void> = [];
	closed = 0;

	send(text: string): void {
		this.sent.push(text);
	}
	close(): void {
		this.closed += 1;
	}
	onOpen(task: () => void): void {
		this.openTasks.push(task);
	}
	onMessage(task: (text: string) => void): void {
		this.messageTasks.push(task);
	}
	onClose(task: () => void): void {
		this.closeTasks.push(task);
	}
	open(): void {
		for (const task of this.openTasks) task();
	}
	receive(text: string): void {
		for (const task of this.messageTasks) task(text);
	}
}

const CONFIG: SessionConfig = { system_prompt: 'prompt', greeting: 'hello', keyterms: ['Mara'] };

interface ProviderFrame {
	type: string;
	session_id?: string;
	config?: { id?: string };
}

function steadyProviderFrames(): { ready: ProviderFrame; updated: ProviderFrame; other: ProviderFrame } {
	const raw = readFileSync(process.cwd() + '/../testdata/sessions/steady/events.json', 'utf8');
	const rows = JSON.parse(raw) as Array<{ event: ProviderFrame }>;
	const ready = rows.find((row) => row.event.type === 'session.ready')?.event;
	const updated = rows.find((row) => row.event.type === 'session.updated')?.event;
	const other = rows.find((row) => row.event.type === 'reply.started')?.event;
	if (ready === undefined || updated === undefined || other === undefined) {
		throw new Error('the recorded frames are missing a ready, updated, or other event');
	}
	if (ready.session_id === undefined || ready.session_id === '' || ready.session_id !== updated.config?.id) {
		throw new Error('the recorded ready and updated frames do not share a provider id');
	}
	return { ready, updated, other };
}

afterEach(() => {
	vi.unstubAllGlobals();
});

function events() {
	return {
		hostAudio: [] as Float32Array[],
		done: [] as boolean[],
		user: [] as Array<{ text: string; itemId: string }>,
		host: [] as Array<{ text: string; startMs: number; endMs: number }>,
		ended: 0
	};
}

function wire(handle: FakeHandle) {
	const seen = events();
	const socket = new VoiceSocket(
		handle,
		CONFIG,
		{
			onHostAudio: (samples) => seen.hostAudio.push(samples),
			onReplyDone: (interrupted) => seen.done.push(interrupted),
			onUserTranscript: (text, itemId) => seen.user.push({ text, itemId }),
			onHostTranscript: (text, startMs, endMs) => seen.host.push({ text, startMs, endMs }),
			onEnded: () => {
				seen.ended += 1;
			}
		}
	);
	return { seen, socket };
}

describe('VoiceSocket', () => {
	it('sends the setup first with the stored config', () => {
		const handle = new FakeHandle();
		wire(handle);
		handle.open();
		expect(handle.sent.length).toBe(1);
		const setup = JSON.parse(handle.sent[0]) as Record<string, unknown>;
		expect(setup['type']).toBe('session.update');
		expect(setup['system_prompt']).toBe('prompt');
		expect(setup['greeting']).toBe('hello');
		expect(setup['keyterms']).toEqual(['Mara']);
	});

	it('routes host audio through a base64 round trip', () => {
		const handle = new FakeHandle();
		const { seen, socket } = wire(handle);
		handle.open();
		const frames = new Float32Array([0, 0.5, -0.5]);
		socket.sendAudio(pcm16ToBytes(floatToPcm16(frames)));
		const sent = JSON.parse(handle.sent[1]) as { type: string; audio: string };
		expect(sent.type).toBe('input.audio');
		handle.receive(JSON.stringify({ type: 'reply.audio', audio: sent.audio }));
		expect(seen.hostAudio.length).toBe(1);
		expect(seen.hostAudio[0].length).toBe(3);
		expect(seen.hostAudio[0][1]).toBeCloseTo(0.5, 3);
	});

	it('reports reply done with its interrupt flag', () => {
		const handle = new FakeHandle();
		const { seen } = wire(handle);
		handle.open();
		handle.receive(JSON.stringify({ type: 'reply.done', interrupted: true }));
		handle.receive(JSON.stringify({ type: 'reply.done' }));
		expect(seen.done).toEqual([true, false]);
	});

	it('routes both transcript shapes', () => {
		const handle = new FakeHandle();
		const { seen } = wire(handle);
		handle.open();
		handle.receive(JSON.stringify({ type: 'transcript.user', text: 'hi', item_id: 'u1' }));
		handle.receive(
			JSON.stringify({ type: 'transcript.agent.delta', text: 'hey', start_ms: 10, end_ms: 40 })
		);
		expect(seen.user).toEqual([{ text: 'hi', itemId: 'u1' }]);
		expect(seen.host).toEqual([{ text: 'hey', startMs: 10, endMs: 40 }]);
	});

	it('drops malformed frames without failing the take', () => {
		const handle = new FakeHandle();
		const { seen } = wire(handle);
		handle.open();
		handle.receive('not json');
		handle.receive(JSON.stringify({ type: 'reply.audio' }));
		handle.receive(JSON.stringify({ type: 'reply.audio', audio: '!!!' }));
		handle.receive(JSON.stringify({ type: 'unknown.shape', x: 1 }));
		expect(seen.hostAudio).toEqual([]);
		expect(seen.done).toEqual([]);
	});

	it('counts audio dropped before the open', () => {
		const handle = new FakeHandle();
		const { socket } = wire(handle);
		socket.sendAudio(new Uint8Array([1, 2]));
		expect(socket.dropped).toBe(1);
		expect(handle.sent).toEqual([]);
	});

	it('sends the close message once across many end calls', async () => {
		const handle = new FakeHandle();
		const { seen, socket } = wire(handle);
		handle.open();
		const first = socket.end();
		const second = socket.end();
		const ends = handle.sent.filter((text) => JSON.parse(text).type === 'session.end');
		expect(ends.length).toBe(1);
		expect(socket.ending).toBe(true);
		handle.receive(JSON.stringify({ type: 'session.ended' }));
		await first;
		await second;
		expect(seen.ended).toBe(1);
		expect(handle.closed).toBe(1);
	});

	it('drops audio once ending starts', () => {
		const handle = new FakeHandle();
		const { socket } = wire(handle);
		handle.open();
		void socket.end();
		socket.sendAudio(new Uint8Array([1, 2]));
		expect(socket.dropped).toBe(1);
	});

	it('resolves an end that arrives after the ended frame', async () => {
		const handle = new FakeHandle();
		const { socket } = wire(handle);
		handle.open();
		const first = socket.end();
		handle.receive(JSON.stringify({ type: 'session.ended' }));
		await first;
		await socket.end();
		const ends = handle.sent.filter((text) => JSON.parse(text).type === 'session.end');
		expect(ends.length).toBe(1);
	});

	it('keeps the provider id from a recorded ready frame', () => {
		const { ready, other } = steadyProviderFrames();
		const handle = new FakeHandle();
		const { socket } = wire(handle);
		handle.open();
		handle.receive(JSON.stringify(other));
		expect(socket.providerSessionId).toBe('');
		handle.receive(JSON.stringify(ready));
		const learned = socket.providerSessionId;
		expect(learned).toBe(ready.session_id);
		handle.receive(JSON.stringify({ type: 'session.ready', session_id: '' }));
		handle.receive(JSON.stringify({ type: 'session.updated', config: { id: '' } }));
		handle.receive(JSON.stringify({ type: 'session.updated', config: { id: 'other-id' } }));
		handle.receive(JSON.stringify(other));
		expect(socket.providerSessionId).toBe(learned);
	});

	it('keeps the provider id from a recorded updated frame', () => {
		const { updated, other } = steadyProviderFrames();
		const handle = new FakeHandle();
		const { socket } = wire(handle);
		handle.open();
		handle.receive(JSON.stringify(updated));
		const learned = socket.providerSessionId;
		expect(learned).toBe(updated.config?.id);
		expect(learned).not.toBe('');
		handle.receive(JSON.stringify({ type: 'session.ready' }));
		handle.receive(JSON.stringify({ type: 'session.updated', config: {} }));
		handle.receive(JSON.stringify(other));
		expect(socket.providerSessionId).toBe(learned);
	});

	it('posts the learned id when a connected take closes', async () => {
		const { ready, updated, other } = steadyProviderFrames();
		const handle = new FakeHandle();
		const { socket } = wire(handle);
		handle.open();
		handle.receive(JSON.stringify(updated));
		handle.receive(JSON.stringify(ready));
		handle.receive(JSON.stringify(other));
		const learned = socket.providerSessionId;
		expect(learned).not.toBe('');
		const stub = vi.fn(async () => {
			return new Response(JSON.stringify({ ok: true }), {
				status: 200,
				headers: { 'content-type': 'application/json' }
			});
		});
		vi.stubGlobal('fetch', stub);
		await closeSession('diary-1', learned);
		const call = stub.mock.calls[0] as unknown[] | undefined;
		const init = call?.[1] as RequestInit | undefined;
		expect(init?.body).toBe(JSON.stringify({ provider_session_id: learned }));
		expect(init?.body).not.toBe(JSON.stringify({ provider_session_id: '' }));
	});

	it('settles a pre-open end without starting a session', async () => {
		const handle = new FakeHandle();
		const { socket } = wire(handle);
		const ended = socket.end();
		handle.open();
		await Promise.race([
			ended,
			new Promise<never>((_, reject) => {
				setTimeout(() => reject(new Error('pre-open end never settled')), 2000);
			})
		]);
		const types = handle.sent.map((text) => JSON.parse(text).type);
		expect(types).not.toContain('session.update');
		expect(types).not.toContain('session.end');
		expect(handle.closed).toBe(1);
		await socket.end();
	});
});

describe('provider audio encoding', () => {
	it('keeps 24 kHz PCM16 intact through the wire', () => {
		const frames = new Float32Array(480);
		for (let i = 0; i < frames.length; i += 1) frames[i] = Math.sin(i / 10) * 0.4;
		const bytes = pcm16ToBytes(floatToPcm16(frames));
		expect(encodeBase64(bytes).length).toBeGreaterThan(0);
	});
});

describe('live provider audio shape', () => {
	it('routes host audio carried under the data key', () => {
		const handle = new FakeHandle();
		const { seen } = wire(handle);
		handle.open();
		const frames = new Float32Array([0, 0.5, -0.5]);
		const payload = encodeBase64(pcm16ToBytes(floatToPcm16(frames)));
		handle.receive(JSON.stringify({ type: 'reply.audio', data: payload }));
		expect(seen.hostAudio.length).toBe(1);
		expect(seen.hostAudio[0].length).toBe(3);
		expect(seen.hostAudio[0][1]).toBeCloseTo(0.5, 3);
	});

	it('keeps the host stem byte exact from the data key to the drain', () => {
		const handle = new FakeHandle();
		const { seen } = wire(handle);
		handle.open();
		const provider = new Float32Array(480);
		for (let i = 0; i < provider.length; i += 1) provider[i] = 0.3 * Math.sin(i / 10);
		const providerBytes = pcm16ToBytes(floatToPcm16(provider));
		handle.receive(JSON.stringify({ type: 'reply.audio', data: encodeBase64(providerBytes) }));
		expect(seen.hostAudio.length).toBe(1);
		const stored = drainHostBlock(seen.hostAudio[0]);
		expect([...stored]).toEqual([...providerBytes]);
	});

	it('keeps a loud host stem byte exact from the data key to the drain', () => {
		const handle = new FakeHandle();
		const { seen } = wire(handle);
		handle.open();
		const provider = new Float32Array(480);
		for (let i = 0; i < provider.length; i += 1) provider[i] = 0.99 * Math.sin(i / 10);
		const providerBytes = pcm16ToBytes(floatToPcm16(provider));
		handle.receive(JSON.stringify({ type: 'reply.audio', data: encodeBase64(providerBytes) }));
		expect(seen.hostAudio.length).toBe(1);
		const stored = drainHostBlock(seen.hostAudio[0]);
		expect([...stored]).toEqual([...providerBytes]);
	});

	it('round trips every int16 value from the wire to the drain with no loss', () => {
		const frames = new Int16Array(65536);
		for (let value = -32768; value <= 32767; value += 1) frames[value + 32768] = value;
		const stored = drainHostBlock(pcm16ToFloat(bytesToPcm16(pcm16ToBytes(frames))));
		const back = bytesToPcm16(stored);
		let mismatches = 0;
		for (let i = 0; i < back.length; i += 1) {
			if (back[i] !== frames[i]) mismatches += 1;
		}
		expect(mismatches).toBe(0);
		expect(back[65535]).toBe(32767);
		expect(back[0]).toBe(-32768);
	});
});
