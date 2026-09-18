import { describe, expect, it } from 'vitest';
import { resampleLinear } from '@nrynss/chaaya/audio';
import {
	blockCount,
	drainHostBlock,
	drainUserBlock,
	sliceBlocks,
	type HostMark
} from './take';
import { bytesToPcm16, makeTestTone, markerOffsets, markerPresent, pcm16ToFloat } from './pcm';

describe('drainUserBlock', () => {
	it('keeps the upload copy at capture rate with markers intact', () => {
		const rate = 48000;
		const tone = makeTestTone(rate, 2, 1);
		const first = tone.slice(0, 4096);
		const { upload, socket } = drainUserBlock(first, rate);
		expect(upload.length).toBe(4096 * 2);
		const floats = pcm16ToFloat(bytesToPcm16(upload));
		expect(floats.length).toBe(4096);
		const offsets = markerOffsets(2, rate, 1);
		expect(offsets.length).toBeGreaterThan(0);
		const firstOffset = offsets[0];
		if (firstOffset < 4096) {
			expect(markerPresent(floats, firstOffset)).toBe(true);
		}
		expect(socket.length).toBeGreaterThan(0);
	});

	it('sends a 24 kHz copy half the length of a 48 kHz block', () => {
		const block = new Float32Array(4096).fill(0.1);
		const { upload, socket } = drainUserBlock(block, 48000);
		expect(upload.length).toBe(8192);
		expect(socket.length).toBe(4096);
	});

	it('passes a 24 kHz block through without resampling', () => {
		const block = new Float32Array(2048).fill(0.2);
		const { upload, socket } = drainUserBlock(block, 24000);
		expect(upload.length).toBe(4096);
		expect(socket.length).toBe(4096);
	});

	it('matches the library resampler sample for sample', () => {
		const block = new Float32Array(4096);
		for (let i = 0; i < block.length; i += 1) block[i] = Math.sin(i / 20) * 0.4;
		const { socket } = drainUserBlock(block, 48000);
		const expected = resampleLinear(block, 48000, 24000);
		expect(socket.length).toBe(expected.length * 2);
	});
});

describe('drainHostBlock', () => {
	it('packs one host block as PCM16 bytes', () => {
		const block = new Float32Array(480).fill(0.25);
		expect(drainHostBlock(block).length).toBe(960);
	});
});

describe('sliceBlocks', () => {
	it('covers the input with numbered offsets and clock times', () => {
		const input = makeTestTone(48000, 1, 60);
		const blocks = sliceBlocks(input, 48000, 4096);
		expect(blocks.length).toBe(blockCount(1, 48000, 4096));
		expect(blocks[0].offset).toBe(0);
		expect(blocks[0].contextTime).toBe(0);
		let total = 0;
		for (const block of blocks) {
			expect(block.offset).toBe(total);
			total += block.samples.length;
		}
		expect(total).toBe(input.length);
		expect(blocks[1].contextTime).toBeCloseTo(4096 / 48000, 9);
	});
});

describe('blockCount', () => {
	it('counts one block per chunk window, rounding the tail up', () => {
		expect(blockCount(60, 48000, 4096)).toBe(704);
		expect(blockCount(1200, 48000, 4096)).toBe(14063);
		expect(blockCount(60, 48000, 4096)).toBeGreaterThan(600);
	});
});

describe('HostMark', () => {
	it('holds the reply order, the cut time and the interrupt flag', () => {
		const mark: HostMark = { reply: 2, cutTime: 12.5, interrupted: true };
		expect(mark.reply).toBe(2);
		expect(mark.cutTime).toBe(12.5);
		expect(mark.interrupted).toBe(true);
	});
});
