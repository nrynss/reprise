// A scripted draft the editor draws with no backend. Words carry episode
// clock timings, proposals point at word offsets the same way the model
// pass stores them, and the audio is a generated tone of matching length.
// Nothing here reaches the network.

// One timed word. Shaped like the library word it feeds, so the editor
// takes it without conversion. The speaker stays a plain label.
export interface EditWord {
	start: number;
	end: number;
	text: string;
	speaker: string;
}

// One removed span with its reason. Shaped like the library cut.
export interface EditCut {
	id: string;
	range: { start: number; end: number };
	reason: string;
}

export interface FixtureProposal {
	id: string;
	kind: 'cold_open' | 'cut' | 'title' | 'show_notes' | 'callback';
	start: number;
	end: number;
	reason: string;
}

export interface EditorFixture {
	episodeId: string;
	title: string;
	duration: number;
	sampleRate: number;
	words: EditWord[];
	cuts: EditCut[];
	proposals: FixtureProposal[];
	audioUrl: string;
	channels: Float32Array[];
}

const TEXT: ReadonlyArray<readonly [string, string]> = [
	['you', 'I'],
	['you', 'I'],
	['you', 'mean'],
	['you', 'we'],
	['you', 'used'],
	['you', 'to'],
	['you', 'hide'],
	['you', 'above'],
	['you', 'the'],
	['you', 'shop'],
	['you', 'when'],
	['you', 'the'],
	['you', 'rain'],
	['you', 'came'],
	['you', 'down'],
	['you', 'hard'],
	['you', 'and'],
	['you', 'nobody'],
	['you', 'could'],
	['you', 'find'],
	['you', 'us'],
	['you', 'there'],
	['host', 'That'],
	['host', 'silence'],
	['host', 'before'],
	['host', 'the'],
	['host', 'storm'],
	['host', 'broke'],
	['host', 'taught'],
	['host', 'me'],
	['host', 'more'],
	['host', 'than'],
	['host', 'school'],
	['host', 'ever'],
	['host', 'did'],
	['host', 'about'],
	['host', 'waiting'],
	['you', 'Anyway'],
	['you', 'the'],
	['you', 'bus'],
	['you', 'timetable'],
	['you', 'changed'],
	['you', 'again'],
	['you', 'last'],
	['you', 'Tuesday'],
	['you', 'which'],
	['you', 'reminds'],
	['you', 'me'],
	['you', 'she'],
	['you', 'asked'],
	['you', 'if'],
	['you', 'I'],
	['you', 'would'],
	['you', 'come'],
	['you', 'down'],
	['you', 'for'],
	['you', 'the'],
	['you', 'harvest'],
	['you', 'and'],
	['you', 'I']
];

export const FIXTURE_DURATION = 24;
export const FIXTURE_RATE = 8000;

// Cold open and cuts as word offsets, the same addressing the model pass
// stores. The open runs on delivery rather than length here. The fixture
// is fixed, so these ranges never dangle.
export const FIXTURE_COLD_OPEN = { start: 6, end: 21 };
export const FIXTURE_COLD_REASON =
	'Chosen on delivery: the pause before it, and how the voice settles at the end.';

export const FIXTURE_CUT_DEFS = [
	{ id: 'cut-1', start: 0, end: 2, reason: 'False start at the top of the answer.' },
	{ id: 'cut-2', start: 37, end: 46, reason: 'Bus timetable tangent that goes nowhere.' },
	{ id: 'cut-3', start: 58, end: 59, reason: 'Trailing fragment after the harvest line.' }
] as const;

export const FIXTURE_TITLE = 'The only place nobody needs anything';
export const FIXTURE_NOTES =
	'Rain on the shop roof, hiding above it, and an invitation to the harvest that still hangs open.';
export const FIXTURE_CALLBACK = 'Will you go down for the harvest?';

function buildWords(): EditWord[] {
	return TEXT.map(([speaker, text], index) => {
		const start = 0.4 + index * 0.36;
		return { start, end: start + 0.3, text, speaker };
	});
}

// One channel of alternating tones with pauses, so the waveform has shape
// and the peaks call has something to bucket. Deterministic by seed.
function buildChannel(duration: number, rate: number): Float32Array {
	const frames = Math.floor(duration * rate);
	const out = new Float32Array(frames);
	let seed = 0x2f6e2b1;
	const next = () => {
		seed = (seed * 1103515245 + 12345) & 0x7fffffff;
		return seed / 0x7fffffff - 0.5;
	};
	for (let frame = 0; frame < frames; frame += 1) {
		const second = frame / rate;
		const phrase = Math.floor(second / 3) % 2 === 0;
		const tone = Math.sin(2 * Math.PI * 220 * second) * 0.4 + Math.sin(2 * Math.PI * 330 * second) * 0.2;
		const gate = phrase ? 1 : 0.05;
		out[frame] = tone * gate + next() * 0.02;
	}
	return out;
}

// A mono 16-bit WAV around one channel. Returned as bytes, so logic
// checks can read them with no DOM. The page wraps them in a blob URL.
export function encodeWavBytes(channel: Float32Array, rate: number): Uint8Array {
	const frames = channel.length;
	const buffer = new ArrayBuffer(44 + frames * 2);
	const view = new DataView(buffer);
	const writeText = (offset: number, text: string) => {
		for (let i = 0; i < text.length; i += 1) view.setUint8(offset + i, text.charCodeAt(i));
	};
	writeText(0, 'RIFF');
	view.setUint32(4, 36 + frames * 2, true);
	writeText(8, 'WAVE');
	writeText(12, 'fmt ');
	view.setUint32(16, 16, true);
	view.setUint16(20, 1, true);
	view.setUint16(22, 1, true);
	view.setUint32(24, rate, true);
	view.setUint32(28, rate * 2, true);
	view.setUint16(32, 2, true);
	view.setUint16(34, 16, true);
	writeText(36, 'data');
	view.setUint32(40, frames * 2, true);
	for (let frame = 0; frame < frames; frame += 1) {
		const sample = Math.max(-1, Math.min(1, channel[frame] ?? 0));
		view.setInt16(44 + frame * 2, Math.round(sample * 32767), true);
	}
	return new Uint8Array(buffer);
}

// A blob URL for the WAV bytes, or empty where the runtime has no Blob
// URL support. Empty keeps logic checks DOM-free, and the player simply
// stays unloaded while seeking still moves the playhead state.
function encodeWavUrl(channel: Float32Array, rate: number): string {
	try {
		const bytes = encodeWavBytes(channel, rate);
		if (typeof Blob === 'undefined' || typeof URL.createObjectURL !== 'function') return '';
		return URL.createObjectURL(new Blob([bytes.buffer as ArrayBuffer], { type: 'audio/wav' }));
	} catch {
		return '';
	}
}

let cached: EditorFixture | null = null;

// Load the scripted draft. Cached per session, since the tone render and
// the object URL cost nothing to reuse and rebuilding them would orphan a
// URL the player still holds.
export function loadFixture(episodeId: string): EditorFixture {
	if (cached && cached.episodeId === episodeId) return cached;
	const words = buildWords();
	const channel = buildChannel(FIXTURE_DURATION, FIXTURE_RATE);
	const cuts: EditCut[] = FIXTURE_CUT_DEFS.map((def) => ({
		id: def.id,
		range: { start: def.start, end: def.end },
		reason: def.reason
	}));
	const proposals: FixtureProposal[] = [
		{
			id: 'prop-cold-open',
			kind: 'cold_open',
			start: FIXTURE_COLD_OPEN.start,
			end: FIXTURE_COLD_OPEN.end,
			reason: FIXTURE_COLD_REASON
		},
		...FIXTURE_CUT_DEFS.map((def) => ({
			id: `prop-${def.id}`,
			kind: 'cut' as const,
			start: def.start,
			end: def.end,
			reason: def.reason
		})),
		{ id: 'prop-title', kind: 'title' as const, start: 0, end: 0, reason: FIXTURE_TITLE },
		{ id: 'prop-notes', kind: 'show_notes' as const, start: 0, end: 0, reason: FIXTURE_NOTES },
		{
			id: 'prop-callback',
			kind: 'callback' as const,
			start: 48,
			end: 57,
			reason: `${FIXTURE_CALLBACK} — she asked if I would come down for the harvest`
		}
	];
	cached = {
		episodeId,
		title: FIXTURE_TITLE,
		duration: FIXTURE_DURATION,
		sampleRate: FIXTURE_RATE,
		words,
		cuts,
		proposals,
		audioUrl: encodeWavUrl(channel, FIXTURE_RATE),
		channels: [channel]
	};
	return cached;
}

// Quote the words a proposal spans, for the cold open card and the cuts
// list. Joined here, never invented elsewhere.
export function quoteRange(words: ReadonlyArray<EditWord>, start: number, end: number): string {
	return words
		.slice(start, end + 1)
		.map((word) => word.text)
		.join(' ');
}
