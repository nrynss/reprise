// Pure audio helpers for the live take. Every function here runs without a
// browser, so unit tests pin exact values on synthetic samples.

/** The sample rate the voice provider accepts, in hertz. */
export const SOCKET_RATE = 24000;

/** Clamp one float frame into 16 bit PCM range and scale it. Scale by 32768
 * so the decode divide has an exact inverse. Every int16 value divided by
 * 32768 stays exact in float32, so rounding the product returns the same
 * value. Full scale input clamps to the int16 rails. */
export function floatToPcm16(samples: Float32Array): Int16Array {
	const out = new Int16Array(samples.length);
	for (let i = 0; i < samples.length; i += 1) {
		const sample = samples[i];
		const clamped = sample > 1 ? 1 : sample < -1 ? -1 : sample;
		const scaled = Math.round(clamped * 32768);
		out[i] = scaled > 32767 ? 32767 : scaled < -32768 ? -32768 : scaled;
	}
	return out;
}

/** Pack 16 bit frames as little endian bytes. */
export function pcm16ToBytes(frames: Int16Array): Uint8Array<ArrayBuffer> {
	const out = new Uint8Array(frames.length * 2);
	const view = new DataView(out.buffer);
	for (let i = 0; i < frames.length; i += 1) {
		view.setInt16(i * 2, frames[i], true);
	}
	return out;
}

/** Unpack little endian bytes into 16 bit frames. */
export function bytesToPcm16(bytes: Uint8Array): Int16Array {
	const count = Math.floor(bytes.length / 2);
	const view = new DataView(bytes.buffer, bytes.byteOffset, count * 2);
	const out = new Int16Array(count);
	for (let i = 0; i < count; i += 1) {
		out[i] = view.getInt16(i * 2, true);
	}
	return out;
}

/** Return 16 bit frames as floats in the range -1 to 1. */
export function pcm16ToFloat(frames: Int16Array): Float32Array {
	const out = new Float32Array(frames.length);
	for (let i = 0; i < frames.length; i += 1) {
		out[i] = frames[i] / 32768;
	}
	return out;
}

const BASE64_ALPHABET = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';

/** Encode bytes as base64 without touching a browser global. */
export function encodeBase64(bytes: Uint8Array): string {
	let out = '';
	for (let i = 0; i < bytes.length; i += 3) {
		const first = bytes[i];
		const second = i + 1 < bytes.length ? bytes[i + 1] : 0;
		const third = i + 2 < bytes.length ? bytes[i + 2] : 0;
		const triple = (first << 16) | (second << 8) | third;
		out += BASE64_ALPHABET[(triple >> 18) & 63];
		out += BASE64_ALPHABET[(triple >> 12) & 63];
		out += i + 1 < bytes.length ? BASE64_ALPHABET[(triple >> 6) & 63] : '=';
		out += i + 2 < bytes.length ? BASE64_ALPHABET[triple & 63] : '=';
	}
	return out;
}

const BASE64_VALUES = new Int8Array(128).fill(-1);
for (let i = 0; i < BASE64_ALPHABET.length; i += 1) {
	BASE64_VALUES[BASE64_ALPHABET.charCodeAt(i)] = i;
}

/** Read one required base64 character. It throws on anything outside the alphabet. */
function base64Value(char: string | undefined): number {
	if (char === undefined) throw new Error('the audio frame carries characters outside base64');
	const code = char.charCodeAt(0);
	const value = code < BASE64_VALUES.length ? BASE64_VALUES[code] : -1;
	if (value < 0) {
		throw new Error('the audio frame carries characters outside base64');
	}
	return value;
}

/** Read one trailing base64 character. A missing tail reads as zero. */
function base64Tail(text: string, index: number): number {
	if (index >= text.length) return 0;
	return base64Value(text[index]);
}

/** Decode base64 into bytes. It throws on any character outside the alphabet. */
export function decodeBase64(text: string): Uint8Array<ArrayBuffer> {
	const clean = text.endsWith('==') ? text.slice(0, -2) : text.endsWith('=') ? text.slice(0, -1) : text;
	const out = new Uint8Array(Math.floor((clean.length * 3) / 4));
	let position = 0;
	for (let i = 0; i < clean.length; i += 4) {
		const first = base64Value(clean[i]);
		const second = base64Value(clean[i + 1]);
		const third = base64Tail(clean, i + 2);
		const fourth = base64Tail(clean, i + 3);
		const triple = (first << 18) | (second << 12) | (third << 6) | fourth;
		out[position] = (triple >> 16) & 255;
		position += 1;
		if (i + 2 < clean.length) {
			out[position] = (triple >> 8) & 255;
			position += 1;
		}
		if (i + 3 < clean.length) {
			out[position] = triple & 255;
			position += 1;
		}
	}
	return out.slice(0, position);
}

/**
 * List the frame offsets that carry a marker impulse. Markers start one
 * period in and stop before the end, so every marker has audio around it.
 */
export function markerOffsets(durationSeconds: number, sampleRate: number, everySeconds: number): number[] {
	const total = Math.floor(durationSeconds * sampleRate);
	const period = Math.floor(everySeconds * sampleRate);
	const offsets: number[] = [];
	for (let at = period; at < total; at += period) {
		offsets.push(at);
	}
	return offsets;
}

/**
 * Build generated input for a test take. A soft sine carries the voice band
 * and one tall impulse per period marks the frame offsets the markers name.
 * The impulses survive a PCM round trip, so a test finds them again in the
 * stored stem.
 */
export function makeTestTone(sampleRate: number, seconds: number, markerEverySeconds: number): Float32Array {
	const total = Math.floor(sampleRate * seconds);
	const out = new Float32Array(total);
	for (let i = 0; i < total; i += 1) {
		out[i] = 0.3 * Math.sin((2 * Math.PI * 440 * i) / sampleRate);
	}
	for (const at of markerOffsets(seconds, sampleRate, markerEverySeconds)) {
		out[at] = 0.9;
	}
	return out;
}

/**
 * Report whether a marker impulse stands at one offset. The check reads one
 * sample, so a resampled stem never passes. Only a stem stored at capture
 * rate carries the exact impulse.
 */
export function markerPresent(frames: Float32Array, offset: number): boolean {
	if (offset < 0 || offset >= frames.length) return false;
	return frames[offset] > 0.5;
}
