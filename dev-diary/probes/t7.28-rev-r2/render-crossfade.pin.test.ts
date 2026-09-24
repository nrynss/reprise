// Review pin. Two accepted cuts leave two joins, and the render overlaps
// each join by a ten millisecond crossfade. The crossfade probe measured
// a moment 4.5 s into the take at 2.48 s in the rendered file. So the
// word starting at 4 s must sit at 1.98 s on the episode page.
import { describe, expect, it } from 'vitest';
import { renderClockWords, type LiveProposal } from './threads';

function cut(id: string, index: number): LiveProposal {
	return { id, kind: 'cut', startWord: index, endWord: index, reason: '', decision: 'accepted' };
}

describe('render clock crossfade', () => {
	it('places a word after two joins where the rendered file plays it', () => {
		const placed = renderClockWords({
			words: [
				{ text: 'a', start: 0, end: 1 },
				{ text: 'b', start: 1, end: 2 },
				{ text: 'c', start: 2, end: 3 },
				{ text: 'd', start: 3, end: 4 },
				{ text: 'e', start: 4, end: 5 }
			],
			proposals: [cut('cut-b', 1), cut('cut-d', 3)],
			renderWords: []
		});
		const last = placed.find((word) => word.text === 'e');
		expect(Math.abs((last?.start ?? 0) - 1.98)).toBeLessThan(0.001);
	});
});
