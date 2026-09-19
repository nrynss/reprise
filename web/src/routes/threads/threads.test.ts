// Pins for the season fixtures and helpers. Quotes must name real
// turns, the season must run newest first, and the job mark must
// never move backwards.
import { describe, expect, it } from 'vitest';
import { activeWordAt } from '@nrynss/chaaya/transcript';
import {
	browserStore,
	buildTone,
	buildWords,
	encodeWavBytes,
	episodeById,
	episodeNumber,
	formatClock,
	formatEpisodeNumber,
	installMockJob,
	listSeason,
	listThreads,
	progressFrame,
	progressPercent,
	queryValue,
	quoteHref,
	quoteInTranscript,
	readHighWater,
	transcriptText,
	writeHighWater,
	type WaterStore
} from './threads';

function memoryStore(): WaterStore {
	const held: Record<string, string> = {};
	return {
		read: (key) => (key in held ? (held[key] as string) : null),
		write: (key, value) => {
			held[key] = value;
		}
	};
}

describe('season order', () => {
	it('runs newest first with four ready episodes at base', () => {
		const season = listSeason('none');
		expect(season.map((episode) => episode.number)).toEqual([4, 3, 2, 1]);
		for (const episode of season) expect(episode.state).toBe('ready');
	});

	it('carries the fifth episode in its lab state', () => {
		expect(listSeason('rendering')[0]).toMatchObject({ number: 5, state: 'rendering' });
		expect(listSeason('draft')[0]).toMatchObject({ number: 5, state: 'draft' });
		expect(listSeason('ready')[0]).toMatchObject({ number: 5, state: 'ready' });
		expect(listSeason('none')).toHaveLength(4);
	});

	it('looks episodes up by id and misses unknown ids', () => {
		expect(episodeById('ep-4')?.title).toBe('Three weeks of almost');
		expect(episodeById('nope')).toBeUndefined();
	});
});

describe('quotes', () => {
	it('every thread quote sits in its episode transcript', () => {
		const threads = listThreads(true);
		const items = [...threads.commitments, ...threads.people, ...threads.topics];
		expect(items.length).toBeGreaterThan(0);
		for (const item of items) {
			expect(item.quotes.length).toBeGreaterThan(0);
			for (const quote of item.quotes) {
				expect(quoteInTranscript(quote.episode, quote.text)).toBe(true);
			}
		}
	});

	it('every quote offset opens the turn that carries it', () => {
		const threads = listThreads(false);
		const items = [...threads.commitments, ...threads.people, ...threads.topics];
		for (const item of items) {
			for (const quote of item.quotes) {
				const episode = episodeById(quote.episode);
				expect(episode).toBeDefined();
				const turn = episode?.turns.find((candidate) => candidate.start === quote.offset);
				expect(turn).toBeDefined();
				expect(turn?.text.includes(quote.text)).toBe(true);
			}
		}
	});

	it('quote offsets land on words sounding at that second', () => {
		const episode = episodeById('ep-4');
		if (!episode) throw new Error('missing ep-4');
		const words = buildWords(episode.turns);
		const index = activeWordAt(words, [], 14);
		expect(index).not.toBeNull();
		expect(words[index ?? 0]?.text).toBe('Nothing.');
	});

	it('episode five joins the threads once ready', () => {
		const before = listThreads(false);
		const after = listThreads(true);
		expect(after.commitments).toHaveLength(before.commitments.length + 1);
		expect(after.commitments[0]?.id).toBe('go-harvest');
		const june = after.people.find((item) => item.id === 'june');
		expect(june?.count).toBe('10 mentions · 5 episodes');
		expect(transcriptText(episodeById('ep-5')?.turns ?? []).length).toBeGreaterThan(0);
	});
});

describe('clocks and queries', () => {
	it('renders m:ss with zero padding', () => {
		expect(formatClock(0)).toBe('0:00');
		expect(formatClock(14)).toBe('0:14');
		expect(formatClock(75)).toBe('1:15');
		expect(formatClock(-3)).toBe('0:00');
		expect(formatEpisodeNumber(4)).toBe('EP.04');
	});

	it('reads the first matching query value', () => {
		expect(queryValue('?fixture=1&t=14', 't')).toBe('14');
		expect(queryValue('?fixture=1', 't')).toBeNull();
	});

	it('links quotes to the episode at their offset', () => {
		expect(quoteHref('ep-2', 20)).toBe('/episode/ep-2?fixture=1&t=20&play=1');
		expect(episodeNumber('ep-4')).toBe(4);
		expect(episodeNumber('nope')).toBe(0);
	});
});

describe('progress water', () => {
	it('renders whole percent clamped to the total', () => {
		expect(progressPercent(1, 4)).toBe(25);
		expect(progressPercent(3, 4)).toBe(75);
		expect(progressPercent(9, 4)).toBe(100);
		expect(progressPercent(0, 0)).toBe(0);
	});

	it('never moves backwards across writes and restarts', () => {
		const store = memoryStore();
		expect(readHighWater(store, 'job-1')).toBe(0);
		expect(writeHighWater(store, 'job-1', 25)).toBe(25);
		expect(writeHighWater(store, 'job-1', 10)).toBe(25);
		expect(writeHighWater(store, 'job-1', 50)).toBe(50);
		const fresh = memoryStore();
		expect(readHighWater(fresh, 'job-1')).toBe(0);
		expect(readHighWater(store, 'job-1')).toBe(50);
	});

	it('answers zero when the store refuses', () => {
		const refusing: WaterStore = {
			read: () => {
				throw new Error('denied');
			},
			write: () => {
				throw new Error('denied');
			}
		};
		expect(readHighWater(refusing, 'job-1')).toBe(0);
		expect(writeHighWater(refusing, 'job-1', 40)).toBe(40);
	});

	it('the browser store holds the mark where a window lives', () => {
		const store = browserStore();
		if (!store) {
			expect(store).toBeNull();
			return;
		}
		expect(writeHighWater(store, 'job-dom', 30)).toBe(30);
		expect(readHighWater(store, 'job-dom')).toBe(30);
	});
});

describe('fixture audio', () => {
	it('builds a deterministic tone at the fixture rate', () => {
		const first = buildTone(2, 4);
		const second = buildTone(2, 4);
		expect(first.length).toBe(16000);
		expect(first).toEqual(second);
		expect(buildTone(2, 5)).not.toEqual(first);
	});

	it('wraps the tone in a WAV of exact size', () => {
		const bytes = encodeWavBytes(buildTone(1, 1), 8000);
		expect(bytes.length).toBe(44 + 8000 * 2);
		expect(String.fromCharCode(bytes[0] ?? 0, bytes[1] ?? 0, bytes[2] ?? 0, bytes[3] ?? 0)).toBe(
			'RIFF'
		);
	});

	it('frames scripted progress in SSE wire shape', () => {
		expect(progressFrame('job-1', 'rendering', 2, 4, 2)).toContain('event: progress');
		expect(progressFrame('job-1', 'rendering', 2, 4, 2)).toContain('"current":2');
		expect(typeof installMockJob).toBe('function');
	});
});
