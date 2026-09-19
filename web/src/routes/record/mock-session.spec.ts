// End to end run of the voice path against doubles. The record page runs
// under the mock flag with generated input, so no microphone and no
// fixtures take part. Run it directly against the preview server, because
// it lives beside the route it drives and not in the shared tests folder.
// npx playwright test src/routes/record/mock-session.spec.ts

import { execFileSync } from 'child_process';
import { mkdirSync, writeFileSync } from 'fs';
import { tmpdir } from 'os';
import { join } from 'path';
import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';

const OUT = join(tmpdir(), 'reprise-voice');

interface Harness {
	feedBlocks(count: number): void;
	heapBytes(): number | null;
	rate(): number;
	markerEveryBlocks(): number;
	fedBlocks(): number;
	sessionEndCount(): number;
	httpEndCount(): number;
	serverIds(): string[];
	serverBytes(id: string): number[];
	userUploadId(): string;
	hostUploadId(): string;
	marks(): Array<{ reply: number; cutTime: number; interrupted: boolean }>;
	turns(): Array<{ role: string; text: string }>;
	uploadState(): { user: string; host: string; userError: string; hostError: string };
	finishUploads(): Promise<{ userBytes: number; hostBytes: number }>;
	finishTake(): Promise<void>;
}

async function startMockTake(page: Page): Promise<void> {
	await page.goto('/record?mock=1');
	await page.getByRole('button', { name: 'Start session' }).focus();
	await page.keyboard.press('Enter');
	await expect(page.getByRole('heading', { name: 'On air' })).toBeVisible();
}

async function installCompletionStub(page: Page): Promise<void> {
	await page.route('**/stems/complete', async (route) => {
		await route.fulfill({
			status: 200,
			contentType: 'application/json',
			body: JSON.stringify({
				episode_id: 'mock-episode',
				moved: true,
				scheduled: true,
				job_id: 'tj-1',
				state: 'draft'
			})
		});
	});
}

// Wrap raw PCM16 mono bytes in a WAV header the measuring tool reads.
function wavBytes(pcm: number[], sampleRate: number): Uint8Array {
	const data = Uint8Array.from(pcm);
	const header = new Uint8Array(44);
	const view = new DataView(header.buffer);
	const ascii = (at: number, text: string) => {
		for (let i = 0; i < text.length; i += 1) header[at + i] = text.charCodeAt(i);
	};
	ascii(0, 'RIFF');
	view.setUint32(4, 36 + data.length, true);
	ascii(8, 'WAVE');
	ascii(12, 'fmt ');
	view.setUint32(16, 16, true);
	view.setUint16(20, 1, true);
	view.setUint16(22, 1, true);
	view.setUint32(24, sampleRate, true);
	view.setUint32(28, sampleRate * 2, true);
	view.setUint16(32, 2, true);
	view.setUint16(34, 16, true);
	ascii(36, 'data');
	view.setUint32(40, data.length, true);
	const out = new Uint8Array(44 + data.length);
	out.set(header, 0);
	out.set(data, 44);
	return out;
}

function probeStream(path: string): { sample_rate: string; channels: number; codec_name: string } {
	const out = execFileSync(
		'ffprobe',
		['-v', 'error', '-show_streams', '-of', 'json', path],
		{ encoding: 'utf8' }
	);
	const parsed = JSON.parse(out) as { streams: unknown[] };
	const stream = parsed.streams[0] as {
		sample_rate: string;
		channels: number;
		codec_name: string;
	};
	return { sample_rate: stream.sample_rate, channels: stream.channels, codec_name: stream.codec_name };
}

function toInt16(bytes: number[]): Int16Array {
	const view = new DataView(new Uint8Array(bytes).buffer);
	const out = new Int16Array(Math.floor(bytes.length / 2));
	for (let i = 0; i < out.length; i += 1) out[i] = view.getInt16(i * 2, true);
	return out;
}

test('mock take stores both stems with markers in the user stem', async ({ page }) => {
	await startMockTake(page);
	const rate = await page.evaluate(() => (window as unknown as { __mockVoice: Harness }).__mockVoice.rate());
	expect(rate).toBeGreaterThan(0);
	const period = await page.evaluate(
		() => (window as unknown as { __mockVoice: Harness }).__mockVoice.markerEveryBlocks()
	);
	const blocks = period * 3 + 8;
	await page.evaluate((count) => {
		(window as unknown as { __mockVoice: Harness }).__mockVoice.feedBlocks(count);
	}, blocks);

	const seen = await page.evaluate(() => {
		const mock = (window as unknown as { __mockVoice: Harness }).__mockVoice;
		return { marks: mock.marks(), turns: mock.turns() };
	});
	expect(seen.turns.some((turn) => turn.role === 'host' && turn.text.length > 0)).toBe(true);
	expect(seen.marks.length).toBeGreaterThanOrEqual(2);
	expect(seen.marks[0].interrupted).toBe(false);
	expect(seen.marks[1].interrupted).toBe(true);
	expect(seen.marks[1].cutTime).toBeGreaterThan(0);

	const totals = await page.evaluate(() =>
		(window as unknown as { __mockVoice: Harness }).__mockVoice.finishUploads()
	);
	expect(totals.userBytes).toBe(blocks * 4096 * 2);
	expect(totals.hostBytes).toBe(2 * 48000 * 2);
	const stems = await page.evaluate((ids: { userId: string; hostId: string }) => {
		const mock = (window as unknown as { __mockVoice: Harness }).__mockVoice;
		return { userBytes: mock.serverBytes(ids.userId), hostBytes: mock.serverBytes(ids.hostId) };
	}, await page.evaluate(() => {
		const mock = (window as unknown as { __mockVoice: Harness }).__mockVoice;
		return { userId: mock.userUploadId(), hostId: mock.hostUploadId() };
	}));
	expect(stems.userBytes.length).toBe(blocks * 4096 * 2);
	expect(stems.hostBytes.length).toBe(2 * 48000 * 2);

	mkdirSync(OUT, { recursive: true });
	const userPath = join(OUT, 'user.wav');
	const hostPath = join(OUT, 'host.wav');
	writeFileSync(userPath, wavBytes(stems.userBytes, rate));
	writeFileSync(hostPath, wavBytes(stems.hostBytes, 24000));

	const userStream = probeStream(userPath);
	expect(userStream.codec_name).toBe('pcm_s16le');
	expect(userStream.channels).toBe(1);
	expect(userStream.sample_rate).toBe(String(rate));
	const hostStream = probeStream(hostPath);
	expect(hostStream.codec_name).toBe('pcm_s16le');
	expect(hostStream.channels).toBe(1);
	expect(hostStream.sample_rate).toBe('24000');

	const frames = toInt16(stems.userBytes);
	const totalFrames = frames.length;
	let checked = 0;
	for (let index = period; index * 4096 < totalFrames; index += period) {
		expect(Math.abs(frames[index * 4096])).toBeGreaterThan(20000);
		checked += 1;
	}
	expect(checked).toBeGreaterThan(0);
});

test('memory stays flat from a one minute take to a twenty minute take', async ({ page }) => {
	test.setTimeout(180_000);
	await startMockTake(page);
	const rate: number = await page.evaluate(
		() => (window as unknown as { __mockVoice: Harness }).__mockVoice.rate()
	);
	const perMinute = Math.ceil((60 * rate) / 4096);
	const feed = async (count: number) => {
		const chunk = 700;
		for (let left = count; left > 0; left -= chunk) {
			const next = Math.min(chunk, left);
			await page.evaluate((countInner) => {
				(window as unknown as { __mockVoice: Harness }).__mockVoice.feedBlocks(countInner);
			}, next);
		}
	};
	const settle = async () => {
		await page.waitForFunction(
			() => {
				const mock = (window as unknown as { __mockVoice: Harness }).__mockVoice;
				const state = mock.uploadState();
				return state.user.includes('pending=0') && state.host.includes('pending=0');
			},
			undefined,
			{ timeout: 120_000 }
		);
	};
	await feed(perMinute);
	await settle();
	const shortHeap: number | null = await page.evaluate(
		() => (window as unknown as { __mockVoice: Harness }).__mockVoice.heapBytes()
	);
	expect(shortHeap).not.toBeNull();
	await feed(perMinute * 19);
	await settle();
	const longHeap: number | null = await page.evaluate(
		() => (window as unknown as { __mockVoice: Harness }).__mockVoice.heapBytes()
	);
	expect(longHeap).not.toBeNull();
	const ratio = (longHeap ?? 0) / (shortHeap ?? 1);
	expect(ratio).toBeLessThan(4);
});

test('reload completes the take over exactly the persisted bytes', async ({ page }) => {
	await startMockTake(page);
	await page.evaluate(() => {
		(window as unknown as { __mockVoice: Harness }).__mockVoice.feedBlocks(200);
	});
	await page.waitForFunction(() => {
		const mock = (window as unknown as { __mockVoice: Harness }).__mockVoice;
		return (
			mock.serverBytes(mock.userUploadId()).length === 200 * 4096 * 2 &&
			mock.serverBytes(mock.hostUploadId()).length === 2 * 65536
		);
	});
	const before = await page.evaluate(() => {
		const mock = (window as unknown as { __mockVoice: Harness }).__mockVoice;
		const userId = mock.userUploadId();
		const hostId = mock.hostUploadId();
		return {
			userId,
			hostId,
			userBytes: mock.serverBytes(userId),
			hostBytes: mock.serverBytes(hostId)
		};
	});
	expect(before.userBytes.length).toBe(200 * 4096 * 2);

	await page.goto('/record?mock=1&resume=1');
	await expect(page.getByText('Recovered 2 uploads over persisted bytes.')).toBeVisible();
	const after = await page.evaluate(
		(ids: { userId: string; hostId: string }) => {
			const mock = (
				window as unknown as {
					__mockVoice: {
						recovered(): {
							sessions: number;
							chunks: number;
							receipts: Array<{ id: string; sizeBytes: number }>;
						};
						serverBytes(id: string): number[];
						serverIds(): string[];
					};
				}
			).__mockVoice;
			const recovered = mock.recovered();
			return {
				recovered,
				serverIds: mock.serverIds(),
				userBytes: mock.serverBytes(ids.userId),
				hostBytes: mock.serverBytes(ids.hostId)
			};
		},
		{ userId: before.userId, hostId: before.hostId }
	);
	expect(after.recovered.sessions).toBe(2);
	expect(after.recovered.receipts.length).toBe(2);
	expect(after.serverIds).toContain(before.userId);
	expect(after.serverIds).toContain(before.hostId);
	expect(after.userBytes).toEqual(before.userBytes);
	expect(after.hostBytes).toEqual(before.hostBytes);
});

test('leaving the page sends the close message exactly once', async ({ page }) => {
	await startMockTake(page);
	await installCompletionStub(page);
	await page.evaluate(() => {
		(window as unknown as { __mockVoice: Harness }).__mockVoice.feedBlocks(10);
	});
	const counts = await page.evaluate(() => {
		window.dispatchEvent(new Event('pagehide'));
		window.dispatchEvent(new Event('pagehide'));
		const mock = (window as unknown as { __mockVoice: Harness }).__mockVoice;
		return { sessionEnds: mock.sessionEndCount(), httpEnds: mock.httpEndCount() };
	});
	expect(counts.sessionEnds).toBe(1);
	expect(counts.httpEnds).toBe(1);
	void page.evaluate(() => {
		void (window as unknown as { __mockVoice: Harness }).__mockVoice.finishTake();
	});
	await page.waitForURL(/processing/);
	await expect(page.getByRole('heading', { name: 'Processing' })).toBeVisible();
	await expect(page.getByText('Both stems durable')).toBeVisible();
});

test('processing draws scripted jobs through the shared follower', async ({ page }) => {
	await page.goto('/processing?episode=e1&transcript=t1&editorial=e1&mock=1&uploads=done&userBytes=10&hostBytes=20');
	await expect(page.getByText('done: Transcript ready.', { exact: true })).toBeVisible();
	await expect(page.getByText('done: Proposals ready.', { exact: true })).toBeVisible();
	await expect(page.getByText('done: The draft is ready.', { exact: true })).toBeVisible();
	const steps = await page.evaluate(() => {
		const target = window as unknown as {
			__processing: { steps(): Array<{ state: string }> };
		};
		return target.__processing.steps().map((step) => step.state);
	});
	expect(steps.slice(0, 4)).toEqual(['done', 'done', 'done', 'done']);
});
