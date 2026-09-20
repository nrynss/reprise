import { describe, expect, it } from 'vitest';
import {
	decodeBase64,
	encodeBase64,
	floatToPcm16,
	makeTestTone,
	markerOffsets,
	markerPresent,
	pcm16ToBytes,
	bytesToPcm16,
	pcm16ToFloat
} from './pcm';

describe('floatToPcm16', () => {
	it('maps silence to zero and full scale to the rails', () => {
		const frames = floatToPcm16(new Float32Array([0, 1, -1, 0.5, -0.5]));
		expect([...frames]).toEqual([0, 32767, -32768, 16384, -16384]);
	});

	it('clamps frames outside the range instead of wrapping', () => {
		const frames = floatToPcm16(new Float32Array([2, -2]));
		expect([...frames]).toEqual([32767, -32768]);
	});
});

describe('pcm16 bytes', () => {
	it('round trips through little endian bytes', () => {
		const frames = new Int16Array([0, 1, -1, 32767, -32767, 1234]);
		expect([...bytesToPcm16(pcm16ToBytes(frames))]).toEqual([...frames]);
	});

	it('drops a trailing stray byte', () => {
		expect([...bytesToPcm16(new Uint8Array([1, 0, 9]))]).toEqual([1]);
	});

	it('returns floats in range', () => {
		const floats = pcm16ToFloat(new Int16Array([0, 32767, -32768]));
		expect(floats[0]).toBe(0);
		expect(floats[1]).toBeCloseTo(32767 / 32768, 6);
		expect(floats[2]).toBe(-1);
	});
});

describe('base64', () => {
	it('round trips arbitrary bytes', () => {
		const bytes = new Uint8Array([0, 1, 2, 250, 255, 16, 32, 64, 128]);
		expect([...decodeBase64(encodeBase64(bytes))]).toEqual([...bytes]);
	});

	it('round trips a realistic audio frame', () => {
		const tone = makeTestTone(24000, 0.5, 60);
		const bytes = pcm16ToBytes(floatToPcm16(tone));
		expect([...decodeBase64(encodeBase64(bytes))]).toEqual([...bytes]);
	});

	it('rejects characters outside the alphabet', () => {
		expect(() => decodeBase64('!!!')).toThrow();
	});
});

describe('markers', () => {
	it('places markers one period in and before the end', () => {
		expect(markerOffsets(10, 1000, 2)).toEqual([2000, 4000, 6000, 8000]);
	});

	it('finds every marker in generated input after a PCM round trip', () => {
		const tone = makeTestTone(48000, 3, 1);
		const floats = pcm16ToFloat(bytesToPcm16(pcm16ToBytes(floatToPcm16(tone))));
		for (const at of markerOffsets(3, 48000, 1)) {
			expect(markerPresent(floats, at)).toBe(true);
		}
	});

	it('finds no marker where none was written', () => {
		const tone = makeTestTone(48000, 1, 60);
		expect(markerOffsets(1, 48000, 60)).toEqual([]);
		expect(markerPresent(tone, 100)).toBe(false);
		expect(markerPresent(tone, -1)).toBe(false);
		expect(markerPresent(tone, tone.length)).toBe(false);
	});
});
