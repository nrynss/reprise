// Pins for the episode export bundle control. The route names the
// episode, the download name follows the header, and each refusal
// status reads its own line with no download behind it.
import { describe, expect, it, vi } from 'vitest';
import {
	bundleFilename,
	emptyScreen,
	EpisodeController,
	exportPath,
	exportRefusal
} from './threads';

function liveDetail(): Record<string, unknown> {
	return {
		episode: { id: 'ep-live', number: 2, title: 'Quiet take', state: 'ready', visibility: 'private' },
		proposals: [],
		words: [],
		audio_url: '',
		render_audio_url: ''
	};
}

async function mounted(snaps: Array<ReturnType<typeof emptyScreen>>): Promise<EpisodeController> {
	const controller = new EpisodeController('ep-live', (snap) => snaps.push(snap));
	controller.mount('');
	await vi.waitFor(() => {
		expect(snaps.at(-1)?.ready).toBe(true);
	});
	return controller;
}

describe('export bundle', () => {
	it('names the bundle route behind the episode', () => {
		expect(exportPath('ep-9')).toBe('/api/episodes/ep-9/export');
		expect(exportPath('a b')).toBe('/api/episodes/a%20b/export');
	});

	it('reads the download name from the header with a fallback', () => {
		const named = new Response('x', {
			headers: { 'content-disposition': 'attachment; filename="episode-4-bundle.zip"' }
		});
		expect(bundleFilename(named, 'ep-4')).toBe('episode-4-bundle.zip');
		expect(bundleFilename(new Response('x'), 'ep-4')).toBe('episode-ep-4-bundle.zip');
	});

	it('explains each refusal status', () => {
		expect(exportRefusal(404)).toContain('No episode');
		expect(exportRefusal(409)).toContain('never finished');
		expect(exportRefusal(500)).toContain('Retry');
	});

	it('downloads the bundle behind a live ready episode', async () => {
		const seen: string[] = [];
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
				if (url.endsWith('/export')) {
					return new Response('PK fake zip', {
						status: 200,
						headers: {
							'content-type': 'application/zip',
							'content-disposition': 'attachment; filename="episode-2-bundle.zip"'
						}
					});
				}
				if (url.includes('/api/threads')) {
					return Response.json({ name_threads: [], circled_topics: [] });
				}
				return Response.json(liveDetail());
			})
		);
		const createObjectURL = vi.fn(() => 'blob:fake-bundle');
		const revokeObjectURL = vi.fn();
		Object.defineProperty(URL, 'createObjectURL', { value: createObjectURL, configurable: true });
		Object.defineProperty(URL, 'revokeObjectURL', { value: revokeObjectURL, configurable: true });
		const clicks: string[] = [];
		const clickSpy = vi
			.spyOn(window.HTMLAnchorElement.prototype, 'click')
			.mockImplementation(function (this: HTMLAnchorElement) {
				seen.push(this.href);
				clicks.push(this.download);
			});
		try {
			const snaps: Array<ReturnType<typeof emptyScreen>> = [];
			const controller = await mounted(snaps);
			await controller.exportBundle();
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.exporting).toBe(false);
			});
			expect(snaps.at(-1)?.notice).toContain('downloaded');
			expect(createObjectURL).toHaveBeenCalledTimes(1);
			expect(clicks).toEqual(['episode-2-bundle.zip']);
			expect(seen).toEqual(['blob:fake-bundle']);
			expect(revokeObjectURL).toHaveBeenCalledWith('blob:fake-bundle');
			controller.destroy();
		} finally {
			clickSpy.mockRestore();
			vi.unstubAllGlobals();
		}
	});

	it('reports a refused export with no download', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
				if (url.endsWith('/export')) {
					return new Response('{"error":{"code":"export_not_ready","message":"unfinished"}}', {
						status: 409,
						headers: { 'content-type': 'application/json' }
					});
				}
				if (url.includes('/api/threads')) {
					return Response.json({ name_threads: [], circled_topics: [] });
				}
				return Response.json(liveDetail());
			})
		);
		const clickSpy = vi.spyOn(window.HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
		try {
			const snaps: Array<ReturnType<typeof emptyScreen>> = [];
			const controller = await mounted(snaps);
			await controller.exportBundle();
			await vi.waitFor(() => {
				expect(snaps.at(-1)?.exporting).toBe(false);
			});
			expect(snaps.at(-1)?.notice).toContain('never finished');
			expect(clickSpy).not.toHaveBeenCalled();
			controller.destroy();
		} finally {
			clickSpy.mockRestore();
			vi.unstubAllGlobals();
		}
	});
});
