// The timer proofs. A fake clock moves time by hand, so every run below is
// exact and no run waits on a real duration. Short caps stand in for the
// minted cap. The mock socket answers the close message the way the provider
// does, so the timer proves the full close path through the real socket.

import { describe, expect, it } from 'vitest';
import { SessionCap, type CapClock } from './cap';
import { MockSocketHandle } from './mock';
import { makeTestTone } from './pcm';
import { VoiceSocket } from './socket';
import type { SessionConfig } from './session';

const CONFIG: SessionConfig = { system_prompt: 'prompt', greeting: 'hello', keyterms: [] };

interface FakeTimer {
	id: number;
	at: number;
	task: () => void;
}

// FakeClock stands in for time. Tests move it by hand. Advance runs every
// timer due on the way. Jump moves the reading with no timer running, which
// is what a sleeping laptop looks like to the page.
class FakeClock implements CapClock {
	private nowMs = 0;
	private nextId = 1;
	private timers: FakeTimer[] = [];

	now(): number {
		return this.nowMs;
	}

	setTimeout(task: () => void, delayMs: number): unknown {
		const id = this.nextId;
		this.nextId += 1;
		this.timers.push({ id, at: this.nowMs + Math.max(0, delayMs), task });
		return id;
	}

	clearTimeout(handle: unknown): void {
		this.timers = this.timers.filter((timer) => timer.id !== handle);
	}

	advance(ms: number): void {
		const target = this.nowMs + ms;
		for (;;) {
			let next: FakeTimer | null = null;
			for (const timer of this.timers) {
				if (timer.at <= target && (next === null || timer.at < next.at)) next = timer;
			}
			if (next === null) break;
			const due = next;
			this.timers = this.timers.filter((timer) => timer.id !== due.id);
			this.nowMs = due.at;
			due.task();
		}
		this.nowMs = target;
	}

	jump(ms: number): void {
		this.nowMs += ms;
	}

	pending(): number {
		return this.timers.length;
	}
}

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

function endCount(handle: MockSocketHandle): number {
	return handle.log.filter((entry) => entry === 'session.end').length;
}

describe('SessionCap', () => {
	it('ends the take once past the cap with nobody pressing end', async () => {
		const clock = new FakeClock();
		const handle = new MockSocketHandle(script());
		let endedFrames = 0;
		const socket = new VoiceSocket(handle, CONFIG, {
			onHostAudio: () => {},
			onReplyDone: () => {},
			onUserTranscript: () => {},
			onHostTranscript: () => {},
			onEnded: () => {
				endedFrames += 1;
			}
		});
		handle.open();
		let endCalls = 0;
		let closing: Promise<void> | null = null;
		const cap = new SessionCap(
			() => {
				endCalls += 1;
				closing = socket.end();
			},
			{ maxSeconds: 10, warnSeconds: 3, clock }
		);
		cap.start();
		clock.advance(30_000);
		expect(closing).not.toBeNull();
		await closing;
		expect(endCalls).toBe(1);
		expect(endCount(handle)).toBe(1);
		expect(handle.log[0]).toBe('session.update');
		expect(endedFrames).toBe(1);
		expect(cap.ended).toBe(true);
		clock.advance(60_000);
		expect(endCalls).toBe(1);
		expect(endCount(handle)).toBe(1);
	});

	it('warns before the cap so the screen announces the end first', () => {
		const clock = new FakeClock();
		const warned: number[] = [];
		const order: string[] = [];
		const cap = new SessionCap(
			() => {
				order.push('end');
			},
			{
				maxSeconds: 300,
				warnSeconds: 60,
				clock,
				onWarn: (left) => {
					warned.push(left);
					order.push('warn');
				}
			}
		);
		cap.start();
		clock.advance(239_000);
		expect(warned).toEqual([]);
		expect(cap.warning).toBe(false);
		clock.advance(1000);
		expect(warned).toEqual([60]);
		expect(cap.warning).toBe(true);
		expect(order).toEqual(['warn']);
		expect(cap.remainingSeconds()).toBe(60);
		expect(cap.warnText()).toContain('60 seconds');
		clock.advance(60_000);
		expect(order).toEqual(['warn', 'end']);
	});

	it('ends on the next wake when the clock jumps past the cap', async () => {
		const clock = new FakeClock();
		const handle = new MockSocketHandle(script());
		const socket = new VoiceSocket(handle, CONFIG, {
			onHostAudio: () => {},
			onReplyDone: () => {},
			onUserTranscript: () => {},
			onHostTranscript: () => {},
			onEnded: () => {}
		});
		handle.open();
		const warned: number[] = [];
		let endCalls = 0;
		let closing: Promise<void> | null = null;
		const cap = new SessionCap(
			() => {
				endCalls += 1;
				closing = socket.end();
			},
			{
				maxSeconds: 10,
				warnSeconds: 3,
				clock,
				onWarn: (left) => {
					warned.push(left);
				}
			}
		);
		cap.start();
		clock.jump(30_000);
		expect(endCalls).toBe(0);
		cap.wake();
		expect(closing).not.toBeNull();
		await closing;
		expect(endCalls).toBe(1);
		expect(endCount(handle)).toBe(1);
		expect(warned).toEqual([0]);
		expect(clock.pending()).toBe(0);
	});

	it('ends at once when a delayed tick lands past the cap', async () => {
		const clock = new FakeClock();
		const handle = new MockSocketHandle(script());
		const socket = new VoiceSocket(handle, CONFIG, {
			onHostAudio: () => {},
			onReplyDone: () => {},
			onUserTranscript: () => {},
			onHostTranscript: () => {},
			onEnded: () => {}
		});
		handle.open();
		let endCalls = 0;
		let closing: Promise<void> | null = null;
		const cap = new SessionCap(
			() => {
				endCalls += 1;
				closing = socket.end();
			},
			{ maxSeconds: 10, warnSeconds: 3, clock }
		);
		cap.start();
		clock.jump(30_000);
		clock.advance(0);
		expect(closing).not.toBeNull();
		await closing;
		expect(endCalls).toBe(1);
		expect(endCount(handle)).toBe(1);
		expect(clock.pending()).toBe(0);
	});

	it('never closes a take that already ended another way', () => {
		const clock = new FakeClock();
		let endCalls = 0;
		const cap = new SessionCap(
			() => {
				endCalls += 1;
			},
			{ maxSeconds: 10, warnSeconds: 3, clock }
		);
		cap.start();
		cap.stop();
		clock.advance(120_000);
		expect(endCalls).toBe(0);
		expect(clock.pending()).toBe(0);
	});

	it('rejects a cap that could never bound a session', () => {
		const clock = new FakeClock();
		expect(() => new SessionCap(() => {}, { maxSeconds: 0, clock })).toThrow();
		expect(() => new SessionCap(() => {}, { maxSeconds: -5, clock })).toThrow();
		expect(() => new SessionCap(() => {}, { maxSeconds: 10, warnSeconds: -1, clock })).toThrow();
	});
});
