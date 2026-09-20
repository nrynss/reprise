// One live take and everything it touches. The record page owns the
// template and nothing else. This controller owns the audio clock, the
// stems, the socket and the ending, so the drain path stays identical in
// production and under the mock harness. It holds no reactivity of its
// own. It reports every change through one snapshot callback the page
// renders.

import {
	closeSession,
	describeSessionEndFailure,
	describeUploadFailure,
	mintSession
} from '$lib/voice/session-calls';
import {
	AudioRecorder,
	ChunkUploader,
	PcmStreamPlayer,
	measureBlock,
	type CaptureChunk
} from '@nrynss/chaaya/audio';
import { SessionGuard } from '@nrynss/chaaya/guard';
import {
	MockSocketHandle,
	MockUploadServer,
	rehydrateServerFromBrowser,
	type MockScript
} from '$lib/voice/mock';
import {
	completionPair,
	createCompletionDriver,
	type CompletionDriver,
	type CompletionView,
	type StemPair
} from '../../routes/record/stems-complete';
import { drainHostBlock, drainUserBlock, type HostMark } from '$lib/voice/take';
import { makeTestTone } from '$lib/voice/pcm';
import { socketUrl, type SessionStart } from '$lib/voice/session';
import { browserSocket, VoiceSocket, type SocketHandle } from '$lib/voice/socket';
import { SessionCap, type CapClock } from '$lib/voice/cap';

export type RecordPhase = 'preflight' | 'starting' | 'live' | 'ending' | 'recovering';

export interface RecordTurn {
	role: 'user' | 'host';
	text: string;
}

// RecordSnapshot carries everything the page renders. The controller emits
// a fresh one after every change, and the page swaps it in wholesale.
export interface RecordSnapshot {
	phase: RecordPhase;
	notice: string;
	turns: RecordTurn[];
	levelDb: number;
	elapsed: string;
	armed: boolean;
	greeting: string;
	capWarning: boolean;
	capText: string;
	completionFailed: boolean;
}

// MockVoiceHarness drives the take without fixtures. The page exposes it
// on the window under the mock flag, and the end to end run calls it.
export interface MockVoiceHarness {
	feedBlocks(count: number): void;
	heapBytes(): number | null;
	rate(): number;
	markerEveryBlocks(): number;
	fedBlocks(): number;
	sessionEndCount(): number;
	httpEndCount(): number;
	serverIds(): string[];
	serverBytes(id: string): number[];
	userUploadId(): string | undefined;
	hostUploadId(): string | undefined;
	marks(): HostMark[];
	turns(): RecordTurn[];
	uploadState(): { user: string; host: string; userError: string; hostError: string };
	finishUploads(): Promise<{ userBytes: number; hostBytes: number }>;
	finishTake(): Promise<void>;
	retryCompletion(): Promise<void>;
	completionState(): string;
	completionText(): string;
	capAdvance(ms: number): void;
	capJump(ms: number): void;
	capWake(): void;
	capInfo(): { warning: boolean; text: string; remainingSeconds: number };
	clockRunning(): boolean;
	challengeUrl(part: string): void;
	clearChallenges(): void;
}

export interface MockRecovered {
	sessions: number;
	chunks: number;
	receipts: Array<{ id: string; sizeBytes: number }>;
}

interface HeapPerformance extends Performance {
	memory?: { usedJSHeapSize: number };
}

const UPLOAD_BASE = '/api/uploads';
const CHUNK_FRAMES = 4096;
const TONE_SECONDS = 2;

// emptySnapshot gives the page an initial render with no session behind it.
export const emptySnapshot: RecordSnapshot = {
	phase: 'preflight',
	notice: 'Check the microphone, then start the take.',
	turns: [],
	levelDb: -100,
	elapsed: '0:00',
	armed: false,
	greeting: '',
	capWarning: false,
	capText: '',
	completionFailed: false
};

function mockScript(): MockScript {
	return {
		greetingText: 'Last time you mentioned the loft. Did you ever go back?',
		greetingAudio: makeTestTone(24000, TONE_SECONDS, 60),
		replyAfterBlocks: 4,
		replyAudio: makeTestTone(24000, TONE_SECONDS, 60),
		replyText: 'Say more about that.',
		interrupted: true
	};
}

// HarnessClock stands in for time under the mock flag. The end to end run
// moves it by hand, so the cap proofs never wait on a real duration. Advance
// runs every timer due on the way. Jump moves the reading with no timer
// running, which is what a sleeping laptop looks like to the page.
class HarnessClock implements CapClock {
	private nowMs = 0;
	private nextId = 1;
	private timers: Array<{ id: number; at: number; task: () => void }> = [];

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
			let next: { id: number; at: number; task: () => void } | null = null;
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
}

// RecordController runs one take from the first gesture to the draft.
// The mock flag swaps the provider and the upload server for doubles and
// feeds generated input through the same drain. The resume flag skips the
// take and completes whatever the store still holds.
export class RecordController {
	private readonly mockMode: boolean;
	private readonly resumeOnly: boolean;
	private readonly onChange: (snapshot: RecordSnapshot) => void;
	private phase: RecordPhase = 'preflight';
	private notice = emptySnapshot.notice;
	private turns: RecordTurn[] = [];
	private levelDb = -100;
	private elapsed = '0:00';
	private armed = false;
	private greeting = '';
	private context: AudioContext | null = null;
	private recorder: AudioRecorder | null = null;
	private player: PcmStreamPlayer | null = null;
	private userUpload: ChunkUploader | null = null;
	private hostUpload: ChunkUploader | null = null;
	private voice: VoiceSocket | null = null;
	private guard: SessionGuard | null = null;
	private session: SessionStart | null = null;
	private owner = 'guest';
	private takeStart = 0;
	private replyCount = 0;
	private timer: number | null = null;
	private marks: HostMark[] = [];
	private mockServer: MockUploadServer | null = null;
	private mockHandle: MockSocketHandle | null = null;
	private httpEndCount = 0;
	private feedOffset = 0;
	private feedTime = 0;
	private fedBlockCount = 0;
	private ending: Promise<void> | null = null;
	private hideListener: (() => void) | null = null;
	private showListener: (() => void) | null = null;
	private visibleListener: (() => void) | null = null;
	private cap: SessionCap | null = null;
	private capClock: HarnessClock | null = null;
	private capWarning = false;
	private capText = '';
	private completionDriver: CompletionDriver | null = null;
	private completion: CompletionView | null = null;
	private completionPosted = false;
	private uploadFailure: string | null = null;
	private pendingEnd: string | null = null;
	private mockChallenges: string[] = [];
	private pendingCompletion: { episode: string; pair: StemPair; userBytes: number; hostBytes: number } | null =
		null;

	constructor(options: {
		mock: boolean;
		resume: boolean;
		onChange: (snapshot: RecordSnapshot) => void;
	}) {
		this.mockMode = options.mock;
		this.resumeOnly = options.resume;
		this.onChange = options.onChange;
	}

	/** Start the take or the recovery, depending on the resume flag. */
	mount(): void {
		if (this.mockMode && this.resumeOnly) {
			void this.recoverMockUploads();
			return;
		}
		this.emit();
		this.hideListener = () => {
			if (this.phase === 'live') {
				this.stopCap();
				void this.voice?.end();
			}
		};
		window.addEventListener('pagehide', this.hideListener);
		this.showListener = () => {
			this.cap?.wake();
		};
		window.addEventListener('pageshow', this.showListener);
		this.visibleListener = () => {
			this.cap?.wake();
		};
		document.addEventListener('visibilitychange', this.visibleListener);
	}

	/** Stop timers and listeners, and end a take the page walks away from. */
	destroy(): void {
		if (this.hideListener !== null) {
			window.removeEventListener('pagehide', this.hideListener);
			this.hideListener = null;
		}
		if (this.showListener !== null) {
			window.removeEventListener('pageshow', this.showListener);
			this.showListener = null;
		}
		if (this.visibleListener !== null) {
			document.removeEventListener('visibilitychange', this.visibleListener);
			this.visibleListener = null;
		}
		this.stopClock();
		this.guard?.destroy();
		this.stopCap();
		if (this.phase === 'live') void this.voice?.end();
	}

	/** Open the session from the start control. A gesture must wrap this. */
	async start(): Promise<void> {
		if (this.phase !== 'preflight') return;
		this.phase = 'starting';
		this.notice = 'Opening the session.';
		this.uploadFailure = null;
		this.pendingEnd = null;
		this.completionPosted = false;
		this.emit();
		try {
			if (this.mockMode) {
				await this.startMockTake();
			} else {
				await this.startRealTake();
			}
			this.takeStart = this.context?.currentTime ?? 0;
			this.phase = 'live';
			this.notice = 'On air. The host hears you.';
			this.startCap();
			this.emit();
			this.timer = window.setInterval(() => {
				if (this.context !== null) {
					this.elapsed = formatElapsed(this.context.currentTime, this.takeStart);
					this.emit();
				}
			}, 500);
		} catch (error) {
			this.phase = 'preflight';
			this.notice = error instanceof Error ? error.message : 'The session did not open.';
			this.emit();
		}
	}

	/** Drive the end control. The first press arms, the second press ends. */
	endControl(): void {
		if (this.phase !== 'live') return;
		if (!this.armed) {
			this.armed = true;
			this.notice = 'Press end again to stop the take.';
			this.emit();
			return;
		}
		void this.endTake();
	}

	/** End the take and hand the session to the processing screen. */
	async endTake(): Promise<void> {
		if (this.ending !== null) {
			await this.ending;
			return;
		}
		if (this.voice === null || this.session === null) return;
		this.stopCap();
		this.stopClock();
		this.phase = 'ending';
		this.armed = false;
		this.notice = 'Ending the session.';
		this.emit();
		const voice = this.voice;
		const session = this.session;
		this.ending = (async () => {
			await voice.end();
			await this.recorder?.stop();
			if (this.userUpload !== null) await this.userUpload.finish();
			if (this.hostUpload !== null) await this.hostUpload.finish();
			this.uploadFailure = describeUploadFailure(this.userUpload, this.hostUpload);
			const userBytes = this.userUpload?.receipt?.sizeBytes ?? 0;
			const hostBytes = this.hostUpload?.receipt?.sizeBytes ?? 0;
			try {
				await closeSession(session.session_id);
			} catch (error) {
				this.pendingEnd = session.session_id;
				this.pendingCompletion = {
					episode: session.episode_id,
					pair: this.storedPair(),
					userBytes,
					hostBytes
				};
				this.completionPosted = false;
				this.failTake(describeSessionEndFailure(error));
				return;
			}
			this.pendingEnd = null;
			if (this.uploadFailure !== null) {
				this.pendingCompletion = {
					episode: session.episode_id,
					pair: this.storedPair(),
					userBytes,
					hostBytes
				};
				this.completionPosted = false;
				this.failTake(`${this.uploadFailure} Your stems stay stored. Press retry.`);
				return;
			}
			this.guard?.close();
			const moved = await this.postStoredCompletion(session.episode_id, userBytes, hostBytes);
			if (!moved) return;
			this.goProcessing(session.episode_id, userBytes, hostBytes);
		})();
		await this.ending;
	}

	/** Retry the stored take after a loud refusal. A pending close record goes
	first, then the stored pair reposts through the driver. Either retry is
	safe: the close record is idempotent and the server reports the standing
	outcome instead of scheduling twice. */
	async retryCompletion(): Promise<void> {
		if (this.pendingCompletion === null) return;
		const pending = this.pendingCompletion;
		if (this.pendingEnd !== null) {
			try {
				await closeSession(this.pendingEnd);
			} catch (error) {
				this.failTake(describeSessionEndFailure(error));
				return;
			}
			this.pendingEnd = null;
		}
		if (this.completionPosted) {
			await this.ensureCompletionDriver().retry();
		} else {
			this.completionPosted = true;
			await this.ensureCompletionDriver().run(pending.episode, pending.pair);
		}
		if (this.completion?.status === 'failed') return;
		this.pendingCompletion = null;
		this.goProcessing(pending.episode, pending.userBytes, pending.hostBytes);
	}

	// postStoredCompletion posts the finished stem pair for one episode. It
	// returns true when the draft move lands. A refusal keeps the pending
	// pair for the retry and returns false, so the take never navigates
	// away silent. Missing ids or a missing rate fail the same loud way.
	private async postStoredCompletion(
		episode: string,
		userBytes: number,
		hostBytes: number
	): Promise<boolean> {
		const pair = this.storedPair();
		this.pendingCompletion = { episode, pair, userBytes, hostBytes };
		this.completionPosted = true;
		await this.ensureCompletionDriver().run(episode, pair);
		if (this.completion?.status === 'failed') return false;
		this.pendingCompletion = null;
		return true;
	}

	// ensureCompletionDriver builds the draft move driver around the take
	// notice. Every outcome lands in words on the page, so a failure reads
	// as a refusal with a retry instead of a silent stall.
	private ensureCompletionDriver(): CompletionDriver {
		if (this.completionDriver === null) {
			this.completionDriver = createCompletionDriver((view) => {
				this.completion = view;
				this.notice = view.text;
				this.emit();
			});
		}
		return this.completionDriver;
	}

	// storedPair builds the completion pair from the finished uploads. The
	// host stem plays at the provider rate, so the host rate rides on that
	// constant the same way on every path that posts.
	private storedPair(): StemPair {
		return completionPair(
			this.userUpload?.id ?? '',
			this.hostUpload?.id ?? '',
			Math.round(this.context?.sampleRate ?? 0)
		);
	}

	// requireUploadsOpen throws a named error when either stem upload failed
	// to open. The start catch shows it loudly and the page stays preflight,
	// so the start control remains the retry and no half take records.
	private requireUploadsOpen(): void {
		const failure = describeUploadFailure(this.userUpload, this.hostUpload);
		if (failure !== null) throw new Error(`${failure} Press start to try again.`);
	}

	// failTake freezes the take on a loud named failure. The clock stops, the
	// page stays on the record screen with the retry, and the take never
	// stalls silent and never ticks against a dead take.
	private failTake(text: string): void {
		this.stopClock();
		this.completion = { status: 'failed', text, canRetry: true };
		this.notice = text;
		this.emit();
	}

	// stopClock freezes the elapsed display. Every take end path calls this,
	// so the display never ticks against a take that cannot proceed.
	private stopClock(): void {
		if (this.timer !== null) {
			window.clearInterval(this.timer);
			this.timer = null;
		}
	}

	// goProcessing hands the finished take to the processing screen. The
	// handoff names the episode and the durable totals, plus the mock flag
	// under the harness so the doubles stay in charge there.
	private goProcessing(episode: string, userBytes: number, hostBytes: number): void {
		const suffix = this.mockMode ? '&mock=1' : '';
		window.location.assign(
			`/processing?episode=${encodeURIComponent(episode)}&uploads=done&userBytes=${userBytes}&hostBytes=${hostBytes}${suffix}`
		);
	}

	harness(): MockVoiceHarness | null {
		if (!this.mockMode || this.mockHandle === null || this.mockServer === null) return null;
		const handle = this.mockHandle;
		const server = this.mockServer;
		if (handle === null || server === null) return null;
		return {
			feedBlocks: (count: number) => this.feedBlocks(count),
			heapBytes: () => this.heapBytes(),
			rate: () => this.context?.sampleRate ?? 0,
			markerEveryBlocks: () =>
				Math.max(1, Math.round(((this.context?.sampleRate ?? 48000) * 2) / CHUNK_FRAMES)),
			fedBlocks: () => this.fedBlockCount,
			sessionEndCount: () => handle.log.filter((entry) => entry === 'session.end').length,
			httpEndCount: () => this.httpEndCount,
			serverIds: () => server.ids(),
			serverBytes: (id: string) => [...server.bytes(id)],
			userUploadId: () => this.userUpload?.id,
			hostUploadId: () => this.hostUpload?.id,
			marks: () => [...this.marks],
			turns: () => [...this.turns],
			uploadState: () => ({
				user: `${this.userUpload?.state ?? 'none'} ack=${this.userUpload?.acknowledged ?? -1} pending=${this.userUpload?.pending ?? -1} retries=${this.userUpload?.retries ?? -1}`,
				host: `${this.hostUpload?.state ?? 'none'} ack=${this.hostUpload?.acknowledged ?? -1} pending=${this.hostUpload?.pending ?? -1} retries=${this.hostUpload?.retries ?? -1}`,
				userError: this.userUpload?.error?.code ?? '',
				hostError: this.hostUpload?.error?.code ?? ''
			}),
			finishUploads: () => this.finishUploads(),
			finishTake: () => this.endTake(),
			retryCompletion: () => this.retryCompletion(),
			completionState: () => this.completion?.status ?? 'none',
			completionText: () => this.completion?.text ?? '',
			capAdvance: (ms: number) => {
				this.capClock?.advance(ms);
			},
			capJump: (ms: number) => {
				this.capClock?.jump(ms);
			},
			capWake: () => {
				this.cap?.wake();
			},
			capInfo: () => ({
				warning: this.capWarning,
				text: this.capText,
				remainingSeconds: this.cap?.remainingSeconds() ?? 0
			}),
			clockRunning: () => this.timer !== null,
			challengeUrl: (part: string) => {
				this.mockChallenges.push(part);
			},
			clearChallenges: () => {
				this.mockChallenges = [];
			}
		};
	}

	/** Finish both uploads without ending the socket. Tests read stems off this. */
	private async finishUploads(): Promise<{ userBytes: number; hostBytes: number }> {
		if (this.userUpload !== null) await this.userUpload.finish();
		if (this.hostUpload !== null) await this.hostUpload.finish();
		return {
			userBytes: this.userUpload?.receipt?.sizeBytes ?? 0,
			hostBytes: this.hostUpload?.receipt?.sizeBytes ?? 0
		};
	}

	private feedBlocks(count: number): void {
		if (this.context === null || !this.mockMode) return;
		const rate = this.context.sampleRate;
		const period = Math.max(1, Math.round((rate * 2) / CHUNK_FRAMES));
		for (let i = 0; i < count; i += 1) {
			const samples = new Float32Array(CHUNK_FRAMES);
			for (let frame = 0; frame < samples.length; frame += 1) {
				samples[frame] = 0.3 * Math.sin((2 * Math.PI * 440 * (this.feedOffset + frame)) / rate);
			}
			if (this.fedBlockCount > 0 && this.fedBlockCount % period === 0) {
				samples[0] = 0.9;
			}
			this.handleUserBlock({ samples, offset: this.feedOffset, contextTime: this.feedTime });
			this.feedOffset += samples.length;
			this.feedTime += samples.length / rate;
			this.fedBlockCount += 1;
		}
	}

	private heapBytes(): number | null {
		const perf = performance as HeapPerformance;
		if (perf.memory === undefined) return null;
		return perf.memory.usedJSHeapSize;
	}

	private async startRealTake(): Promise<void> {
		this.session = await mintSession();
		this.greeting = this.session.config.greeting;
		this.owner = this.session.episode_id;
		this.context = new AudioContext();
		await this.context.resume();
		this.player = new PcmStreamPlayer({ context: this.context, streamRate: 24000 });
		await this.openUploads();
		this.requireUploadsOpen();
		this.voice = this.wireVoice(browserSocket(socketUrl(this.session.token)));
		this.guard = new SessionGuard({ url: `/api/sessions/${this.session.session_id}/end` });
		this.guard.attach();
		this.recorder = new AudioRecorder({
			mode: 'pcm',
			context: this.context,
			autoStopSeconds: 0,
			echoCancellation: true,
			noiseSuppression: false,
			autoGainControl: false,
			retain: false,
			onChunk: (chunk) => this.handleUserBlock(chunk)
		});
		await this.recorder.start();
	}

	private async startMockTake(): Promise<void> {
		this.context = new AudioContext();
		await this.context.resume();
		this.session = {
			session_id: 'mock-session',
			episode_id: 'mock-episode',
			token: 'mock-token',
			expires_in_seconds: 60,
			max_session_duration_seconds: 1200,
			config: {
				system_prompt: 'mock host',
				greeting: 'Last time you mentioned the loft. Did you ever go back?',
				keyterms: ['the loft']
			}
		};
		this.greeting = this.session.config.greeting;
		this.mockServer = new MockUploadServer(UPLOAD_BASE);
		this.installMockFetch(this.mockServer);
		this.player = new PcmStreamPlayer({ context: this.context, streamRate: 24000 });
		await this.openUploads();
		this.requireUploadsOpen();
		this.mockHandle = new MockSocketHandle(mockScript());
		this.voice = this.wireVoice(this.mockHandle);
		this.guard = new SessionGuard({ url: `/api/sessions/${this.session.session_id}/end` });
		this.guard.attach();
		this.mockHandle.open();
		this.pushTurn('host', this.session.config.greeting);
		this.exposeMockHandle();
	}

	// startCap builds the session stop beside the socket. The timer ends
	// through the same take end the armed end control calls, so the socket
	// latch below it keeps every close path to one close message. The broker
	// cap bounds the timer. Under the mock flag the run moves the clock by
	// hand, so the proofs never wait on a real duration.
	private startCap(): void {
		if (this.session === null || this.voice === null) return;
		const maxSeconds = this.session.max_session_duration_seconds;
		const finish = () => {
			void this.endTake();
		};
		const onWarn = () => {
			this.capWarning = true;
			this.capText = this.cap?.warnText() ?? '';
			this.emit();
		};
		if (this.mockMode) {
			const clock = new HarnessClock();
			this.capClock = clock;
			this.cap = new SessionCap(finish, { maxSeconds, clock, onWarn });
		} else {
			this.cap = new SessionCap(finish, { maxSeconds, onWarn });
		}
		this.cap.start();
	}

	// stopCap cancels the timer. Every take end path calls this, so the
	// timer never closes a take that already ended another way.
	private stopCap(): void {
		this.cap?.stop();
		this.cap = null;
	}

	private wireVoice(handle: SocketHandle): VoiceSocket {
		if (this.session === null) throw new Error('the take opened with no session');
		return new VoiceSocket(handle, this.session.config, {
			onHostAudio: (samples) => this.handleHostAudio(samples),
			onReplyDone: (interrupted) => this.handleReplyDone(interrupted),
			onUserTranscript: (text) => this.pushTurn('user', text),
			onHostTranscript: (text) => this.pushTurn('host', text),
			onEnded: () => {}
		});
	}

	private async openUploads(): Promise<void> {
		this.userUpload = new ChunkUploader({
			url: UPLOAD_BASE,
			owner: this.owner,
			contentType: 'audio/pcm'
		});
		this.hostUpload = new ChunkUploader({
			url: UPLOAD_BASE,
			owner: this.owner,
			contentType: 'audio/pcm'
		});
		await this.userUpload.start();
		await this.hostUpload.start();
	}

	private handleUserBlock(chunk: CaptureChunk): void {
		if (this.context === null || this.userUpload === null || this.voice === null) return;
		const drained = drainUserBlock(chunk.samples, this.context.sampleRate);
		this.userUpload.append(drained.upload);
		this.voice.sendAudio(drained.socket);
		this.levelDb = measureBlock(chunk.samples).rmsDb;
	}

	private handleHostAudio(samples: Float32Array): void {
		if (this.player === null || this.hostUpload === null) return;
		this.player.push(samples);
		this.hostUpload.append(drainHostBlock(samples));
	}

	private handleReplyDone(interrupted: boolean): void {
		if (this.player === null) return;
		const cut = this.player.flush();
		this.marks.push({ reply: this.replyCount, cutTime: cut, interrupted });
		this.replyCount += 1;
	}

	private pushTurn(role: 'user' | 'host', text: string): void {
		this.turns = [...this.turns.slice(-49), { role, text }];
	}

	private emit(): void {
		this.onChange({
			phase: this.phase,
			notice: this.notice,
			turns: [...this.turns],
			levelDb: this.levelDb,
			elapsed: this.elapsed,
			armed: this.armed,
			greeting: this.greeting,
			capWarning: this.capWarning,
			capText: this.capText,
			completionFailed: this.completion?.status === 'failed'
		});
	}

	private installMockFetch(server: MockUploadServer): void {
		const realFetch = window.fetch.bind(window);
		window.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
			const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
			if (this.mockChallenges.some((part) => url.includes(part))) {
				return new Response('<html><body>challenge</body></html>', {
					status: 200,
					headers: { 'content-type': 'text/html; charset=utf-8' }
				});
			}
			if (url.includes(UPLOAD_BASE)) {
				return server.handle(url, init);
			}
			if (url.includes('/api/sessions/') && url.endsWith('/end')) {
				this.httpEndCount += 1;
				return new Response(JSON.stringify({ ok: true }), {
					status: 200,
					headers: { 'content-type': 'application/json' }
				});
			}
			return realFetch(input, init);
		}) as typeof window.fetch;
		const realBeacon = navigator.sendBeacon.bind(navigator);
		const countHttpEnd = () => {
			this.httpEndCount += 1;
		};
		navigator.sendBeacon = ((url: string | URL) => {
			const text = typeof url === 'string' ? url : url.href;
			if (text.includes('/api/sessions/') && text.endsWith('/end')) {
				countHttpEnd();
				return true;
			}
			return realBeacon(url);
		}) as Navigator['sendBeacon'];
	}

	private exposeMockHandle(): void {
		const target = window as unknown as Record<string, unknown>;
		target['__mockVoice'] = this.harness();
	}

	private async recoverMockUploads(): Promise<void> {
		this.phase = 'recovering';
		this.notice = 'Recovering the persisted upload.';
		this.emit();
		const server = new MockUploadServer(UPLOAD_BASE);
		this.installMockFetch(server);
		const found = await rehydrateServerFromBrowser(server);
		const receipts: MockRecovered['receipts'] = [];
		for (;;) {
			const resumed = await ChunkUploader.resume();
			if (resumed === undefined) break;
			await resumed.finish();
			if (resumed.receipt !== undefined) {
				receipts.push({ id: resumed.receipt.id, sizeBytes: resumed.receipt.sizeBytes });
			}
		}
		const target = window as unknown as Record<string, unknown>;
		target['__mockVoice'] = {
			recovered: (): MockRecovered => ({				sessions: found.sessions,
				chunks: found.chunks,
				receipts
			}),
			serverBytes: (id: string) => [...server.bytes(id)],
			serverIds: () => server.ids()
		};
		this.notice = `Recovered ${receipts.length} uploads over persisted bytes.`;
		this.emit();
	}
}

function formatElapsed(now: number, start: number): string {
	const total = Math.max(0, Math.floor(now - start));
	const minutes = Math.floor(total / 60);
	const seconds = total % 60;
	return `${minutes}:${seconds.toString().padStart(2, '0')}`;
}
