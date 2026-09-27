// Unit pins for the host stem writer. Every case renders fixed spans with
// synthetic frames, so no proof below waits on a real duration.

import { describe, expect, it } from 'vitest';
import { HostStemWriter } from './host-stem';
import { bytesToPcm16, pcm16ToFloat } from './pcm';

const RATE = 24000;

function reply(value: number, frames: number): Float32Array {
	return new Float32Array(frames).fill(value);
}

function floats(bytes: Uint8Array): Float32Array {
	return pcm16ToFloat(bytesToPcm16(bytes));
}

describe('HostStemWriter', () => {
	it('stores the first reply at the origin with no silence ahead', () => {
		const stem = new HostStemWriter(100);
		stem.push(reply(0.5, 480), { startTime: 100, endTime: 100.02 });
		const bytes = stem.finish(100.02, false);
		expect(bytes).not.toBeNull();
		expect(bytes?.length).toBe(480 * 2);
		const frames = floats(bytes ?? new Uint8Array(0));
		expect(frames[0]).toBeCloseTo(0.5, 3);
		expect(frames[479]).toBeCloseTo(0.5, 3);
		expect(stem.storedThrough).toBeCloseTo(100.02, 6);
		expect(stem.open).toBe(false);
	});

	it('holds the second reply at its play time with silence before it', () => {
		const stem = new HostStemWriter(0);
		stem.push(reply(0.5, 480), { startTime: 0, endTime: 0.02 });
		expect(stem.finish(0.02, false)?.length).toBe(480 * 2);
		stem.push(reply(-0.5, 480), { startTime: 0.05, endTime: 0.07 });
		const bytes = stem.finish(0.07, false);
		expect(bytes?.length).toBe((720 + 480) * 2);
		const frames = floats(bytes ?? new Uint8Array(0));
		for (let i = 0; i < 720; i += 1) expect(frames[i]).toBe(0);
		expect(frames[720]).toBeCloseTo(-0.5, 3);
		expect(frames[1199]).toBeCloseTo(-0.5, 3);
		expect(stem.storedThrough).toBeCloseTo(0.07, 6);
	});

	it('keeps an internal gap where the guest heard it', () => {
		const stem = new HostStemWriter(0);
		stem.push(reply(0.5, 240), { startTime: 1, endTime: 1.01 });
		stem.push(reply(0.5, 240), { startTime: 1.02, endTime: 1.03 });
		const bytes = stem.finish(1.03, false);
		expect(bytes?.length).toBe((RATE + 720) * 2);
		const frames = floats(bytes ?? new Uint8Array(0));
		expect(frames[RATE]).toBeCloseTo(0.5, 3);
		for (let i = RATE + 240; i < RATE + 480; i += 1) expect(frames[i]).toBe(0);
		expect(frames[RATE + 480]).toBeCloseTo(0.5, 3);
	});

	it('cuts an interrupted reply at the flush and keeps only what played', () => {
		const stem = new HostStemWriter(0);
		stem.push(reply(0.5, 480), { startTime: 2, endTime: 2.02 });
		stem.push(reply(0.25, 480), { startTime: 2.02, endTime: 2.04 });
		const bytes = stem.finish(2.03, true);
		expect(bytes?.length).toBe((2 * RATE + 720) * 2);
		const frames = floats(bytes ?? new Uint8Array(0));
		expect(frames[2 * RATE + 719]).toBeCloseTo(0.25, 3);
		expect(frames.length).toBe(2 * RATE + 720);
		expect(stem.storedThrough).toBeCloseTo(2.03, 6);
		stem.push(reply(-0.5, 240), { startTime: 2.1, endTime: 2.11 });
		const next = stem.finish(2.11, false);
		const leading = Math.round((2.1 - 2.03) * RATE);
		expect(next?.length).toBe((leading + 240) * 2);
		const following = floats(next ?? new Uint8Array(0));
		expect(following[leading]).toBeCloseTo(-0.5, 3);
	});

	it('renders no played audio when the cut lands before the reply starts', () => {
		const stem = new HostStemWriter(5);
		stem.push(reply(0.5, 480), { startTime: 6, endTime: 6.02 });
		const bytes = stem.finish(5.5, true);
		expect(bytes?.length).toBe(RATE * 2);
		expect(stem.storedThrough).toBeCloseTo(6, 6);
	});

	it('returns null when no reply is open', () => {
		const stem = new HostStemWriter(0);
		expect(stem.finish(1, false)).toBeNull();
		expect(stem.open).toBe(false);
	});

	it('clamps a reply that overlaps the stem end instead of rewinding', () => {
		const stem = new HostStemWriter(0);
		stem.push(reply(0.5, 240), { startTime: 0, endTime: 0.01 });
		expect(stem.finish(0.01, false)?.length).toBe(240 * 2);
		stem.push(reply(-0.5, 240), { startTime: 0.005, endTime: 0.015 });
		const bytes = stem.finish(0.015, false);
		expect(bytes?.length).toBe(240 * 2);
		expect(floats(bytes ?? new Uint8Array(0))[0]).toBeCloseTo(-0.5, 3);
	});

	it('pins each reply mark within one provider block', () => {
		const blockFrames = 960;
		const stem = new HostStemWriter(10);
		stem.push(reply(0.5, blockFrames), { startTime: 12, endTime: 12.04 });
		const first = stem.finish(12.04, false) ?? new Uint8Array(0);
		stem.push(reply(-0.5, blockFrames), { startTime: 12.04, endTime: 12.08 });
		const cut = stem.finish(12.06, true) ?? new Uint8Array(0);
		const firstFrames = first.length / 2;
		const cutFrames = cut.length / 2;
		expect(firstFrames).toBe(2 * RATE + blockFrames);
		expect(cutFrames).toBe(blockFrames / 2);
		const frames = floats(cut);
		expect(frames[0]).toBeCloseTo(-0.5, 3);
		expect(Math.abs(cutFrames - blockFrames / 2)).toBeLessThanOrEqual(blockFrames);
	});
});
