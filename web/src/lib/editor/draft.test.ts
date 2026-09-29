// The draft controller keeps its promises: a revert drops the cut and
// writes the decision row, the waveform regions follow the cuts, and the
// cold open preview parks the playhead where the open starts.
import { beforeAll, describe, expect, it, vi } from 'vitest';
import {
	DraftController,
	formatTime,
	LIVE_DRAFT_EMPTY_NOTICE,
	LIVE_DRAFT_NOTICE,
	LIVE_RENDER_RUNNING_DETAIL,
	queryValue,
	type DraftSnapshot
} from './draft';
import { encodeWavBytes } from './fixture';

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

	it('follows a started render instead of calling it refused', async () => {
		const detail = {
			episode: { id: 'live-9', number: 9, title: 'Live take', state: 'draft', visibility: 'private' },
			proposals: [],
			words: [{ text: 'Hello', start: 0, end: 0.4 }],
			audio_url: '',
			render_audio_url: ''
		};
		const fetchMock = vi.fn(async (input: unknown) => {
			const url = typeof input === 'string' ? input : String((input as Request)?.url ?? input);
			if (url.includes('/done')) {
				return Response.json({ job_id: 'r-1', queued: false }, { status: 202 });
			}
			if (url.includes('/api/jobs/r-1/events')) {
				return new Response(
					'id: 1\nevent: progress\ndata: {"job_id":"r-1","stage":"rendering","current":1,"total":4}\n\n',
					{ status: 200, headers: { 'content-type': 'text/event-stream' } }
				);
			}
			if (url.includes('/api/jobs/r-1')) {
				return Response.json({ jobId: 'r-1', status: 'running', current: 1, total: 4 });
			}
			return Response.json(detail);
		});
		vi.stubGlobal('fetch', fetchMock);
		try {
			const controller = new DraftController({ episodeId: 'live-9', onChange: () => {} });
			controller.mount('');
			await vi.waitFor(() => {
				expect(controller.snapshot.ready).toBe(true);
			});
			controller.markDone();
			expect(controller.snapshot.renderStage).toBe('confirm');
			controller.markDone();
			await vi.waitFor(() => {
				expect(controller.snapshot.renderStage).toBe('running');
			});
			expect(controller.snapshot.renderDetail).toContain(LIVE_RENDER_RUNNING_DETAIL);
			expect(controller.snapshot.renderDetail).not.toContain('refused');
			expect(controller.snapshot.renderDetail).not.toContain('could not be followed');
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
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

	it('leaves a refused live load unready with the failure named', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('nope', { status: 500 })));
		try {
			const controller = new DraftController({ episodeId: 'live-1', onChange: () => {} });
			controller.mount('');
			await vi.waitFor(() => {
				expect(controller.snapshot.notice).toContain('This episode did not load');
			});
			const snap = controller.snapshot;
			expect(snap.ready).toBe(false);
			expect(snap.words).toEqual([]);
			expect(snap.title).toBe('');
			expect(snap.appliedCount).toBe(0);
			expect(snap.notice).toContain('500');
			expect(snap.loadError).not.toBeNull();
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it('retries the live load on demand after a refusal', async () => {
		const fetchMock = vi
			.fn()
			.mockResolvedValueOnce(new Response('nope', { status: 500 }))
			.mockResolvedValueOnce(
				Response.json({
					episode: {
						id: 'live-1',
						number: 2,
						title: 'Live take',
						state: 'draft',
						visibility: 'private'
					},
					proposals: [],
					words: [{ text: 'Hello', start: 0, end: 0.4 }],
					audio_url: '',
					render_audio_url: ''
				})
			);
		vi.stubGlobal('fetch', fetchMock);
		try {
			const controller = new DraftController({ episodeId: 'live-1', onChange: () => {} });
			controller.mount('');
			await vi.waitFor(() => {
				expect(controller.snapshot.notice).toContain('This episode did not load');
			});
			expect(controller.snapshot.ready).toBe(false);
			controller.retry();
			await vi.waitFor(() => {
				expect(controller.snapshot.ready).toBe(true);
			});
			expect(controller.snapshot.title).toBe('Live take');
			expect(controller.snapshot.words.map((word) => word.text)).toEqual(['Hello']);
			expect(fetchMock).toHaveBeenCalledTimes(2);
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it('waits out a rate limit once and then loads the live draft', async () => {
		vi.stubGlobal(
			'fetch',
			vi
				.fn()
				.mockResolvedValueOnce(
					new Response('slow down', { status: 429, headers: { 'Retry-After': '1' } })
				)
				.mockResolvedValueOnce(
					Response.json({
						episode: {
							id: 'live-1',
							number: 2,
							title: 'Live take',
							state: 'draft',
							visibility: 'private'
						},
						proposals: [],
						words: [{ text: 'Hello', start: 0, end: 0.4 }],
						audio_url: '',
						render_audio_url: ''
					})
				)
		);
		try {
			const controller = new DraftController({ episodeId: 'live-1', onChange: () => {} });
			controller.mount('');
			await vi.waitFor(
				() => {
					expect(controller.snapshot.ready).toBe(true);
				},
				{ timeout: 10_000 }
			);
			expect(controller.snapshot.title).toBe('Live take');
			expect(controller.snapshot.words.map((word) => word.text)).toEqual(['Hello']);
			expect(controller.snapshot.notice).not.toContain('did not load');
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it('loads stored words and the stem address instead of the scripted sample', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn().mockResolvedValue(
				Response.json({
					episode: {
						id: 'live-1',
						number: 2,
						title: 'Live take',
						state: 'draft',
						visibility: 'private'
					},
					proposals: [
						{
							id: 'cut-a',
							kind: 'cut',
							start_word: 0,
							end_word: 1,
							reason: 'Trim the open.',
							decision: 'accepted'
						}
					],
					words: [
						{ text: 'Hello', start: 0, end: 0.4 },
						{ text: 'there', start: 0.4, end: 0.9 }
					],
					audio_url: '/media/user-stem',
					render_audio_url: '/media/opus-ignored'
				})
			)
		);
		try {
			const controller = new DraftController({ episodeId: 'live-1', onChange: () => {} });
			controller.mount('');
			await vi.waitFor(() => {
				expect(controller.snapshot.ready).toBe(true);
			});
			expect(controller.snapshot.title).toBe('Live take');
			expect(controller.snapshot.words.map((word) => word.text)).toEqual(['Hello', 'there']);
			expect(controller.snapshot.appliedCount).toBe(1);
			expect(controller.snapshot.cutCards[0]?.reason).toBe('Trim the open.');
			expect(controller.snapshot.notice).not.toContain('scripted');
			expect(controller.player.source).toBe('/media/user-stem');
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it('keeps each stored speaker and never labels a host word as the guest', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn().mockResolvedValue(
				Response.json({
					episode: {
						id: 'live-1',
						number: 2,
						title: 'Live take',
						state: 'draft',
						visibility: 'private'
					},
					proposals: [],
					words: [
						{ text: 'Hello', start: 0, end: 0.4, speaker: 'host' },
						{ text: 'there', start: 0.4, end: 0.9, speaker: 'user' },
						{ text: 'again', start: 0.9, end: 1.2 }
					],
					audio_url: '/media/user-stem',
					render_audio_url: ''
				})
			)
		);
		try {
			const controller = new DraftController({ episodeId: 'live-1', onChange: () => {} });
			controller.mount('');
			await vi.waitFor(() => {
				expect(controller.snapshot.ready).toBe(true);
			});
			expect(controller.snapshot.words.map((word) => word.speaker)).toEqual(['host', 'you', '']);
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it('stays an empty live draft when the detail has no words', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn().mockResolvedValue(
				Response.json({
					episode: {
						id: 'live-1',
						number: 1,
						title: 'Quiet take',
						state: 'draft',
						visibility: 'private'
					},
					proposals: [],
					words: [],
					audio_url: '',
					render_audio_url: ''
				})
			)
		);
		try {
			const controller = new DraftController({ episodeId: 'live-1', onChange: () => {} });
			controller.mount('');
			await vi.waitFor(() => {
				expect(controller.snapshot.ready).toBe(true);
			});
			expect(controller.snapshot.title).toBe('Quiet take');
			expect(controller.snapshot.words).toEqual([]);
			expect(controller.snapshot.appliedCount).toBe(0);
			expect(controller.snapshot.notice).not.toContain('scripted');
			expect(controller.player.source).toBeNull();
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it('carries a proposed cold open on the scripted draft', () => {
		const { controller } = openDraft();
		const snap = controller.snapshot;
		expect(snap.hasColdOpen).toBe(true);
		expect(snap.coldOpen.quote).not.toBe('');
		expect(snap.coldOpen.reason).not.toBe('');
	});

	it('names no cold open and no callback when the detail stores neither', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn().mockResolvedValue(
				Response.json({
					episode: {
						id: 'live-1',
						number: 2,
						title: 'Live take',
						state: 'draft',
						visibility: 'private'
					},
					proposals: [
						{
							id: 'title-9',
							kind: 'title',
							start_word: 0,
							end_word: 0,
							reason: 'Live take',
							decision: ''
						}
					],
					words: [
						{ text: 'Hello', start: 0, end: 0.4 },
						{ text: 'there', start: 0.4, end: 0.9 }
					],
					audio_url: '/media/user-stem',
					render_audio_url: ''
				})
			)
		);
		try {
			const controller = new DraftController({ episodeId: 'live-1', onChange: () => {} });
			controller.mount('');
			await vi.waitFor(() => {
				expect(controller.snapshot.ready).toBe(true);
			});
			const snap = controller.snapshot;
			expect(snap.hasColdOpen).toBe(false);
			expect(snap.coldOpenReverted).toBe(false);
			expect(snap.callback).toBe('');
			expect(snap.callbackQuote).toBe('');
			expect(snap.proposedCallback).toBe('');
			expect(snap.callbackReverted).toBe(false);
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it('names the stored cold open when the detail stores one', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn().mockResolvedValue(
				Response.json({
					episode: {
						id: 'live-1',
						number: 2,
						title: 'Live take',
						state: 'draft',
						visibility: 'private'
					},
					proposals: [
						{
							id: 'cold-9',
							kind: 'cold_open',
							start_word: 0,
							end_word: 1,
							reason: 'Strong line.',
							decision: ''
						}
					],
					words: [
						{ text: 'Hello', start: 0, end: 0.4 },
						{ text: 'there', start: 0.4, end: 0.9 }
					],
					audio_url: '/media/user-stem',
					render_audio_url: ''
				})
			)
		);
		try {
			const controller = new DraftController({ episodeId: 'live-1', onChange: () => {} });
			controller.mount('');
			await vi.waitFor(() => {
				expect(controller.snapshot.ready).toBe(true);
			});
			const snap = controller.snapshot;
			expect(snap.hasColdOpen).toBe(true);
			expect(snap.coldOpen.quote).toBe('Hello there');
			expect(snap.coldOpen.reason).toBe('Strong line.');
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it('plays the preview address first and falls back to the stem address', async () => {
		async function sourceWithPreview(preview: string): Promise<string | null> {
			vi.stubGlobal(
				'fetch',
				vi.fn().mockResolvedValue(
					Response.json({
						episode: {
							id: 'live-1',
							number: 2,
							title: 'Live take',
							state: 'draft',
							visibility: 'private'
						},
						proposals: [],
						words: [
							{ text: 'Hello', start: 0, end: 0.4 },
							{ text: 'there', start: 0.4, end: 0.9 }
						],
						audio_url: '/media/user-stem',
						preview_audio_url: preview,
						render_audio_url: ''
					})
				)
			);
			try {
				const controller = new DraftController({ episodeId: 'live-1', onChange: () => {} });
				controller.mount('');
				await vi.waitFor(() => {
					expect(controller.snapshot.ready).toBe(true);
				});
				const source = controller.player.source;
				controller.destroy();
				return source;
			} finally {
				vi.unstubAllGlobals();
			}
		}
		expect(await sourceWithPreview('/media/draft-preview')).toBe('/media/draft-preview');
		expect(await sourceWithPreview('')).toBe('/media/user-stem');
	});

	it('adopts the element length once it reports one', () => {
		const { controller } = openDraft();
		expect(controller.snapshot.duration).toBe(24);
		controller.player.duration = 30;
		controller.syncDuration();
		expect(controller.snapshot.duration).toBe(30);
		expect(controller.snapshot.regions).toHaveLength(3);
		controller.player.duration = 30;
		controller.syncDuration();
		expect(controller.snapshot.duration).toBe(30);
	});

	it('loads live peaks from the audio the draft plays', async () => {
		const channel = new Float32Array(8000);
		for (let frame = 0; frame < channel.length; frame += 1) {
			channel[frame] = Math.sin((2 * Math.PI * 220 * frame) / 8000) * 0.5;
		}
		const bytes = encodeWavBytes(channel, 8000);
		vi.stubGlobal('fetch', async (input: unknown) => {
			const url = typeof input === 'string' ? input : String((input as Request)?.url ?? input);
			if (url === '/media/take') return new Response(bytes.buffer as ArrayBuffer, { status: 200 });
			return Response.json({
				episode: {
					id: 'live-2',
					number: 2,
					title: 'Live take',
					state: 'draft',
					visibility: 'private'
				},
				proposals: [],
				words: [{ text: 'Hello', start: 0, end: 0.4 }],
				audio_url: '/media/take',
				render_audio_url: ''
			});
		});
		vi.stubGlobal(
			'OfflineAudioContext',
			class {
				async decodeAudioData(raw: ArrayBuffer): Promise<{
					numberOfChannels: number;
					getChannelData(index: number): Float32Array;
				}> {
					const view = new DataView(raw);
					const count = view.getUint32(40, true) / 2;
					const out = new Float32Array(count);
					for (let frame = 0; frame < count; frame += 1) {
						out[frame] = view.getInt16(44 + frame * 2, true) / 32768;
					}
					return { numberOfChannels: 1, getChannelData: () => out };
				}
			}
		);
		try {
			const controller = new DraftController({ episodeId: 'live-2', onChange: () => {} });
			controller.mount('');
			await vi.waitFor(() => {
				expect(controller.snapshot.ready).toBe(true);
			});
			await vi.waitFor(() => {
				expect(controller.snapshot.peaks).not.toBeNull();
			});
			const peaks = controller.snapshot.peaks;
			expect(peaks?.max.length).toBeGreaterThan(0);
			expect(Math.max(...Array.from(peaks?.max ?? []))).toBeGreaterThan(0.1);
			controller.destroy();
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it('keeps the flat line and still plays when the live decode fails', async () => {
		vi.stubGlobal('fetch', async (input: unknown) => {
			const url = typeof input === 'string' ? input : String((input as Request)?.url ?? input);
			if (url === '/media/take')
				return new Response(new Uint8Array([1, 2, 3]).buffer as ArrayBuffer, { status: 200 });
			return Response.json({
				episode: {
					id: 'live-3',
					number: 3,
					title: 'Live take',
					state: 'draft',
					visibility: 'private'
				},
				proposals: [],
				words: [{ text: 'Hello', start: 0, end: 0.4 }],
				audio_url: '/media/take',
				render_audio_url: ''
			});
		});
		vi.stubGlobal(
			'OfflineAudioContext',
			class {
				async decodeAudioData(): Promise<never> {
					throw new Error('unreadable');
				}
			}
		);
		const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
		try {
			const controller = new DraftController({ episodeId: 'live-3', onChange: () => {} });
			controller.mount('');
			await vi.waitFor(() => {
				expect(controller.snapshot.ready).toBe(true);
			});
			const play = vi.spyOn(controller.player, 'play').mockResolvedValue(true);
			await controller.togglePlay();
			await vi.waitFor(() => {
				expect(warn).toHaveBeenCalledTimes(1);
			});
			expect(controller.snapshot.peaks).toBeNull();
			expect(controller.snapshot.playing).toBe(true);
			expect(play).toHaveBeenCalledTimes(1);
			controller.destroy();
		} finally {
			warn.mockRestore();
			vi.unstubAllGlobals();
		}
	});
	it('shows no notice on a stored draft and a plain line on an empty one', async () => {
		async function noticeFor(words: unknown[]): Promise<string> {
			vi.stubGlobal(
				'fetch',
				vi.fn().mockResolvedValue(
					Response.json({
						episode: {
							id: 'live-1',
							number: 2,
							title: 'Live take',
							state: 'draft',
							visibility: 'private'
						},
						proposals: [],
						words,
						audio_url: '',
						render_audio_url: ''
					})
				)
			);
			try {
				const controller = new DraftController({ episodeId: 'live-1', onChange: () => {} });
				controller.mount('');
				await vi.waitFor(() => {
					expect(controller.snapshot.ready).toBe(true);
				});
				const notice = controller.snapshot.notice;
				controller.destroy();
				return notice;
			} finally {
				vi.unstubAllGlobals();
			}
		}
		expect(LIVE_DRAFT_NOTICE).toBe('');
		expect(LIVE_DRAFT_EMPTY_NOTICE).toBe('No transcript yet.');
		expect(await noticeFor([{ text: 'Hello', start: 0, end: 0.4 }])).toBe(LIVE_DRAFT_NOTICE);
		expect(await noticeFor([])).toBe(LIVE_DRAFT_EMPTY_NOTICE);
	});
});

describe('draft first press', () => {
	it('plays after one press when the retry answers', async () => {
		const { controller } = openDraft();
		const play = vi
			.spyOn(controller.player, 'play')
			.mockResolvedValueOnce(false)
			.mockResolvedValueOnce(true);
		try {
			await controller.togglePlay();
			expect(play).toHaveBeenCalledTimes(2);
			expect(controller.snapshot.playing).toBe(true);
			expect(controller.snapshot.notice).not.toContain('refused');
		} finally {
			controller.destroy();
		}
	});

	it('shows the refusal only after the retry also fails', async () => {
		const { controller } = openDraft();
		const play = vi.spyOn(controller.player, 'play').mockResolvedValue(false);
		try {
			await controller.togglePlay();
			expect(play).toHaveBeenCalledTimes(2);
			expect(controller.snapshot.playing).toBe(false);
			expect(controller.snapshot.notice).toContain('refused');
		} finally {
			controller.destroy();
		}
	});

	it('logs the stored refusal name once per refused call', async () => {
		const { controller } = openDraft();
		vi.spyOn(controller.player, 'play').mockResolvedValue(false);
		controller.player.lastPlayError = { name: 'NotAllowedError', message: 'x' };
		const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
		try {
			await controller.togglePlay();
			expect(warn).toHaveBeenCalledTimes(2);
			const joined = warn.mock.calls.map((call) => String(call[0])).join('\n');
			expect(joined.split('NotAllowedError').length - 1).toBe(2);
			expect(joined).toContain('x');
		} finally {
			warn.mockRestore();
			controller.destroy();
		}
	});
});
