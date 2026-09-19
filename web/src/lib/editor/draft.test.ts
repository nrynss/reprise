// The draft controller keeps its promises: a revert drops the cut and
// writes the decision row, the waveform regions follow the cuts, and the
// cold open preview parks the playhead where the open starts.
import { beforeAll, describe, expect, it, vi } from 'vitest';
import { DraftController, formatTime, queryValue, type DraftSnapshot } from './draft';

beforeAll(() => {
	if (typeof URL.createObjectURL !== 'function') {
		Object.defineProperty(URL, 'createObjectURL', { value: () => 'blob:fixture', writable: true });
	}
});

function openDraft(): { controller: DraftController; snaps: DraftSnapshot[] } {
	const snaps: DraftSnapshot[] = [];
	const controller = new DraftController({ episodeId: 'draft-1', onChange: (snap) => snaps.push(snap) });
	controller.mount('?fixture=1');
	return { controller, snaps };
}

describe('draft controller', () => {
	it('loads three applied cuts with reasons', () => {
		const { controller } = openDraft();
		const snap = controller.snapshot;
		expect(snap.ready).toBe(true);
		expect(snap.appliedCount).toBe(3);
		expect(snap.cutCards.map((card) => card.reason)).toEqual([
			'False start at the top of the answer.',
			'Bus timetable tangent that goes nowhere.',
			'Trailing fragment after the harvest line.'
		]);
		expect(snap.regions).toHaveLength(3);
	});

	it('reverts one cut and writes its decision row', () => {
		const { controller } = openDraft();
		controller.revertCut('cut-1');
		const snap = controller.snapshot;
		expect(snap.appliedCount).toBe(2);
		expect(snap.cutCards.map((card) => card.id)).not.toContain('cut-1');
		expect(snap.decisions).toHaveLength(1);
		expect(snap.decisions[0]).toMatchObject({
			id: 'dec-cut-1',
			proposalId: 'prop-cut-1',
			cutId: 'cut-1',
			decision: 'reverted'
		});
		expect(snap.regions).toHaveLength(2);
		expect(controller.removedSpans()).toHaveLength(2);
	});

	it('reverts the cold open and writes its decision row', () => {
		const { controller } = openDraft();
		const quote = controller.snapshot.coldOpen.quote;
		controller.revertColdOpen();
		const snap = controller.snapshot;
		expect(snap.coldOpenReverted).toBe(true);
		expect(snap.coldOpen.quote).toBe(quote);
		expect(snap.decisions).toHaveLength(1);
		expect(snap.decisions[0]).toMatchObject({
			id: 'dec-cold-open',
			proposalId: 'prop-cold-open',
			decision: 'reverted'
		});
		controller.revertColdOpen();
		expect(controller.snapshot.decisions).toHaveLength(1);
	});

	it('reverts the title to the plain fallback', () => {
		const { controller } = openDraft();
		const proposed = controller.snapshot.proposedTitle;
		expect(proposed).not.toBe('');
		controller.revertTitle();
		const snap = controller.snapshot;
		expect(snap.titleReverted).toBe(true);
		expect(snap.title).toBe('Episode draft-1');
		expect(snap.decisions).toHaveLength(1);
		expect(snap.decisions[0]).toMatchObject({
			id: 'dec-title',
			proposalId: 'prop-title',
			decision: 'reverted',
			reason: proposed
		});
		controller.revertTitle();
		expect(controller.snapshot.decisions).toHaveLength(1);
	});

	it('reverts the show notes to empty', () => {
		const { controller } = openDraft();
		expect(controller.snapshot.notes).not.toBe('');
		controller.revertNotes();
		const snap = controller.snapshot;
		expect(snap.notesReverted).toBe(true);
		expect(snap.notes).toBe('');
		expect(snap.decisions).toHaveLength(1);
		expect(snap.decisions[0]).toMatchObject({
			id: 'dec-notes',
			proposalId: 'prop-notes',
			decision: 'reverted'
		});
		controller.revertNotes();
		expect(controller.snapshot.decisions).toHaveLength(1);
	});

	it('reverts the callback and clears its planted row', () => {
		const { controller } = openDraft();
		expect(controller.snapshot.callback).not.toBe('');
		controller.revertCallback();
		const snap = controller.snapshot;
		expect(snap.callbackReverted).toBe(true);
		expect(snap.callback).toBe('');
		expect(snap.callbackQuote).toBe('');
		expect(snap.callbacksCleared).toBe(true);
		expect(snap.decisions).toHaveLength(1);
		expect(snap.decisions[0]).toMatchObject({
			id: 'dec-callback',
			proposalId: 'prop-callback',
			decision: 'reverted'
		});
		controller.revertCallback();
		expect(controller.snapshot.decisions).toHaveLength(1);
	});

	it('leaves every cut alone on an unknown revert', () => {
		const { controller } = openDraft();
		controller.revertCut('cut-9');
		expect(controller.snapshot.appliedCount).toBe(3);
		expect(controller.snapshot.decisions).toHaveLength(0);
	});

	it('seeks to the word a click names', () => {
		const { controller } = openDraft();
		controller.seekToWord(10);
		expect(controller.snapshot.position).toBeCloseTo(controller.snapshot.words[10]?.start ?? -1);
	});

	it('parks the playhead at the cold open on preview', async () => {
		const { controller } = openDraft();
		const play = vi.spyOn(controller.player, 'play').mockResolvedValue(true);
		await controller.previewColdOpen();
		const snap = controller.snapshot;
		expect(snap.position).toBeCloseTo(snap.words[snap.coldOpen.start]?.start ?? -1);
		expect(play).toHaveBeenCalledTimes(1);
	});

	it('confirms before starting the render', async () => {
		const { controller } = openDraft();
		controller.markDone();
		expect(controller.snapshot.renderStage).toBe('confirm');
		controller.markDone();
		await vi.waitFor(() => {
			expect(controller.snapshot.renderStage).toBe('running');
		});
		expect(controller.snapshot.renderDetail).toContain('Render running');
	});

	it('formats the readout as minutes and seconds', () => {
		expect(formatTime(0)).toBe('0:00');
		expect(formatTime(65)).toBe('1:05');
		expect(formatTime(143)).toBe('2:23');
	});

	it('reads one query value by name', () => {
		expect(queryValue('?fixture=1&gate=0', 'fixture')).toBe('1');
		expect(queryValue('?fixture=1', 'gate')).toBeNull();
		expect(queryValue('', 'fixture')).toBeNull();
	});

	it('falls back to the scripted draft when the backend refuses', async () => {
		vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('no backend')));
		const snaps: DraftSnapshot[] = [];
		const controller = new DraftController({ episodeId: 'draft-1', onChange: (snap) => snaps.push(snap) });
		controller.mount('?fixture=0');
		await vi.waitFor(() => {
			expect(controller.snapshot.ready).toBe(true);
		});
		expect(controller.snapshot.appliedCount).toBe(3);
		expect(controller.snapshot.notice).toContain('scripted draft');
		vi.unstubAllGlobals();
	});
});
