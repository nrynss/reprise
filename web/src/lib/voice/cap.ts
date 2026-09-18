// The browser side stop for a minted session. The provider leaves a session
// open past its cap until the client closes it, so the page runs this timer
// beside the socket. At the cap it calls the same close path the end control
// uses. That path sends the close message once and waits for the answer.

/**
 * CapClock drives the timer. Production passes the system clock. Tests pass
 * a fake the test moves by hand, so no check waits on a real duration.
 */
export interface CapClock {
	now(): number;
	setTimeout(task: () => void, delayMs: number): unknown;
	clearTimeout(handle: unknown): void;
}

/** The system clock behind the CapClock seam. */
export const systemClock: CapClock = {
	now: () => Date.now(),
	setTimeout: (task, delayMs) => setTimeout(task, delayMs),
	clearTimeout: (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>)
};

// SessionCapOptions tunes one timer. The page reads the cap from the broker
// session body and hands the end control path in as finish.
export interface SessionCapOptions {
	/** The cap the broker minted, in seconds. The timer ends at this age. */
	maxSeconds: number;
	/** How long before the cap the screen warns, in seconds. Defaults to 60. */
	warnSeconds?: number;
	/** The clock the timer reads. Defaults to the system clock. */
	clock?: CapClock;
	/** Fires once when the warn point arrives, with whole seconds left. */
	onWarn?: (remainingSeconds: number) => void;
}

// SessionCap ends one take at its minted cap. It compares clock readings
// instead of counting ticks, so a jump from sleep or a suspended tab ends on
// the next wake rather than waiting out stale delay. It calls finish exactly
// once. The page passes the end control path, so the socket latch below it
// keeps every close path to one close message.
export class SessionCap {
	private readonly finish: () => void;
	private readonly maxMs: number;
	private readonly warnLeadMs: number;
	private readonly clock: CapClock;
	private readonly onWarn: ((remainingSeconds: number) => void) | undefined;
	private startedAt = 0;
	private running = false;
	private stopped = false;
	private warned = false;
	private capped = false;
	private timer: unknown = null;

	constructor(finish: () => void, options: SessionCapOptions) {
		if (!Number.isFinite(options.maxSeconds) || options.maxSeconds <= 0) {
			throw new Error('the session cap needs a positive length in seconds');
		}
		const lead = options.warnSeconds ?? 60;
		if (!Number.isFinite(lead) || lead < 0) {
			throw new Error('the session warn lead needs zero or more seconds');
		}
		this.finish = finish;
		this.maxMs = options.maxSeconds * 1000;
		this.warnLeadMs = Math.min(lead * 1000, this.maxMs);
		this.clock = options.clock ?? systemClock;
		this.onWarn = options.onWarn;
	}

	/** True once the warn point has passed. The page reads this to show the notice. */
	get warning(): boolean {
		return this.warned;
	}

	/** True once the timer has fired the close path. */
	get ended(): boolean {
		return this.capped;
	}

	/** Whole seconds left before the cap, never below zero. */
	remainingSeconds(): number {
		if (!this.running) return Math.ceil(this.maxMs / 1000);
		return Math.max(0, Math.ceil((this.startedAt + this.maxMs - this.clock.now()) / 1000));
	}

	/** One sentence the page announces when the warn point arrives. */
	warnText(): string {
		return `This take ends in ${this.remainingSeconds()} seconds. Finish your thought.`;
	}

	/** Start the timer. A second call changes nothing. */
	start(): void {
		if (this.running || this.stopped) return;
		this.running = true;
		this.startedAt = this.clock.now();
		this.arm();
	}

	/**
	 * Re-read the clock now. The page calls this when the tab wakes, so a
	 * jump past the cap ends at once instead of waiting out stale delay.
	 */
	wake(): void {
		if (!this.running || this.stopped || this.capped) return;
		this.tick();
	}

	/**
	 * Cancel the timer. The page calls this when the take ends any other
	 * way, so the timer never closes an already closed take.
	 */
	stop(): void {
		this.stopped = true;
		this.clear();
	}

	private arm(): void {
		this.clear();
		if (!this.running || this.stopped || this.capped) return;
		const now = this.clock.now();
		const warnAt = this.startedAt + this.maxMs - this.warnLeadMs;
		const next = now < warnAt ? warnAt : this.startedAt + this.maxMs;
		this.timer = this.clock.setTimeout(() => this.tick(), Math.max(0, next - now));
	}

	private tick(): void {
		this.clear();
		if (!this.running || this.stopped || this.capped) return;
		const now = this.clock.now();
		const deadline = this.startedAt + this.maxMs;
		if (now >= deadline) {
			this.announce();
			this.capped = true;
			this.finish();
			return;
		}
		if (now >= deadline - this.warnLeadMs) this.announce();
		this.arm();
	}

	private announce(): void {
		if (this.warned) return;
		this.warned = true;
		if (this.onWarn !== undefined) this.onWarn(this.remainingSeconds());
	}

	private clear(): void {
		if (this.timer !== null) {
			this.clock.clearTimeout(this.timer);
			this.timer = null;
		}
	}
}
