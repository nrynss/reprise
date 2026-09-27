// The pagehide close must name the provider id the socket learned. The guard
// under test is the real one, so a missing body option fails this pin. The
// upload double is only here so the take can open without a database.

import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('@nrynss/chaaya/audio', async () => {
	const actual = await vi.importActual<typeof import('@nrynss/chaaya/audio')>('@nrynss/chaaya/audio');
	let nextUpload = 1;

	class MemoryUploader {
		state = 'idle';
		id: string | undefined = undefined;
		acknowledged = 0;
		pending = 0;
		retries = 0;
		error: { code: string } | undefined = undefined;
		receipt: { id: string; sizeBytes: number } | undefined = undefined;

		async start(): Promise<void> {
			this.state = 'streaming';
			this.id = `upload-${nextUpload}`;
			nextUpload += 1;
		}

		append(): void {}

		async finish(): Promise<void> {
			this.state = 'done';
			this.receipt = { id: this.id ?? 'upload', sizeBytes: 0 };
		}

		static async resume(): Promise<undefined> {
			return undefined;
		}
	}

	return { ...actual, ChunkUploader: MemoryUploader };
});

import { RecordController } from './record-state';
import type { MockVoiceHarness } from './record-state';

const PROVIDER_ID = 'prov-9';
const END_BODY = JSON.stringify({ provider_session_id: PROVIDER_ID });

function installAudioContext(): void {
	class FakeBuffer {
		private readonly channels: Float32Array[] = [];

		constructor(length: number) {
			this.channels.push(new Float32Array(length));
		}

		getChannelData(index: number): Float32Array {
			return this.channels[index] ?? this.channels[0];
		}
	}

	class FakeSource {
		buffer: FakeBuffer | null = null;
		onended: (() => void) | null = null;
		connect(): void {}
		disconnect(): void {}
		start(): void {}
	}

	class FakeContext {
		readonly sampleRate = 48000;
		readonly destination = {};
		currentTime = 0;

		resume(): Promise<void> {
			return Promise.resolve();
		}

		close(): Promise<void> {
			return Promise.resolve();
		}

		createBuffer(...parts: number[]): FakeBuffer {
			return new FakeBuffer(parts[1] ?? 0);
		}

		createBufferSource(): FakeSource {
			return new FakeSource();
		}
	}

	vi.stubGlobal('AudioContext', FakeContext);
}

function requestUrl(input: unknown): string {
	if (typeof input === 'string') return input;
	if (input instanceof URL) return input.href;
	if (typeof input === 'object' && input !== null && 'url' in input) {
		const url = (input as { url?: unknown }).url;
		if (typeof url === 'string') return url;
	}
	return '';
}

function bodyText(body: unknown): string {
	return typeof body === 'string' ? body : '';
}

describe('pagehide close body', () => {
	let controller: RecordController | null = null;

	afterEach(() => {
		controller?.destroy();
		controller = null;
		vi.unstubAllGlobals();
		vi.restoreAllMocks();
	});

	async function startMockTake(): Promise<MockVoiceHarness> {
		installAudioContext();
		if (typeof navigator.sendBeacon !== 'function') {
			navigator.sendBeacon = () => true;
		}
		const notices: string[] = [];
		controller = new RecordController({
			mock: true,
			resume: false,
			onChange: (snapshot) => {
				notices.push(`${snapshot.phase}: ${snapshot.notice}`);
			}
		});
		controller.mount();
		await controller.start();
		if ((window as unknown as { __mockVoice?: MockVoiceHarness }).__mockVoice === undefined) {
			throw new Error(notices.join(' | ') || 'the take emitted nothing');
		}
		const harness = (window as unknown as { __mockVoice?: MockVoiceHarness }).__mockVoice;
		if (harness === undefined) throw new Error('the mock take exposed no harness');
		return harness;
	}

	it('beacons the learned provider id when the page hides', async () => {
		const harness = await startMockTake();
		const fetches: Array<{ url: string; body: string; keepalive: boolean }> = [];
		const previousFetch = window.fetch.bind(window);
		window.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
			fetches.push({
				url: requestUrl(input),
				body: bodyText(init?.body),
				keepalive: init?.keepalive === true
			});
			return previousFetch(input, init);
		}) as typeof window.fetch;
		const beacons: Array<{ url: string; body: string }> = [];
		navigator.sendBeacon = ((url: string | URL, data?: BodyInit | null) => {
			beacons.push({ url: String(url), body: bodyText(data) });
			return true;
		}) as Navigator['sendBeacon'];

		harness.learnProvider(PROVIDER_ID);
		await vi.waitFor(() => {
			expect(fetches.some((call) => call.body === END_BODY)).toBe(true);
		});
		expect(beacons).toHaveLength(0);

		window.dispatchEvent(new Event('pagehide'));

		expect(beacons).toHaveLength(1);
		expect(beacons[0]?.url).toContain('/api/sessions/mock-session/end');
		expect(beacons[0]?.body).toBe(END_BODY);
		expect(beacons[0]?.body).not.toBe(JSON.stringify({ provider_session_id: '' }));
		expect(fetches.filter((call) => call.keepalive)).toHaveLength(0);
	});

	it('sends the same provider id on the keepalive fetch when the beacon refuses', async () => {
		const harness = await startMockTake();
		const fetches: Array<{ url: string; body: string; keepalive: boolean }> = [];
		const previousFetch = window.fetch.bind(window);
		window.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
			fetches.push({
				url: requestUrl(input),
				body: bodyText(init?.body),
				keepalive: init?.keepalive === true
			});
			return previousFetch(input, init);
		}) as typeof window.fetch;
		navigator.sendBeacon = (() => false) as Navigator['sendBeacon'];

		harness.learnProvider(PROVIDER_ID);
		await vi.waitFor(() => {
			expect(fetches.some((call) => call.body === END_BODY && !call.keepalive)).toBe(true);
		});

		window.dispatchEvent(new Event('pagehide'));

		const kept = fetches.filter((call) => call.keepalive);
		expect(kept).toHaveLength(1);
		expect(kept[0]?.url).toContain('/api/sessions/mock-session/end');
		expect(kept[0]?.body).toBe(END_BODY);
		expect(kept[0]?.body).not.toBe(JSON.stringify({ provider_session_id: '' }));
	});
});
