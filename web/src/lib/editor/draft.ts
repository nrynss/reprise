// The draft behind the editor screen. A framework-free controller over
// the library editing model: TranscriptEditor carries words and cuts,
// TranscriptFollower binds them to the player clock, regionsFromCuts
// draws the waveform marks, and AudioPlayer carries playback. The screen
// mirrors a snapshot and calls back in. Local state is the truth until
// the backend answers, so every control works against fixtures.
import { AudioPlayer, computePeaksInWorker, type Peaks } from '@nrynss/chaaya/audio';
import { JobStream, type JobSnapshot } from '@nrynss/chaaya/job';
import {
	TranscriptEditor,
	TranscriptFollower,
	cutSpans,
	regionsFromCuts,
	type WaveformRegion
} from '@nrynss/chaaya/transcript';
import {
	FIXTURE_COLD_OPEN,
	FIXTURE_COLD_REASON,
	FIXTURE_CALLBACK,
	loadFixture,
	quoteRange,
	type EditCut,
	type EditWord
} from './fixture';
import { CONTRAST_PAIRS, EDITOR_THEME_CSS } from './theme';

export interface DecisionRow {
	id: string;
	proposalId: string;
	cutId: string;
	decision: 'reverted';
	reason: string;
}

export type RenderStage = 'idle' | 'confirm' | 'starting' | 'running' | 'done';

export interface ColdOpen {
	start: number;
	end: number;
	reason: string;
	quote: string;
}

export interface CutCard {
	id: string;
	proposalId: string;
	reason: string;
	quote: string;
}

export interface DraftSnapshot {
	ready: boolean;
	demo: boolean;
	notice: string;
	loadError: string | null;
	episodeLabel: string;
	title: string;
	words: EditWord[];
	cuts: EditCut[];
	cutOf: Array<string | null>;
	position: number;
	duration: number;
	playing: boolean;
	peaks: Peaks | null;
	regions: WaveformRegion[];
	coldOpen: ColdOpen;
	coldOpenReverted: boolean;
	hasColdOpen: boolean;
	proposedTitle: string;
	titleReverted: boolean;
	proposedNotes: string;
	notesReverted: boolean;
	cutCards: CutCard[];
	callback: string;
	callbackQuote: string;
	proposedCallback: string;
	notes: string;
	callbackReverted: boolean;
	callbacksCleared: boolean;
	appliedCount: number;
	decisions: DecisionRow[];
	renderStage: RenderStage;
	renderDetail: string;
	gateResult: string | null;
}

export interface DraftOptions {
	episodeId: string;
	onChange: (snap: DraftSnapshot) => void;
}

// Live draft notices in plain words. A stored draft shows no notice at
// all, because the editor already shows its words. An empty draft says
// the transcript has not landed yet. A draft that never loads names the
// retry, never the answer behind it.
export const LIVE_DRAFT_NOTICE = '';
export const LIVE_DRAFT_EMPTY_NOTICE = 'No transcript yet.';
export const LIVE_DRAFT_FAILED_NOTICE = "Couldn't load this draft. Try again.";

// Live finish pass lines in plain words. The screen shows the pass state
// and where to look next, never the mechanics behind it.
export const LIVE_RENDER_RUNNING_DETAIL = 'Finishing your episode.';
export const LIVE_RENDER_DONE_READY_DETAIL = 'Your episode is ready in the gallery.';
export const LIVE_RENDER_DONE_DETAIL = 'Done. Find it in the gallery.';

// Mark done steps in plain words. Confirm names the once-only finish.
// Starting and waiting are status words. A refusal names the retry. A
// pass the screen cannot follow, or one that stops early, points at the
// gallery instead of the mechanics.
export const MARK_DONE_CONFIRM_DETAIL = 'Finishing makes the episode ready. Confirm to start.';
export const MARK_DONE_STARTING_DETAIL = 'Starting…';
export const MARK_DONE_QUEUED_DETAIL = 'Waiting to start.';
export const MARK_DONE_REFUSED_DETAIL = "Couldn't finish this episode. Try again.";
export const MARK_DONE_UNFOLLOWED_DETAIL = 'Finishing. Find it in the gallery.';
export const MARK_DONE_STOPPED_DETAIL = 'Stopped before it finished. Find it in the gallery.';

export function emptyDraft(episodeId: string): DraftSnapshot {
	return {
		ready: false,
		demo: false,
		notice: 'Loading…',
		loadError: null,
		episodeLabel: `Episode ${episodeId}`,
		title: '',
		words: [],
		cuts: [],
		cutOf: [],
		position: 0,
		duration: 0,
		playing: false,
		peaks: null,
		regions: [],
		coldOpen: { start: 0, end: 0, reason: '', quote: '' },
		coldOpenReverted: false,
		hasColdOpen: false,
		proposedTitle: '',
		titleReverted: false,
		proposedNotes: '',
		notesReverted: false,
		cutCards: [],
		callback: '',
		callbackQuote: '',
		proposedCallback: '',
		callbackReverted: false,
		callbacksCleared: false,
		notes: '',
		appliedCount: 0,
		decisions: [],
		renderStage: 'idle',
		renderDetail: '',
		gateResult: null
	};
}

// Seconds as m:ss for the player readout and the slider text.
export function formatTime(seconds: number): string {
	const clamped = Math.max(0, seconds);
	const minutes = Math.floor(clamped / 60);
	const rest = Math.floor(clamped % 60);
	return `${minutes}:${rest.toString().padStart(2, '0')}`;
}

const PEAK_BUCKETS = 120;

// Read one query value by iterating the entries in order. The first
// match is the answer, and a missing name reads null.
export function queryValue(search: string, name: string): string | null {
	for (const [key, value] of new URLSearchParams(search)) {
		if (key === name) return value;
	}
	return null;
}

// Seconds the server asks to wait before the one automatic retry. Reads
// the Retry-After header by iterating entries, capped at a minute. A
// missing or unreadable value waits nothing.
function retryAfterSeconds(headers: Headers): number {
	let seconds = 0;
	for (const [name, value] of headers) {
		if (name.toLowerCase() !== 'retry-after') continue;
		const parsed = Number.parseInt(value, 10);
		if (Number.isFinite(parsed)) seconds = Math.max(0, parsed);
	}
	return Math.min(seconds, 60);
}

// Wait a whole number of seconds. The rate limit names the pause, so the
// caller honors it instead of polling.
function waitSeconds(seconds: number): Promise<void> {
	return new Promise((resolve) => {
		setTimeout(resolve, Math.max(0, seconds) * 1000);
	});
}

// Decode stored audio bytes without touching the playback element. An
// offline context decodes with no gesture and no audible output.
function decodeAudioBytes(bytes: ArrayBuffer): Promise<AudioBuffer> {
	const context = new OfflineAudioContext(1, 1, 44100);
	return context.decodeAudioData(bytes);
}

// Fixture proposal ids follow one pattern: prop-<cut id> for cuts, and
// a fixed id per kind for the rest. Live ids come from the stored rows.
function fixtureProposalId(cutId: string): string {
	return `prop-${cutId}`;
}

// The stored proposal id behind each non-cut kind, or empty when the
// draft stores none of that kind.
interface StoredIds {
	coldOpen: string;
	title: string;
	notes: string;
	callback: string;
}

const FIXTURE_IDS: StoredIds = {
	coldOpen: 'prop-cold-open',
	title: 'prop-title',
	notes: 'prop-notes',
	callback: 'prop-callback'
};

const FIXTURE_ROW_IDS: StoredIds = {
	coldOpen: 'dec-cold-open',
	title: 'dec-title',
	notes: 'dec-notes',
	callback: 'dec-callback'
};

// The latest stored decision values. A cut applies only when its latest
// row reads accepted, which is the rule the render reads. Every other
// kind applies until a row reads reverted.
const DECISION_ACCEPTED = 'accepted';
const DECISION_REVERTED = 'reverted';

interface StoredProposal {
	id: string;
	kind: string;
	start: number;
	end: number;
	reason: string;
	decision: string;
}

interface StoredDraft {
	title: string;
	words: EditWord[];
	proposals: StoredProposal[];
	audioUrl: string;
	previewAudioUrl: string;
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === 'object' && value !== null;
}

function textOf(body: Record<string, unknown>, name: string): string {
	const value = body[name];
	return typeof value === 'string' ? value : '';
}

function numberOf(body: Record<string, unknown>, name: string): number {
	const value = body[name];
	return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

// The speaker label a stored word carries. The backend stores user for
// the guest and host for the host. Older rows carry neither, and an
// unknown speaker stays unknown instead of reading as the guest.
function speakerOf(item: Record<string, unknown>): string {
	const raw = textOf(item, 'speaker');
	if (raw === 'host') return 'host';
	if (raw === 'user' || raw === 'you') return 'you';
	return '';
}

// Read one detail body. Words and cut ranges use the stored indexes.
// A body with no episode is not a detail. A detail with no words is
// still a draft, and the caller keeps it empty.
function readStoredDraft(value: unknown, episodeId: string): StoredDraft {
	if (!isRecord(value)) throw new Error('detail is not an object');
	const episode = value['episode'];
	if (!isRecord(episode)) throw new Error('detail holds no episode');
	const rawWords = Array.isArray(value['words']) ? value['words'] : [];
	const words: EditWord[] = [];
	for (const item of rawWords) {
		if (!isRecord(item)) continue;
		words.push({
			start: numberOf(item, 'start'),
			end: numberOf(item, 'end'),
			text: textOf(item, 'text'),
			speaker: speakerOf(item)
		});
	}
	const rawProposals = Array.isArray(value['proposals']) ? value['proposals'] : [];
	const proposals: StoredProposal[] = [];
	for (const item of rawProposals) {
		if (!isRecord(item)) continue;
		proposals.push({
			id: textOf(item, 'id'),
			kind: textOf(item, 'kind'),
			start: numberOf(item, 'start_word'),
			end: numberOf(item, 'end_word'),
			reason: textOf(item, 'reason'),
			decision: textOf(item, 'decision')
		});
	}
	const title = textOf(episode, 'title') || `Episode ${episodeId}`;
	return {
		title,
		words,
		proposals,
		audioUrl: textOf(value, 'audio_url'),
		previewAudioUrl: textOf(value, 'preview_audio_url')
	};
}

// CutProposals maps a cut id to the stored proposal behind it. A missing
// id reads undefined, as a Map lookup does.
type CutProposals = Record<string, string | undefined>;

// emptyCutProposals builds a record with no prototype, so inherited keys
// such as constructor never read as a stored proposal.
function emptyCutProposals(): CutProposals {
	return Object.create(null) as CutProposals;
}

export class DraftController {
	readonly episodeId: string;
	readonly player = new AudioPlayer();
	editor: TranscriptEditor | null = null;
	follower: TranscriptFollower | null = null;
	private stream: JobStream | null = null;
	private cleanups: Array<() => void> = [];
	private followTimer: number | null = null;
	private followWarned = false;
	private peaksWarned = false;
	private followedJob = '';
	private snap: DraftSnapshot;
	private readonly onChange: (snap: DraftSnapshot) => void;
	private fixtureMode = true;
	// Live cut ids map to the stored proposal each decision row names. A
	// null-prototype record keeps an id like "constructor" or "__proto__"
	// from reading or writing an inherited member instead of its own.
	private cutProposals: CutProposals = emptyCutProposals();
	private storedIds: StoredIds = FIXTURE_IDS;

	constructor(options: DraftOptions) {
		this.episodeId = options.episodeId;
		this.onChange = options.onChange;
		this.snap = emptyDraft(options.episodeId);
	}

	get snapshot(): DraftSnapshot {
		return this.snap;
	}

	// Open the draft. Fixture answers unless the page asks for the backend
	// and the episode endpoint serves it. Peaks render in a worker after
	// the words land, so a missing worker still leaves a usable screen.
	mount(search: string): void {
		const wantsBackend = queryValue(search, 'fixture') !== '1';
		if (wantsBackend) {
			void this.loadFromBackend();
		} else {
			this.loadFromFixture('');
		}
	}

	destroy(): void {
		this.stopFollowPoll();
		for (const cleanup of this.cleanups) {
			try {
				cleanup();
			} catch {
				// A spent cleanup never blocks the rest.
			}
		}
		this.cleanups = [];
		this.followedJob = '';
		this.stream?.close();
		this.stream = null;
		this.player.pause();
	}

	// Stop the render poll. A finished or abandoned follow leaves no timer.
	private stopFollowPoll(): void {
		if (this.followTimer !== null && typeof window !== 'undefined') {
			window.clearInterval(this.followTimer);
		}
		this.followTimer = null;
	}

	// Retry the backend read after a refusal. The screen offers this
	// control, so a refused episode never reads as owned content.
	retry(): void {
		this.snap = { ...this.snap, loadError: null, notice: 'Loading…' };
		this.emit();
		void this.loadFromBackend();
	}

	// Park a refused or failed read. The draft stays unready with the
	// failure named, and no invented words ever stand in for the episode.
	private failLoad(detail: string): void {
		this.snap = {
			...this.snap,
			ready: false,
			loadError: detail,
			notice: LIVE_DRAFT_FAILED_NOTICE
		};
		this.emit();
	}

	private emit(): void {
		this.onChange(this.snap);
	}

	private loadFromFixture(notice: string): void {
		this.fixtureMode = true;
		this.cutProposals = emptyCutProposals();
		this.storedIds = FIXTURE_IDS;
		const fixture = loadFixture(this.episodeId);
		this.installDraft({
			title: fixture.title,
			words: fixture.words,
			cuts: fixture.cuts,
			duration: fixture.duration,
			coldStart: FIXTURE_COLD_OPEN.start,
			coldEnd: FIXTURE_COLD_OPEN.end,
			coldReason: FIXTURE_COLD_REASON,
			hasColdOpen: true,
			callback: FIXTURE_CALLBACK,
			callbackQuote: quoteRange(fixture.words, 48, 57),
			notes:
				fixture.proposals.find((proposal) => proposal.kind === 'show_notes')?.reason ?? '',
			audioUrl: fixture.audioUrl,
			channels: fixture.channels.map((channel) => channel.slice()),
			reverted: { coldOpen: false, title: false, notes: false, callback: false },
			decisions: [],
			notice,
			demo: true
		});
	}

	private async loadFromBackend(): Promise<void> {
		let response: Response | null;
		try {
			response = await fetch(`/api/episodes/${encodeURIComponent(this.episodeId)}`);
			if (response.status === 429) {
				await waitSeconds(retryAfterSeconds(response.headers));
				response = await fetch(`/api/episodes/${encodeURIComponent(this.episodeId)}`);
			}
		} catch {
			response = null;
		}
		if (response === null) {
			this.failLoad('no answer');
			return;
		}
		if (!response.ok) {
			this.failLoad(`status ${response.status}`);
			return;
		}
		let stored: StoredDraft;
		try {
			stored = readStoredDraft(await response.json(), this.episodeId);
		} catch {
			this.failLoad(`status ${response.status}`);
			return;
		}
		const words = stored.words;
		// A cut shows as applied only while the render would apply it. A
		// reverted cut stays off the transcript and keeps its decision row,
		// so a reload shows what the render will do.
		const cuts: EditCut[] = [];
		const decisions: DecisionRow[] = [];
		const cutProposals = emptyCutProposals();
		for (const proposal of stored.proposals) {
			if (proposal.kind !== 'cut' || !proposal.id) continue;
			if (proposal.decision === DECISION_ACCEPTED) {
				cuts.push({
					id: proposal.id,
					range: { start: proposal.start, end: proposal.end },
					reason: proposal.reason
				});
				cutProposals[proposal.id] = proposal.id;
			} else if (proposal.decision === DECISION_REVERTED) {
				decisions.push({
					id: `dec-${proposal.id}`,
					proposalId: proposal.id,
					cutId: proposal.id,
					decision: 'reverted',
					reason: proposal.reason
				});
			}
		}
		// The latest proposal of each kind is the one the render reads.
		const latest = (kind: string): StoredProposal | undefined =>
			stored.proposals.filter((proposal) => proposal.kind === kind && proposal.id).at(-1);
		const cold = latest('cold_open');
		const title = latest('title');
		const callback = latest('callback');
		const notes = latest('show_notes');
		const isReverted = (proposal: StoredProposal | undefined): boolean =>
			proposal?.decision === DECISION_REVERTED;
		for (const proposal of [cold, title, notes, callback]) {
			if (!proposal || !isReverted(proposal)) continue;
			decisions.push({
				id: `dec-${proposal.id}`,
				proposalId: proposal.id,
				cutId: '',
				decision: 'reverted',
				reason: proposal.reason
			});
		}
		this.fixtureMode = false;
		this.cutProposals = cutProposals;
		this.storedIds = {
			coldOpen: cold?.id ?? '',
			title: title?.id ?? '',
			notes: notes?.id ?? '',
			callback: callback?.id ?? ''
		};
		this.installDraft({
			title: stored.title,
			words,
			cuts,
			duration: words.length > 0 ? (words[words.length - 1]?.end ?? 0) : 0,
			coldStart: cold?.start ?? 0,
			coldEnd: cold?.end ?? 0,
			coldReason: cold?.reason ?? '',
			hasColdOpen: cold !== undefined,
			callback: callback?.reason ?? '',
			callbackQuote:
				callback && words.length > 0
					? quoteRange(words, callback.start, Math.min(callback.end, words.length - 1))
					: '',
			notes: notes?.reason ?? '',
			// The preview carries both voices on the word clock, so it
			// plays first. The stem address stays the fallback while no
			// preview exists. The follower keeps its skip spans, because
			// the clock is unchanged.
			audioUrl: stored.previewAudioUrl || stored.audioUrl,
			channels: null,
			reverted: {
				coldOpen: isReverted(cold),
				title: isReverted(title),
				notes: isReverted(notes),
				callback: isReverted(callback)
			},
			decisions,
			notice: words.length > 0 ? LIVE_DRAFT_NOTICE : LIVE_DRAFT_EMPTY_NOTICE,
			demo: false
		});
	}

	private installDraft(args: {
		title: string;
		words: EditWord[];
		cuts: EditCut[];
		duration: number;
		coldStart: number;
		coldEnd: number;
		coldReason: string;
		hasColdOpen: boolean;
		callback: string;
		callbackQuote: string;
		notes: string;
		audioUrl: string;
		channels: Float32Array[] | null;
		reverted: { coldOpen: boolean; title: boolean; notes: boolean; callback: boolean };
		decisions: DecisionRow[];
		notice: string;
		demo: boolean;
	}): void {
		this.editor = new TranscriptEditor(args.words);
		for (const cut of args.cuts) {
			this.editor.cuts.push({ ...cut, range: { ...cut.range } });
		}
		this.follower = new TranscriptFollower(this.editor, this.player);
		if (args.audioUrl) this.player.load(args.audioUrl);
		this.snap = {
			...this.snap,
			ready: true,
			demo: args.demo,
			notice: args.notice,
			loadError: null,
			title: args.title,
			episodeLabel: `Episode ${this.episodeId} · draft`,
			words: args.words,
			duration: args.duration,
			coldOpen: {
				start: args.coldStart,
				end: args.coldEnd,
				reason: args.coldReason,
				quote: quoteRange(args.words, args.coldStart, args.coldEnd)
			},
			coldOpenReverted: args.reverted.coldOpen,
			hasColdOpen: args.hasColdOpen,
			proposedTitle: args.title,
			titleReverted: args.reverted.title,
			proposedNotes: args.notes,
			notesReverted: args.reverted.notes,
			callback: args.reverted.callback ? '' : args.callback,
			callbackQuote: args.reverted.callback ? '' : args.callbackQuote,
			proposedCallback: args.callback,
			callbackReverted: args.reverted.callback,
			callbacksCleared: args.reverted.callback,
			decisions: args.decisions,
			renderStage: 'idle',
			renderDetail: '',
			notes: args.reverted.notes ? '' : args.notes
		};
		if (args.reverted.title) this.snap = { ...this.snap, title: `Episode ${this.episodeId}` };
		this.refreshCuts();
		this.emit();
		if (args.channels) {
			void computePeaksInWorker(args.channels, PEAK_BUCKETS).then(
				(peaks) => {
					this.snap = { ...this.snap, peaks };
					this.emit();
				},
				(error) => {
					if (!this.peaksWarned) {
						this.peaksWarned = true;
						console.warn('Peaks unavailable. The waveform keeps its flat line.', error);
					}
				}
			);
		} else if (args.audioUrl) {
			void this.loadLivePeaks(args.audioUrl);
		}
	}

	// Peaks for a stored draft. The detail carries no buckets, so the
	// screen fetches the same bytes the element plays and decodes them
	// away from the main thread. A failure keeps the flat line, warns
	// once, and never blocks playback.
	private async loadLivePeaks(audioUrl: string): Promise<void> {
		try {
			const response = await fetch(audioUrl);
			if (!response.ok) throw new Error(`peaks ${response.status}`);
			const decoded = await decodeAudioBytes(await response.arrayBuffer());
			const channels: Float32Array[] = [];
			for (let index = 0; index < decoded.numberOfChannels; index += 1) {
				channels.push(decoded.getChannelData(index).slice());
			}
			const peaks = await computePeaksInWorker(channels, PEAK_BUCKETS);
			this.snap = { ...this.snap, peaks };
			this.emit();
		} catch (error) {
			if (!this.peaksWarned) {
				this.peaksWarned = true;
				console.warn('Live peaks unavailable. The waveform keeps its flat line.', error);
			}
		}
	}

	// Rebuild every cut-derived view after a revert: the cards, the count,
	// the waveform regions, and the spans the follower skips.
	private refreshCuts(): void {
		if (!this.editor) return;
		const cuts = this.editor.cuts.map((cut) => ({
			...cut,
			range: { ...cut.range }
		}));
		const words = this.snap.words;
		const cutOf: Array<string | null> = words.map((_, index) => {
			for (const cut of cuts) {
				if (index >= cut.range.start && index <= cut.range.end) return cut.id;
			}
			return null;
		});
		this.snap = {
			...this.snap,
			cuts,
			cutOf,
			cutCards: cuts.map((cut) => ({
				id: cut.id,
				proposalId: this.proposalIdForCut(cut.id),
				reason: cut.reason,
				quote: quoteRange(words, cut.range.start, cut.range.end)
			})),
			appliedCount: cuts.length,
			regions: regionsFromCuts(words, cuts, PEAK_BUCKETS, this.snap.duration)
		};
	}

	// The proposal id a cut decision names. A fixture cut follows the
	// fixture pattern. A live cut names its stored proposal, or empty
	// when no stored proposal backs it.
	private proposalIdForCut(cutId: string): string {
		if (this.fixtureMode) return fixtureProposalId(cutId);
		return this.cutProposals[cutId] ?? '';
	}

	// Revert one cut with one action. The decision row is the revertible
	// record. A fixture draft keeps the local row as its record. A live
	// draft puts the cut back when the server refuses the row, because
	// the render reads the stored rows and would still cut those words.
	revertCut(cutId: string): void {
		if (!this.editor) return;
		const proposalId = this.proposalIdForCut(cutId);
		if (!proposalId) {
			this.snap = { ...this.snap, notice: 'Nothing to revert. That cut already stands.' };
			this.emit();
			return;
		}
		const index = this.editor.cuts.findIndex((cut) => cut.id === cutId);
		const result = this.editor.revert(cutId);
		if (typeof result === 'string') {
			this.snap = { ...this.snap, notice: 'Nothing to revert. That cut already stands.' };
			this.emit();
			return;
		}
		const row: DecisionRow = {
			id: `dec-${result.id}`,
			proposalId,
			cutId: result.id,
			decision: 'reverted',
			reason: result.reason
		};
		this.snap = {
			...this.snap,
			decisions: [...this.snap.decisions, row],
			notice: `Reverted. ${result.reason}`
		};
		this.refreshCuts();
		this.emit();
		const restored = { ...result, range: { ...result.range } };
		this.postDecision(row, () => {
			if (!this.editor) return;
			this.editor.cuts.splice(Math.max(0, index), 0, restored);
			this.refreshCuts();
		});
	}

	// Post one decision row. A fixture draft has no stored rows, so a
	// refusal there changes nothing on screen. A live refusal drops the
	// local row, runs undo, and names the retry.
	private postDecision(row: DecisionRow, undo: () => void): void {
		const live = !this.fixtureMode;
		void fetch(`/api/episodes/${encodeURIComponent(this.episodeId)}/decisions`, {
			method: 'POST',
			headers: { 'content-type': 'application/json' },
			body: JSON.stringify({ proposal_id: row.proposalId, decision: row.decision })
		})
			.then((response) => (response.ok ? null : `status ${response.status}`))
			.catch(() => 'no answer')
			.then((failure) => {
				if (failure === null || !live) return;
				this.snap = {
					...this.snap,
					decisions: this.snap.decisions.filter((held) => held.id !== row.id)
				};
				undo();
				this.snap = {
					...this.snap,
					notice: "Couldn't keep that change. Try again."
				};
				this.emit();
			});
	}

	// Write one decision row for a non-cut revert. A second revert of the
	// same kind changes nothing, so the row stays the single record. A
	// live draft with no stored proposal of that kind has nothing to
	// revert.
	private recordNonCutRevert(kind: keyof StoredIds, reason: string): DecisionRow | null {
		const proposalId = this.storedIds[kind];
		if (!proposalId) {
			this.snap = { ...this.snap, notice: 'Nothing to revert. It already stands.' };
			this.emit();
			return null;
		}
		if (this.snap.decisions.some((row) => row.proposalId === proposalId)) {
			this.snap = { ...this.snap, notice: 'Already reverted. Nothing changed.' };
			this.emit();
			return null;
		}
		const row: DecisionRow = {
			id: this.fixtureMode ? FIXTURE_ROW_IDS[kind] : `dec-${proposalId}`,
			proposalId,
			cutId: '',
			decision: 'reverted',
			reason
		};
		this.snap = { ...this.snap, decisions: [...this.snap.decisions, row] };
		return row;
	}

	// Revert the cold open with one action. The range stays on screen
	// struck through, while the flag tells the render to start at the top.
	revertColdOpen(): void {
		if (!this.editor) return;
		if (this.snap.coldOpenReverted) {
			this.snap = { ...this.snap, notice: 'Already reverted. Nothing changed.' };
			this.emit();
			return;
		}
		const reason = this.snap.coldOpen.reason || 'Cold open.';
		const row = this.recordNonCutRevert('coldOpen', reason);
		if (!row) return;
		this.snap = {
			...this.snap,
			coldOpenReverted: true,
			notice: 'Cold open reverted. The episode starts at the top.'
		};
		this.emit();
		this.postDecision(row, () => {
			this.snap = { ...this.snap, coldOpenReverted: false };
		});
	}

	// Revert the title with one action. The heading falls back to the
	// plain episode number, and the suggestion stays on screen struck through.
	revertTitle(): void {
		if (!this.editor) return;
		if (this.snap.titleReverted) {
			this.snap = { ...this.snap, notice: 'Already reverted. Nothing changed.' };
			this.emit();
			return;
		}
		const reason = this.snap.proposedTitle || 'Title.';
		const row = this.recordNonCutRevert('title', reason);
		if (!row) return;
		const title = this.snap.title;
		this.snap = {
			...this.snap,
			title: `Episode ${this.episodeId}`,
			titleReverted: true,
			notice: 'Reverted the title.'
		};
		this.emit();
		this.postDecision(row, () => {
			this.snap = { ...this.snap, title, titleReverted: false };
		});
	}

	// Revert the show notes with one action. Notes fall back to empty,
	// and the suggestion stays on screen struck through.
	revertNotes(): void {
		if (!this.editor) return;
		if (this.snap.notesReverted) {
			this.snap = { ...this.snap, notice: 'Already reverted. Nothing changed.' };
			this.emit();
			return;
		}
		const reason = this.snap.proposedNotes || 'Show notes.';
		const row = this.recordNonCutRevert('notes', reason);
		if (!row) return;
		const notes = this.snap.notes;
		this.snap = { ...this.snap, notes: '', notesReverted: true, notice: 'Reverted the show notes.' };
		this.emit();
		this.postDecision(row, () => {
			this.snap = { ...this.snap, notes, notesReverted: false };
		});
	}

	// Revert the callback with one action. The suggestion state flips and the
	// planted line clears with it, so the next opening cites nothing new.
	revertCallback(): void {
		if (!this.editor) return;
		if (this.snap.callbackReverted) {
			this.snap = { ...this.snap, notice: 'Already reverted. Nothing changed.' };
			this.emit();
			return;
		}
		const reason = this.snap.proposedCallback || 'Planted callback.';
		const row = this.recordNonCutRevert('callback', reason);
		if (!row) return;
		const { callback, callbackQuote } = this.snap;
		this.snap = {
			...this.snap,
			callback: '',
			callbackQuote: '',
			callbackReverted: true,
			callbacksCleared: true,
			notice: 'Reverted the callback and cleared it from the next opening.'
		};
		this.emit();
		this.postDecision(row, () => {
			this.snap = {
				...this.snap,
				callback,
				callbackQuote,
				callbackReverted: false,
				callbacksCleared: false
			};
		});
	}

	// Seek playback to one word start. The position state moves at once so
	// the readout answers without waiting on the element.
	seekToWord(index: number): void {
		const word = this.snap.words[index];
		if (!word || !this.follower) return;
		this.follower.seekToWord(index);
		this.snap = { ...this.snap, position: word.start };
		this.emit();
	}

	// Name the refusal behind one play call. The player stores the
	// browser reason on every refused call and clears it on success.
	// The log carries the page and which call refused, so the next
	// live run shows whether the retry cures the first press.
	private logPlayRefusal(attempt: string): void {
		const refusal = this.player.lastPlayError;
		if (refusal) {
			console.warn(`Draft playback refused on ${attempt} call: ${refusal.name}: ${refusal.message}`);
		} else {
			console.warn(`Draft playback refused on ${attempt} call with no reason stored.`);
		}
	}

	// Adopt the element length once it reports one. The install length
	// comes from the last word end, which can sit short of or past the
	// stored audio. Regions follow the adopted length, and the playhead
	// never reads past it.
	syncDuration(): void {
		const reported = this.player.duration;
		if (!(reported > 0)) return;
		if (reported === this.snap.duration) return;
		this.snap = {
			...this.snap,
			duration: reported,
			position: Math.min(this.snap.position, reported)
		};
		this.refreshCuts();
		this.emit();
	}

	seekTo(seconds: number): void {
		if (!this.editor) return;
		this.syncDuration();
		const clamped = Math.max(0, Math.min(this.snap.duration, seconds));
		this.player.seek(clamped);
		this.snap = { ...this.snap, position: this.player.currentTime || clamped };
		this.emit();
	}

	// Play the cold open from its range. The seek lands first, so a refused
	// play still leaves the playhead where the open starts.
	async previewColdOpen(): Promise<void> {
		if (!this.editor) return;
		this.syncDuration();
		const start = this.snap.words[this.snap.coldOpen.start]?.start ?? 0;
		this.player.seek(start);
		this.snap = { ...this.snap, position: start };
		this.emit();
		// The first press primes the shared element, so it can refuse
		// while the gesture still counts. The retry runs at once in the
		// same handler, so it keeps the gesture. Only a second refusal
		// shows the notice.
		let ok = await this.player.play();
		if (!ok) {
			this.logPlayRefusal('first');
			ok = await this.player.play();
			if (!ok) this.logPlayRefusal('retried');
		}
		this.snap = {
			...this.snap,
			playing: ok,
			notice: ok ? 'Playing the cold open.' : "Couldn't play. Try again."
		};
		this.emit();
	}

	async togglePlay(): Promise<void> {
		this.syncDuration();
		if (this.player.playing) {
			this.player.pause();
			this.snap = { ...this.snap, playing: false };
			this.emit();
			return;
		}
		// The first press primes the shared element, so it can refuse
		// while the gesture still counts. The retry runs at once in the
		// same handler, so it keeps the gesture. Only a second refusal
		// shows the notice.
		let ok = await this.player.play();
		if (!ok) {
			this.logPlayRefusal('first');
			ok = await this.player.play();
			if (!ok) this.logPlayRefusal('retried');
		}
		this.snap = {
			...this.snap,
			playing: ok,
			position: this.player.currentTime || this.snap.position,
			notice: ok ? this.snap.notice : "Couldn't play. Try again."
		};
		this.emit();
	}

	// Mark done arms first and finishes the episode on confirm. Confirming
	// is the only path that leaves the draft, and it always passes here.
	markDone(): void {
		if (this.snap.renderStage === 'idle') {
			this.snap = {
				...this.snap,
				renderStage: 'confirm',
				renderDetail: MARK_DONE_CONFIRM_DETAIL
			};
			this.emit();
			return;
		}
		if (this.snap.renderStage !== 'confirm' && this.snap.renderStage !== 'done') return;
		this.snap = { ...this.snap, renderStage: 'starting', renderDetail: MARK_DONE_STARTING_DETAIL };
		this.emit();
		void fetch(`/api/episodes/${encodeURIComponent(this.episodeId)}/done`, { method: 'POST' })
			.then(async (response) => {
				if (!response.ok) throw new Error(`done ${response.status}`);
				try {
					const body = (await response.json()) as { job_id?: string; queued?: boolean };
					if (body.queued) {
						this.onRenderQueued();
						return;
					}
					this.onRenderStarted(body.job_id ?? null);
				} catch (error) {
					this.onFollowFailed(error);
				}
			})
			.catch(() => {
				if (this.fixtureMode) {
					this.onRenderStarted(null);
					return;
				}
				this.snap = {
					...this.snap,
					renderStage: 'done',
					renderDetail: MARK_DONE_REFUSED_DETAIL
				};
				this.emit();
			});
	}

	private onRenderQueued(): void {
		this.snap = {
			...this.snap,
			renderStage: 'running',
			renderDetail: MARK_DONE_QUEUED_DETAIL
		};
		this.emit();
	}

	private onRenderStarted(jobId: string | null): void {
		if (jobId && !this.fixtureMode) {
			this.snap = {
				...this.snap,
				renderStage: 'running',
				renderDetail: LIVE_RENDER_RUNNING_DETAIL
			};
			this.emit();
			this.followRender(jobId);
			return;
		}
		this.snap = {
			...this.snap,
			renderStage: 'running',
			renderDetail: LIVE_RENDER_RUNNING_DETAIL
		};
		this.emit();
	}

	// Follow the render job over the event feed. Only attached when the
	// backend started a real job, so fixtures never open a dead stream.
	// The explicit runner keeps the attach working outside a component,
	// where the mark done answer lands. The poll below then moves the
	// detail from running to done.
	private followRender(jobId: string): void {
		const fetchState = async (): Promise<JobSnapshot> => {
			const response = await fetch(`/api/jobs/${encodeURIComponent(jobId)}`);
			if (!response.ok) throw new Error(`job ${response.status}`);
			return (await response.json()) as JobSnapshot;
		};
		for (const cleanup of this.cleanups) {
			try {
				cleanup();
			} catch {
				// A spent cleanup never blocks the rest.
			}
		}
		this.cleanups = [];
		this.stopFollowPoll();
		this.stream?.close();
		this.stream = new JobStream({
			url: `/api/jobs/${encodeURIComponent(jobId)}/events`,
			fetchState
		});
		this.stream.attach((task) => {
			this.cleanups.push(task());
		});
		this.followedJob = jobId;
		this.startFollowPoll();
	}

	// The finish started but its feed could not be followed. The finish
	// itself runs, so the gallery stays the source of truth. Logged once.
	private onFollowFailed(error: unknown): void {
		if (!this.followWarned) {
			this.followWarned = true;
			console.warn('Finish started but progress could not be followed.', error);
		}
		this.snap = {
			...this.snap,
			renderStage: 'running',
			renderDetail: MARK_DONE_UNFOLLOWED_DETAIL
		};
		this.emit();
	}

	// Poll the followed feed until it ends. The waiting line stands
	// while the finish runs. A done finish reads the episode once, so a
	// ready episode links on.
	private startFollowPoll(): void {
		this.stopFollowPoll();
		if (typeof window === 'undefined') return;
		this.followTimer = window.setInterval(() => {
			void this.checkRender();
		}, 500);
	}

	private async checkRender(): Promise<void> {
		const stream = this.stream;
		const jobId = this.followedJob;
		if (!stream || !jobId) return;
		if (stream.status === 'done') {
			this.stopFollowPoll();
			await this.onRenderDone();
			return;
		}
		if (
			stream.status === 'error' ||
			stream.status === 'cancelled' ||
			stream.status === 'interrupted'
		) {
			this.stopFollowPoll();
			this.snap = {
				...this.snap,
				renderStage: 'done',
				renderDetail: MARK_DONE_STOPPED_DETAIL
			};
			this.emit();
		}
	}

	private async onRenderDone(): Promise<void> {
		let ready = false;
		try {
			const response = await fetch(`/api/episodes/${encodeURIComponent(this.episodeId)}`);
			if (response.ok) {
				const body: unknown = await response.json();
				if (isRecord(body)) {
					const episode = body['episode'];
					if (isRecord(episode) && episode['state'] === 'ready') ready = true;
				}
			}
		} catch {
			ready = false;
		}
		if (ready) {
			this.snap = {
				...this.snap,
				renderStage: 'done',
				renderDetail: LIVE_RENDER_DONE_READY_DETAIL
			};
		} else {
			this.snap = {
				...this.snap,
				renderStage: 'done',
				renderDetail: LIVE_RENDER_DONE_DETAIL
			};
		}
		this.emit();
	}

	cancelMarkDone(): void {
		if (this.snap.renderStage !== 'confirm') return;
		this.snap = { ...this.snap, renderStage: 'idle', renderDetail: '' };
		this.emit();
	}

	setGateResult(result: string): void {
		this.snap = { ...this.snap, gateResult: result };
		this.emit();
	}

	// The removed spans in episode seconds, for probes and tests.
	removedSpans(): Array<{ start: number; end: number }> {
		if (!this.editor) return [];
		return cutSpans(this.snap.words, this.editor.cuts);
	}
}

// Run both library gates against the rendered screen. The accessibility
// gate reads the live main element, and the contrast gate measures the
// same palette the screen draws. Returns the one line the screen shows.
export async function runEditorGates(root: HTMLElement): Promise<string> {
	try {
		const { a11yGate, contrastGate } = await import('@nrynss/chaaya/testing');
		await a11yGate(root);
		contrastGate(EDITOR_THEME_CSS, CONTRAST_PAIRS);
		return 'Gates passed: accessibility and contrast.';
	} catch (error) {
		return `Gate failed: ${error instanceof Error ? error.message : 'unknown'}`;
	}
}
