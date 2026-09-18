import { describe, expect, it } from 'vitest';
import { VoiceSocket } from './socket';
import { MockSocketHandle, MockUploadServer, progressFrame, sseResponse, statusFrame } from './mock';
import { makeTestTone } from './pcm';
import type { SessionConfig } from './session';

const CONFIG: SessionConfig = { system_prompt: 'prompt', greeting: 'hello', keyterms: [] };

function script() {
	return {
		greetingText: 'welcome back',
		greetingAudio: makeTestTone(24000, 0.5, 60),
		replyAfterBlocks: 3,
		replyAudio: makeTestTone(24000, 0.5, 60),
		replyText: 'tell me more',
		interrupted: true
	};
}

describe('MockSocketHandle', () => {
	it('answers the setup with greeting audio and a done frame', () => {
		const handle = new MockSocketHandle(script());
		const heard: string[] = [];
		const done: boolean[] = [];
		const socket = new VoiceSocket(handle, CONFIG, {
			onHostAudio: () => heard.push('audio'),
			onReplyDone: (interrupted) => done.push(interrupted),
			onUserTranscript: () => {},
			onHostTranscript: () => {},
			onEnded: () => {}
		});
		handle.open();
		expect(handle.log).toEqual(['session.update']);
		expect(heard.length).toBeGreaterThan(0);
		expect(done).toEqual([false]);
		expect(socket.dropped).toBe(0);
	});

	it('fires one reply after the counted input block', () => {
		const handle = new MockSocketHandle(script());
		const done: boolean[] = [];
		const socket = new VoiceSocket(handle, CONFIG, {
			onHostAudio: () => {},
			onReplyDone: (interrupted) => done.push(interrupted),
			onUserTranscript: () => {},
			onHostTranscript: () => {},
			onEnded: () => {}
		});
		handle.open();
		socket.sendAudio(new Uint8Array([1, 2]));
		socket.sendAudio(new Uint8Array([1, 2]));
		expect(done).toEqual([false]);
		socket.sendAudio(new Uint8Array([1, 2]));
		expect(done).toEqual([false, true]);
	});

	it('answers the close message with the ended frame', async () => {
		const handle = new MockSocketHandle(script());
		let ended = 0;
		const socket = new VoiceSocket(handle, CONFIG, {
			onHostAudio: () => {},
			onReplyDone: () => {},
			onUserTranscript: () => {},
			onHostTranscript: () => {},
			onEnded: () => {
				ended += 1;
			}
		});
		handle.open();
		const done = socket.end();
		handle.open();
		expect(handle.log.filter((entry) => entry === 'session.end').length).toBe(1);
		await done;
		expect(ended).toBe(1);
	});
});

describe('MockUploadServer', () => {
	it('opens, stores chunks and completes one upload', async () => {
		const server = new MockUploadServer();
		const opened = await server.handle('/api/uploads', {
			method: 'POST',
			body: JSON.stringify({ owner: 'o1', content_type: 'audio/pcm', visibility: 'private', chunk_size: 4 })
		});
		expect(opened.status).toBe(200);
		const snapshot = (await opened.json()) as { id: string };
		const id = snapshot.id;
		expect(server.ids()).toEqual([id]);

		const put = await server.handle(`/api/uploads/${id}/chunks/0`, {
			method: 'PUT',
			body: new Uint8Array([1, 2, 3, 4])
		});
		expect(put.status).toBe(200);

		const state = (await (await server.handle(`/api/uploads/${id}`, {})).json()) as {
			received: number[];
			stored_bytes: number;
		};
		expect(state.received).toEqual([0]);
		expect(state.stored_bytes).toBe(4);

		const done = (await (
			await server.handle(`/api/uploads/${id}/complete`, {
				method: 'POST',
				body: JSON.stringify({ sha256: 'abc' })
			})
		).json()) as { size_bytes: number; sha256: string };
		expect(done.size_bytes).toBe(4);
		expect(done.sha256).toBe('abc');
		expect([...server.bytes(id)]).toEqual([1, 2, 3, 4]);
	});

	it('answers unknown uploads with the shared refusal shape', async () => {
		const server = new MockUploadServer();
		const missing = await server.handle('/api/uploads/nope', {});
		expect(missing.status).toBe(404);
		const body = (await missing.json()) as { error: { code: string } };
		expect(body.error.code).toBe('not_found');
	});

	it('assembles adopted chunks in index order', () => {
		const server = new MockUploadServer();
		server.adopt(
			'up-9',
			{ owner: 'o', contentType: 'audio/pcm', visibility: 'private', chunkSize: 2 },
			[
				{ index: 1, bytes: new Uint8Array([3, 4]) },
				{ index: 0, bytes: new Uint8Array([1, 2]) }
			]
		);
		expect([...server.bytes('up-9')]).toEqual([1, 2, 3, 4]);
	});
});

describe('scripted job frames', () => {
	it('builds parseable SSE progress and terminal frames', async () => {
		const frames = [progressFrame('j1', 'transcribe', 1, 2, 1), statusFrame('j1', 'done')];
		const response = sseResponse(frames);
		expect(response.status).toBe(200);
		const text = await response.text();
		expect(text).toContain('event: progress');
		expect(text).toContain('event: done');
		expect(text).toContain('"job_id":"j1"');
	});
});
