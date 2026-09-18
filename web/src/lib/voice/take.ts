// One take across two stems. The recorder drains every block here and keeps
// nothing. User blocks go to the upload at capture rate and to the socket as
// a 24 kHz copy. Host blocks go to the player and to the host upload. A flush
// ends one reply, and its cut time becomes the mark in the host stem.

import type { CaptureChunk } from '@nrynss/chaaya/audio';
import { resampleLinear } from '@nrynss/chaaya/audio';
import { floatToPcm16, pcm16ToBytes, SOCKET_RATE } from './pcm';

// HostMark pins one reply to the host stem clock. The player reports the cut
// time when a reply flushes, and alignment reads the marks later.
export interface HostMark {
	reply: number;
	cutTime: number;
	interrupted: boolean;
}

// DrainedBlock carries both copies of one captured block. The upload copy
// stays at capture rate. The socket copy reaches the provider rate.
export interface DrainedBlock {
	upload: Uint8Array<ArrayBuffer>;
	socket: Uint8Array<ArrayBuffer>;
}

/**
 * Drain one captured user block. The socket copy resamples a copy of the
 * frames, so the stored stem keeps the exact captured values.
 */
export function drainUserBlock(samples: Float32Array, fromRate: number): DrainedBlock {
	const upload = pcm16ToBytes(floatToPcm16(samples));
	const resampled = resampleLinear(samples, fromRate, SOCKET_RATE);
	const socket = pcm16ToBytes(floatToPcm16(resampled));
	return { upload, socket };
}

/** Drain one played host block at the provider rate into upload bytes. */
export function drainHostBlock(samples: Float32Array): Uint8Array<ArrayBuffer> {
	return pcm16ToBytes(floatToPcm16(samples));
}

/**
 * Count the blocks a synthetic take of one length holds. Tests feed that
 * many blocks through the drain to measure held memory at take scale.
 */
export function blockCount(durationSeconds: number, sampleRate: number, chunkFrames: number): number {
	return Math.ceil((durationSeconds * sampleRate) / chunkFrames);
}

/**
 * Slice generated input into capture blocks with clock readings. The drain
 * under test sees the same shape the recorder hands it on a real take.
 */
export function sliceBlocks(
	input: Float32Array,
	sampleRate: number,
	chunkFrames: number
): CaptureChunk[] {
	const blocks: CaptureChunk[] = [];
	let offset = 0;
	let time = 0;
	while (offset < input.length) {
		const samples = input.slice(offset, offset + chunkFrames);
		blocks.push({ samples, offset, contextTime: time });
		offset += samples.length;
		time += samples.length / sampleRate;
	}
	return blocks;
}
