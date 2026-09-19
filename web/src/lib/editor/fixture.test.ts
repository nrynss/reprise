// The scripted draft holds together: every cut and the cold open point
// at real words, timings climb, and the tone matches the clock.
import { describe, expect, it } from 'vitest';
import { normalizeRange } from '@nrynss/chaaya/transcript';
import {
	FIXTURE_COLD_OPEN,
	FIXTURE_CUT_DEFS,
	FIXTURE_DURATION,
	loadFixture,
	quoteRange
} from './fixture';

describe('editor fixture', () => {
	it('points every cut and the cold open at real words', () => {
		const fixture = loadFixture('draft-1');
		const count = fixture.words.length;
		expect(count).toBeGreaterThan(0);
		for (const def of FIXTURE_CUT_DEFS) {
			expect(normalizeRange(count, { start: def.start, end: def.end })).not.toBeNull();
		}
		expect(
			normalizeRange(count, { start: FIXTURE_COLD_OPEN.start, end: FIXTURE_COLD_OPEN.end })
		).not.toBeNull();
	});

	it('times words in order inside the audio', () => {
		const fixture = loadFixture('draft-1');
		let edge = 0;
		for (const word of fixture.words) {
			expect(word.start).toBeGreaterThanOrEqual(edge);
			expect(word.end).toBeGreaterThan(word.start);
			edge = word.end;
		}
		expect(edge).toBeLessThanOrEqual(FIXTURE_DURATION);
	});

	it('quotes the spans it names', () => {
		const fixture = loadFixture('draft-1');
		expect(quoteRange(fixture.words, 0, 2)).toBe('I I mean');
		expect(quoteRange(fixture.words, 48, 48)).toBe('she');
	});

	it('ships one channel the full duration', () => {
		const fixture = loadFixture('draft-1');
		expect(fixture.channels).toHaveLength(1);
		expect(fixture.channels[0]?.length).toBe(FIXTURE_DURATION * fixture.sampleRate);
	});
});
