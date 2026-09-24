// Review pin. A cold open that spans an accepted cut carries a join of
// its own. The cold open probe measured the episode bursts of this draft
// at 3.22 s, 4.21 s and 6.21 s, each half a second into its word.
import { describe, expect, it } from 'vitest';
import { renderClockWords, type LiveProposal } from './threads';

describe('render clock with a cold open across a cut', () => {
	it('places each episode word where the rendered file plays it', () => {
		const proposals: LiveProposal[] = [
			{ id: 'cut-1', kind: 'cut', startWord: 1, endWord: 1, reason: '', decision: 'accepted' },
			{ id: 'cold-1', kind: 'cold_open', startWord: 0, endWord: 2, reason: '', decision: '' }
		];
		const words = ['w0', 'w1', 'w2', 'w3', 'w4', 'w5'].map((text, index) => ({
			text,
			start: index,
			end: index + 1
		}));
		const placed = renderClockWords({ words, proposals, renderWords: [] });
		const at = (text: string): number => placed.find((word) => word.text === text)?.start ?? -1;
		expect(Math.abs(at('w0') - 2.72)).toBeLessThan(0.001);
		expect(Math.abs(at('w2') - 3.71)).toBeLessThan(0.001);
		expect(Math.abs(at('w4') - 5.71)).toBeLessThan(0.001);
	});
});
