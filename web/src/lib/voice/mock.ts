// The test double for the voice provider and the upload server. The record
// page uses it under the mock query flag, so the take runs with generated
// input and no fixtures. Production code never imports this module.

import type { SocketHandle } from './socket';
import { encodeBase64, floatToPcm16, pcm16ToBytes } from './pcm';

// MockScript sets what the fake provider says. Audio arrives as 24 kHz
// floats. The reply fires after the counted input block, so a test drives
// one interruption on purpose.
export interface MockScript {
	greetingText: string;
	greetingAudio: Float32Array;
	replyAfterBlocks: number;
	replyAudio: Float32Array;
	replyText: string;
	interrupted: boolean;
}

// MockSocketHandle stands in for the provider socket. It answers the setup
// with the greeting, answers counted input blocks with one reply, and
// answers the close message with the ended frame. Every received type lands
// in the log, so a test counts close messages exactly.
export class MockSocketHandle implements SocketHandle {
	readonly log: string[] = [];
	private openTasks: Array<() => void> = [];
	private messageTasks: Array<(text: string) => void> = [];
	private closeTasks: Array<() => void> = [];
	private inputBlocks = 0;
	private readonly script: MockScript;

	constructor(script: MockScript) {
		this.script = script;
	}

	send(text: string): void {
		let message: unknown;
		try {
			message = JSON.parse(text);
		} catch {
			return;
		}
		if (typeof message !== 'object' || message === null || Array.isArray(message)) return;
		const record = message as Record<string, unknown>;
		if (typeof record['type'] !== 'string') return;
		this.log.push(record['type']);
		switch (record['type']) {
			case 'session.update':
				this.emitReply(this.script.greetingAudio, this.script.greetingText, false);
				break;
			case 'input.audio':
				this.inputBlocks += 1;
				if (this.inputBlocks === this.script.replyAfterBlocks) {
					this.emitReply(this.script.replyAudio, this.script.replyText, this.script.interrupted);
				}
				break;
			case 'session.end':
				this.emit({ type: 'session.ended' });
				for (const task of this.closeTasks) task();
				break;
			default:
				break;
		}
	}

	close(): void {}

	onOpen(task: () => void): void {
		this.openTasks.push(task);
	}

	onMessage(task: (text: string) => void): void {
		this.messageTasks.push(task);
	}

	onClose(task: () => void): void {
		this.closeTasks.push(task);
	}

	/** Fire the open event, as the network would. */
	open(): void {
		for (const task of this.openTasks) task();
	}

	private emit(message: Record<string, unknown>): void {
		const text = JSON.stringify(message);
		for (const task of this.messageTasks) task(text);
	}

	private emitReply(audio: Float32Array, text: string, interrupted: boolean): void {
		const step = 960;
		for (let at = 0; at < audio.length; at += step) {
			const frames = audio.slice(at, at + step);
			this.emit({ type: 'reply.audio', data: encodeBase64(pcm16ToBytes(floatToPcm16(frames))) });
		}
		this.emit({ type: 'transcript.agent.delta', text, start_ms: 0, end_ms: 1000 });
		this.emit({ type: 'reply.done', interrupted });
	}
}

interface MockStoredUpload {
	owner: string;
	contentType: string;
	visibility: string;
	chunkSize: number;
	chunks: Record<number, Uint8Array>;
}

// MockUploadServer answers the chunk upload protocol from memory. It holds
// every chunk it receives, so a test reads the assembled stems straight off
// it and wraps them for the measuring tool.
export class MockUploadServer {
	readonly base: string;
	private uploads: Record<string, MockStoredUpload> = {};
	private nextId = 1;

	constructor(base = '/api/uploads') {
		this.base = base;
	}

	/** Every upload id the server has opened, in open order. */
	ids(): string[] {
		return Object.keys(this.uploads);
	}

	/** The assembled bytes of one upload, concatenated in index order. */
	bytes(id: string): Uint8Array {
		const upload = this.uploads[id];
		if (upload === undefined) return new Uint8Array(0);
		const indices = Object.keys(upload.chunks).map(Number).sort((a, b) => a - b);
		let total = 0;
		for (const index of indices) total += (upload.chunks[index] ?? new Uint8Array(0)).length;
		const out = new Uint8Array(total);
		let at = 0;
		for (const index of indices) {
			const chunk = upload.chunks[index] ?? new Uint8Array(0);
			out.set(chunk, at);
			at += chunk.length;
		}
		return out;
	}

	/**
	 * Seed one upload from bytes the client already persisted. A reloaded
	 * page rebuilds the server side from the durable client copy, which is
	 * what lets resume finish over exactly the persisted bytes.
	 */
	adopt(
		id: string,
		record: { owner: string; contentType: string; visibility: string; chunkSize: number },
		chunks: Array<{ index: number; bytes: Uint8Array }>
	): void {
		const upload: MockStoredUpload = {
			owner: record.owner,
			contentType: record.contentType,
			visibility: record.visibility,
			chunkSize: record.chunkSize,
			chunks: {}
		};
		for (const chunk of chunks) upload.chunks[chunk.index] = chunk.bytes;
		this.uploads[id] = upload;
	}

	/** Answer one fetch call. Anything outside the base passes through. */
	async handle(input: string, init?: RequestInit): Promise<Response> {
		const path = input.split('?')[0];
		if (!path.startsWith(this.base)) {
			return new Response(JSON.stringify({ error: { code: 'not_found', message: 'no such path' } }), {
				status: 404,
				headers: { 'content-type': 'application/json' }
			});
		}
		const rest = path.slice(this.base.length);
		const method = (init?.method ?? 'GET').toUpperCase();
		if ((rest === '' || rest === '/') && method === 'POST') {
			return this.begin(await readTextBody(init));
		}
		const chunkMatch = rest.match(/^\/([^/]+)\/chunks\/(\d+)$/);
		if (chunkMatch !== null && method === 'PUT') {
			return this.putChunk(chunkMatch[1], Number(chunkMatch[2]), await readBytesBody(init));
		}
		const completeMatch = rest.match(/^\/([^/]+)\/complete$/);
		if (completeMatch !== null && method === 'POST') {
			return this.complete(completeMatch[1], await readTextBody(init));
		}
		const stateMatch = rest.match(/^\/([^/]+)$/);
		if (stateMatch !== null && method === 'GET') {
			return this.state(stateMatch[1]);
		}
		return this.refusal(404, 'not_found', 'no such path');
	}

	private async begin(text: string): Promise<Response> {
		let body: unknown;
		try {
			body = JSON.parse(text);
		} catch {
			return this.refusal(400, 'invalid_request', 'the open body holds no JSON');
		}
		if (typeof body !== 'object' || body === null || Array.isArray(body)) {
			return this.refusal(400, 'invalid_request', 'the open body holds no object');
		}
		const record = body as Record<string, unknown>;
		const id = `up-${this.nextId}`;
		this.nextId += 1;
		this.uploads[id] = {
			owner: typeof record['owner'] === 'string' ? record['owner'] : '',
			contentType: typeof record['content_type'] === 'string' ? record['content_type'] : '',
			visibility: typeof record['visibility'] === 'string' ? record['visibility'] : 'private',
			chunkSize: typeof record['chunk_size'] === 'number' ? record['chunk_size'] : 65536,
			chunks: {}
		};
		return this.snapshot(id);
	}

	private async putChunk(id: string, index: number, bytes: Uint8Array): Promise<Response> {
		const upload = this.uploads[id];
		if (upload === undefined) return this.refusal(404, 'not_found', 'no such upload');
		upload.chunks[index] = bytes;
		return this.snapshot(id);
	}

	private async state(id: string): Promise<Response> {
		if (this.uploads[id] === undefined) return this.refusal(404, 'not_found', 'no such upload');
		return this.snapshot(id);
	}

	private async complete(id: string, text: string): Promise<Response> {
		const upload = this.uploads[id];
		if (upload === undefined) return this.refusal(404, 'not_found', 'no such upload');
		let body: unknown;
		try {
			body = JSON.parse(text);
		} catch {
			return this.refusal(400, 'invalid_request', 'the complete body holds no JSON');
		}
		const sha256 =
			typeof body === 'object' && body !== null && typeof (body as Record<string, unknown>)['sha256'] === 'string'
				? (body as Record<string, unknown>)['sha256']
				: '';
		return json(200, {
			id,
			owner: upload.owner,
			content_type: upload.contentType,
			visibility: upload.visibility,
			size_bytes: this.bytes(id).length,
			sha256
		});
	}

	private async snapshot(id: string): Promise<Response> {
		const upload = this.uploads[id];
		if (upload === undefined) return this.refusal(404, 'not_found', 'no such upload');
		const indices = Object.keys(upload.chunks).map(Number).sort((a, b) => a - b);
		return json(200, {
			id,
			owner: upload.owner,
			content_type: upload.contentType,
			visibility: upload.visibility,
			chunk_size: upload.chunkSize,
			stored_bytes: this.bytes(id).length,
			received: indices,
			missing: [],
			expires_at: new Date(Date.now() + 3600_000).toISOString()
		});
	}

	private refusal(status: number, code: string, message: string): Response {
		return json(status, { error: { code, message } });
	}
}

function json(status: number, value: unknown): Response {
	return new Response(JSON.stringify(value), {
		status,
		headers: { 'content-type': 'application/json' }
	});
}

async function readTextBody(init?: RequestInit): Promise<string> {
	const body = init?.body;
	if (body === undefined || body === null) return '';
	if (typeof body === 'string') return body;
	return new TextDecoder().decode(await readBytesBody(init));
}

async function readBytesBody(init?: RequestInit): Promise<Uint8Array> {
	const body = init?.body;
	if (body === undefined || body === null) return new Uint8Array(0);
	if (typeof body === 'string') return new TextEncoder().encode(body);
	if (body instanceof Uint8Array) return body;
	if (body instanceof ArrayBuffer) return new Uint8Array(body);
	if (typeof Blob !== 'undefined' && body instanceof Blob) {
		return new Uint8Array(await body.arrayBuffer());
	}
	return new Uint8Array(0);
}

/** One progress frame for a scripted job stream, in SSE wire shape. */
export function progressFrame(
	jobId: string,
	stage: string,
	current: number,
	total: number,
	id = 0
): string {
	return `id: ${id}\nevent: progress\ndata: ${JSON.stringify({ job_id: jobId, stage, current, total })}\n\n`;
}

/** One terminal frame for a scripted job stream, in SSE wire shape. */
export function statusFrame(jobId: string, status: 'done' | 'error' | 'cancelled' | 'interrupted'): string {
	return `event: ${status}\ndata: ${JSON.stringify({ job_id: jobId, status })}\n\n`;
}

/** Serve scripted SSE frames as a stream body the job follower reads. */
export function sseResponse(frames: string[]): Response {
	const encoded = frames.map((frame) => new TextEncoder().encode(frame));
	const stream = new ReadableStream<Uint8Array>({
		start(controller) {
			for (const chunk of encoded) controller.enqueue(chunk);
			controller.close();
		}
	});
	return new Response(stream, { status: 200, headers: { 'content-type': 'text/event-stream' } });
}

/**
 * Rebuild the mock server from the upload database in this browser. A page
 * that reloads mid-take calls this before resuming, so resume meets the
 * server state the first load left behind. Mock harness only.
 */
export async function rehydrateServerFromBrowser(server: MockUploadServer): Promise<{
	sessions: number;
	chunks: number;
}> {
	const database = await openChaayaDatabase();
	try {
		const sessions = await readStore<Record<string, unknown>>(database, 'sessions');
		const chunks = await readStore<Record<string, unknown>>(database, 'chunks');
		for (const session of sessions) {
			const id = session['id'];
			if (typeof id !== 'string') continue;
			const owned = chunks.filter((chunk) => chunk['id'] === id);
			const listed = owned
				.filter(
					(chunk) => typeof chunk['index'] === 'number' && chunk['bytes'] instanceof ArrayBuffer
				)
				.map((chunk) => ({
					index: chunk['index'] as number,
					bytes: new Uint8Array(chunk['bytes'] as ArrayBuffer)
				}));
			server.adopt(id, {
				owner: typeof session['owner'] === 'string' ? session['owner'] : '',
				contentType: typeof session['contentType'] === 'string' ? session['contentType'] : '',
				visibility: typeof session['visibility'] === 'string' ? session['visibility'] : 'private',
				chunkSize: typeof session['chunkSize'] === 'number' ? session['chunkSize'] : 65536
			}, listed);
		}
		return { sessions: sessions.length, chunks: chunks.length };
	} finally {
		database.close();
	}
}

function openChaayaDatabase(): Promise<IDBDatabase> {
	return new Promise((resolve, reject) => {
		const request = indexedDB.open('chaaya-upload', 1);
		request.onsuccess = () => resolve(request.result);
		request.onerror = () => reject(request.error ?? new Error('the upload database did not open'));
	});
}

function readStore<T>(database: IDBDatabase, name: string): Promise<T[]> {
	return new Promise((resolve, reject) => {
		const transaction = database.transaction(name, 'readonly');
		const request = transaction.objectStore(name).getAll();
		request.onsuccess = () => resolve(request.result as T[]);
		request.onerror = () => reject(request.error ?? new Error('the upload store refused the read'));
	});
}
