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
	notice: string;
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

export function emptyDraft(episodeId: string): DraftSnapshot {
	return {
		ready: false,
		notice: 'Loading the draft.',
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

// Proposal ids mirror the fixture: prop-<cut id> for cuts.
function proposalIdForCut(cutId: string): string {
	return `prop-${cutId}`;
}

export class DraftController {
	readonly episodeId: string;
	readonly player = new AudioPlayer();
	editor: TranscriptEditor | null = null;
	follower: TranscriptFollower | null = null;
	private stream: JobStream | null = null;
	private snap: DraftSnapshot;
	private readonly onChange: (snap: DraftSnapshot) => void;
	private fixtureMode = true;

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
			this.loadFromFixture('Scripted draft. No backend needed.');
		}
	}

	destroy(): void {
		this.stream?.close();
		this.stream = null;
		this.player.pause();
	}

	private emit(): void {
		this.onChange(this.snap);
	}

	private loadFromFixture(notice: string): void {
		this.fixtureMode = true;
		const fixture = loadFixture(this.episodeId);
		this.installDraft({
			title: fixture.title,
			words: fixture.words,
			cuts: fixture.cuts,
			duration: fixture.duration,
			coldStart: FIXTURE_COLD_OPEN.start,
			coldEnd: FIXTURE_COLD_OPEN.end,
			coldReason: FIXTURE_COLD_REASON,
			callback: FIXTURE_CALLBACK,
			callbackQuote: quoteRange(fixture.words, 48, 57),
			notes:
				fixture.proposals.find((proposal) => proposal.kind === 'show_notes')?.reason ?? '',
			audioUrl: fixture.audioUrl,
			channels: fixture.channels.map((channel) => channel.slice()),
			notice
		});
	}

	private async loadFromBackend(): Promise<void> {
		try {
			const response = await fetch(`/api/episodes/${encodeURIComponent(this.episodeId)}`);
			if (!response.ok) throw new Error(`episode ${response.status}`);
			const body = (await response.json()) as {
				title?: string;
				words?: Array<{ start: number; end: number; text: string; speaker?: string }>;
				proposals?: Array<{ id: string; kind: string; start: number; end: number; reason: string }>;
				audioUrl?: string;
			};
			if (!Array.isArray(body.words) || body.words.length === 0) throw new Error('no words');
			const words: EditWord[] = body.words.map((word) => ({
				start: word.start,
				end: word.end,
				text: word.text,
				speaker: word.speaker ?? 'you'
			}));
			const cuts: EditCut[] = (body.proposals ?? [])
				.filter((proposal) => proposal.kind === 'cut')
				.map((proposal, index) => ({
					id: proposal.id || `cut-${index + 1}`,
					range: { start: proposal.start, end: proposal.end },
					reason: proposal.reason
				}));
			const cold = (body.proposals ?? []).find((proposal) => proposal.kind === 'cold_open');
			const callback = (body.proposals ?? []).find((proposal) => proposal.kind === 'callback');
			const notes = (body.proposals ?? []).find((proposal) => proposal.kind === 'show_notes');
			this.fixtureMode = false;
			this.installDraft({
				title: body.title ?? `Episode ${this.episodeId}`,
				words,
				cuts,
				duration: words.length > 0 ? (words[words.length - 1]?.end ?? 0) : 0,
				coldStart: cold?.start ?? 0,
				coldEnd: cold?.end ?? 0,
				coldReason: cold?.reason ?? '',
				callback: callback?.reason ?? '',
				callbackQuote:
					callback && words.length > 0
						? quoteRange(words, callback.start, Math.min(callback.end, words.length - 1))
						: '',
				notes: notes?.reason ?? '',
				audioUrl: body.audioUrl ?? '',
				channels: null,
				notice: 'Draft loaded.'
			});
		} catch {
			this.loadFromFixture('The episode endpoint refused, so this is the scripted draft.');
		}
	}

	private installDraft(args: {
		title: string;
		words: EditWord[];
		cuts: EditCut[];
		duration: number;
		coldStart: number;
		coldEnd: number;
		coldReason: string;
		callback: string;
		callbackQuote: string;
		notes: string;
		audioUrl: string;
		channels: Float32Array[] | null;
		notice: string;
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
			notice: args.notice,
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
			coldOpenReverted: false,
			proposedTitle: args.title,
			titleReverted: false,
			proposedNotes: args.notes,
			notesReverted: false,
			callback: args.callback,
			callbackQuote: args.callbackQuote,
			proposedCallback: args.callback,
			callbackReverted: false,
			callbacksCleared: false,
			decisions: [],
			renderStage: 'idle',
			renderDetail: '',
			notes: args.notes
		};
		this.refreshCuts();
		this.emit();
		if (args.channels) {
			void computePeaksInWorker(args.channels, PEAK_BUCKETS).then(
				(peaks) => {
					this.snap = { ...this.snap, peaks };
					this.emit();
				},
				() => {
					this.snap = { ...this.snap, notice: `${this.snap.notice} Peaks unavailable.` };
					this.emit();
				}
			);
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
				proposalId: proposalIdForCut(cut.id),
				reason: cut.reason,
				quote: quoteRange(words, cut.range.start, cut.range.end)
			})),
			appliedCount: cuts.length,
			regions: regionsFromCuts(words, cuts, PEAK_BUCKETS, this.snap.duration)
		};
	}

	// Revert one cut with one action. The decision row is the revertible
	// record, written locally first so the screen answers even when the
	// decisions endpoint is still a stub.
	revertCut(cutId: string): void {
		if (!this.editor) return;
		const result = this.editor.revert(cutId);
		if (typeof result === 'string') {
			this.snap = { ...this.snap, notice: `No cut named ${cutId}. Nothing changed.` };
			this.emit();
			return;
		}
		const row: DecisionRow = {
			id: `dec-${result.id}`,
			proposalId: proposalIdForCut(result.id),
			cutId: result.id,
			decision: 'reverted',
			reason: result.reason
		};
		this.snap = {
			...this.snap,
			decisions: [...this.snap.decisions, row],
			notice: `Reverted one cut: ${result.reason}`
		};
		this.refreshCuts();
		this.emit();
		this.postDecision(row.proposalId);
	}

	// Post one decision row to the decisions endpoint. The local row above
	// is the record, so a refused POST changes nothing on screen.
	private postDecision(proposalId: string): void {
		void fetch(`/api/episodes/${encodeURIComponent(this.episodeId)}/decisions`, {
			method: 'POST',
			headers: { 'content-type': 'application/json' },
			body: JSON.stringify({ proposal_id: proposalId, decision: 'reverted' })
		}).catch(() => {
			// The stub endpoint refuses. The local row above is the record.
		});
	}

	// Write one decision row for a non-cut revert. A second revert of the
	// same kind changes nothing, so the row stays the single record.
	private recordNonCutRevert(proposalId: string, rowId: string, reason: string): boolean {
		if (this.snap.decisions.some((row) => row.proposalId === proposalId)) {
			this.snap = { ...this.snap, notice: `That proposal is already reverted. Nothing changed.` };
			this.emit();
			return false;
		}
		const row: DecisionRow = {
			id: rowId,
			proposalId,
			cutId: '',
			decision: 'reverted',
			reason
		};
		this.snap = { ...this.snap, decisions: [...this.snap.decisions, row] };
		this.postDecision(row.proposalId);
		return true;
	}

	// Revert the cold open with one action. The range stays on screen
	// struck through, while the flag tells the render to start at the top.
	revertColdOpen(): void {
		if (!this.editor) return;
		if (this.snap.coldOpenReverted) {
			this.snap = { ...this.snap, notice: `That proposal is already reverted. Nothing changed.` };
			this.emit();
			return;
		}
		const reason = this.snap.coldOpen.reason || 'Proposed cold open.';
		if (!this.recordNonCutRevert('prop-cold-open', 'dec-cold-open', reason)) return;
		this.snap = {
			...this.snap,
			coldOpenReverted: true,
			notice: `Reverted the cold open. The episode starts at the top.`
		};
		this.emit();
	}

	// Revert the title with one action. The heading falls back to the
	// plain episode number, and the proposal stays on screen struck through.
	revertTitle(): void {
		if (!this.editor) return;
		if (this.snap.titleReverted) {
			this.snap = { ...this.snap, notice: `That proposal is already reverted. Nothing changed.` };
			this.emit();
			return;
		}
		const reason = this.snap.proposedTitle || 'Proposed title.';
		if (!this.recordNonCutRevert('prop-title', 'dec-title', reason)) return;
		this.snap = {
			...this.snap,
			title: `Episode ${this.episodeId}`,
			titleReverted: true,
			notice: `Reverted the title.`
		};
		this.emit();
	}

	// Revert the show notes with one action. Notes fall back to empty,
	// and the proposal stays on screen struck through.
	revertNotes(): void {
		if (!this.editor) return;
		if (this.snap.notesReverted) {
			this.snap = { ...this.snap, notice: `That proposal is already reverted. Nothing changed.` };
			this.emit();
			return;
		}
		const reason = this.snap.proposedNotes || 'Proposed show notes.';
		if (!this.recordNonCutRevert('prop-notes', 'dec-notes', reason)) return;
		this.snap = { ...this.snap, notes: '', notesReverted: true, notice: `Reverted the show notes.` };
		this.emit();
	}

	// Revert the callback with one action. The proposal state flips and the
	// planted row clears with it, so the next opening cites nothing new.
	revertCallback(): void {
		if (!this.editor) return;
		if (this.snap.callbackReverted) {
			this.snap = { ...this.snap, notice: `That proposal is already reverted. Nothing changed.` };
			this.emit();
			return;
		}
		const reason = this.snap.proposedCallback || 'Planted callback.';
		if (!this.recordNonCutRevert('prop-callback', 'dec-callback', reason)) return;
		this.snap = {
			...this.snap,
			callback: '',
			callbackQuote: '',
			callbackReverted: true,
			callbacksCleared: true,
			notice: `Reverted the callback and cleared it from the next opening.`
		};
		this.emit();
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

	seekTo(seconds: number): void {
		if (!this.editor) return;
		const clamped = Math.max(0, Math.min(this.snap.duration, seconds));
		this.player.seek(clamped);
		this.snap = { ...this.snap, position: this.player.currentTime || clamped };
		this.emit();
	}

	// Play the cold open from its range. The seek lands first, so a refused
	// play still leaves the playhead where the open starts.
	async previewColdOpen(): Promise<void> {
		if (!this.editor) return;
		const start = this.snap.words[this.snap.coldOpen.start]?.start ?? 0;
		this.player.seek(start);
		this.snap = { ...this.snap, position: start };
		this.emit();
		const ok = await this.player.play();
		this.snap = {
			...this.snap,
			playing: ok,
			notice: ok ? 'Playing the cold open.' : 'Playback refused. The playhead sits at the open.'
		};
		this.emit();
	}

	async togglePlay(): Promise<void> {
		if (this.player.playing) {
			this.player.pause();
			this.snap = { ...this.snap, playing: false };
			this.emit();
			return;
		}
		const ok = await this.player.play();
		this.snap = {
			...this.snap,
			playing: ok,
			position: this.player.currentTime || this.snap.position,
			notice: ok ? this.snap.notice : 'Playback refused. Press play again after a gesture.'
		};
		this.emit();
	}

	// Mark done arms first and starts the render on confirm. Confirming is
	// the only path that leaves the draft, and it always passes here.
	markDone(): void {
		if (this.snap.renderStage === 'idle') {
			this.snap = {
				...this.snap,
				renderStage: 'confirm',
				renderDetail: 'Marking done renders once and runs analysis. Confirm to start.'
			};
			this.emit();
			return;
		}
		if (this.snap.renderStage !== 'confirm') return;
		this.snap = { ...this.snap, renderStage: 'starting', renderDetail: 'Starting the render.' };
		this.emit();
		void fetch(`/api/episodes/${encodeURIComponent(this.episodeId)}/done`, { method: 'POST' })
			.then(async (response) => {
				if (!response.ok) throw new Error(`done ${response.status}`);
				const body = (await response.json()) as { job_id?: string };
				this.onRenderStarted(body.job_id ?? null);
			})
			.catch(() => {
				// The stub endpoint refuses. The fixture render stands in.
				this.onRenderStarted(null);
			});
	}

	private onRenderStarted(jobId: string | null): void {
		if (jobId && !this.fixtureMode) {
			this.snap = {
				...this.snap,
				renderStage: 'running',
				renderDetail: `Render running as job ${jobId}.`
			};
			this.emit();
			this.followRender(jobId);
			return;
		}
		this.snap = {
			...this.snap,
			renderStage: 'running',
			renderDetail: 'Render running: fixture render. Progress follows the render job when the backend serves it.'
		};
		this.emit();
	}

	// Follow the render job over the event feed. Only attached when the
	// backend started a real job, so fixtures never open a dead stream.
	private followRender(jobId: string): void {
		const fetchState = async (): Promise<JobSnapshot> => {
			const response = await fetch(`/api/jobs/${encodeURIComponent(jobId)}`);
			if (!response.ok) throw new Error(`job ${response.status}`);
			return (await response.json()) as JobSnapshot;
		};
		this.stream?.close();
		this.stream = new JobStream({
			url: `/api/jobs/${encodeURIComponent(jobId)}/events`,
			fetchState
		});
		this.stream.attach();
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
