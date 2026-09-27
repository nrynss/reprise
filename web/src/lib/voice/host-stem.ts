// One host reply on the take clock. The player schedules every block where
// the last one ends, so the span it reports is the moment the guest hears
// it. This writer stores each reply at that span with silence ahead of it,
// and it keeps only what played when an interruption cuts the reply short.
// The uploader appends whole replies, because bytes already handed over
// cannot come back when the cut lands.

import { floatToPcm16, pcm16ToBytes } from './pcm';

// HostSpan carries one played block on the context clock in seconds. It
// mirrors the schedule the stream player reports, so the writer never reads
// the player itself and unit tests hand in fixed spans.
export interface HostSpan {
	readonly startTime: number;
	readonly endTime: number;
}

interface BufferedHostBlock {
	frames: Float32Array;
	start: number;
	end: number;
}

// HostStemWriter lays host replies onto one stem on the context clock. Push
// buffers one reply. Finish renders it into stem bytes and closes it. Both
// stems open at the same origin, so a stored offset of zero stays truthful
// for both. The default rate matches the provider stream.
export class HostStemWriter {
	private readonly rate: number;
	private stemEnd: number;
	private replyStart: number | null = null;
	private pending: BufferedHostBlock[] = [];

	constructor(originSeconds: number, rate = 24000) {
		this.rate = rate;
		this.stemEnd = originSeconds;
	}

	/** True while one reply holds buffered blocks behind it. */
	get open(): boolean {
		return this.replyStart !== null;
	}

	/** The context instant the stem has stored through, in seconds. */
	get storedThrough(): number {
		return this.stemEnd;
	}

	// Buffer one played block. The first block of a reply opens it at its
	// span start. The frames are copied, so later pushes never rewrite a
	// reply already held here.
	push(samples: Float32Array, span: HostSpan): void {
		if (this.replyStart === null) this.replyStart = span.startTime;
		this.pending.push({ frames: samples.slice(), start: span.startTime, end: span.endTime });
	}

	// Render the open reply into stem bytes and close it. Silence fills the
	// wait since the stem end, then each block lands at its span offset, so
	// a gap inside the reply stays audible where the guest heard it. An
	// interrupted reply keeps only the head the cut lets through. Audio past
	// the cut never reaches the stem. It returns null when no reply is open.
	finish(cutTimeSeconds: number, interrupted: boolean): Uint8Array<ArrayBuffer> | null {
		const start = this.replyStart;
		if (start === null) return null;
		const blocks = this.pending;
		this.replyStart = null;
		this.pending = [];
		let replyEnd = start;
		for (const block of blocks) replyEnd = Math.max(replyEnd, block.end);
		const leading = Math.max(0, Math.round((start - this.stemEnd) * this.rate));
		const sounded = Math.max(0, Math.round((replyEnd - start) * this.rate));
		let played = sounded;
		if (interrupted) {
			played = Math.min(sounded, Math.max(0, Math.round((cutTimeSeconds - start) * this.rate)));
		}
		const out = new Int16Array(leading + played);
		for (const block of blocks) {
			const at = Math.round((block.start - start) * this.rate) + leading;
			const frames = floatToPcm16(block.frames);
			for (let i = 0; i < frames.length; i += 1) {
				const index = at + i;
				if (index >= leading && index < leading + played) out[index] = frames[i];
			}
		}
		this.stemEnd = start + played / this.rate;
		return pcm16ToBytes(out);
	}
}
