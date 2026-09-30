import { afterEach, describe, expect, it, vi } from 'vitest';
import { ensureSeasonCopy, resetSeasonCopy, SEASON_COPY_TIMEOUT_MS } from './season-copy';

afterEach(() => {
	resetSeasonCopy();
	vi.useRealTimers();
});

describe('ensureSeasonCopy', () => {
	it('asks the welcome read once per load and shares the answer', async () => {
		const fetchFn = vi.fn<(url: string, init?: RequestInit) => Promise<Response>>(
			async () => new Response('{"mode":"seeded"}', { status: 200 })
		);
		const first = ensureSeasonCopy(fetchFn);
		const second = ensureSeasonCopy(fetchFn);
		expect(second).toBe(first);
		await first;
		await ensureSeasonCopy(fetchFn);
		expect(fetchFn).toHaveBeenCalledTimes(1);
		expect(fetchFn.mock.calls[0]?.[0]).toBe('/api/welcome');
	});

	it('resolves when the read is refused', async () => {
		const fetchFn = vi.fn(async () => new Response('nope', { status: 500 }));
		await expect(ensureSeasonCopy(fetchFn)).resolves.toBeUndefined();
	});

	it('resolves when the network fails', async () => {
		const fetchFn = vi.fn(async () => {
			throw new TypeError('network down');
		});
		await expect(ensureSeasonCopy(fetchFn)).resolves.toBeUndefined();
	});

	it('stops waiting on a stuck read', async () => {
		vi.useFakeTimers();
		const fetchFn = vi.fn(
			(_url: string, init?: RequestInit) =>
				new Promise<Response>((_resolve, reject) => {
					init?.signal?.addEventListener('abort', () =>
						reject(new DOMException('aborted', 'AbortError'))
					);
				})
		);
		const done = ensureSeasonCopy(fetchFn);
		await vi.advanceTimersByTimeAsync(SEASON_COPY_TIMEOUT_MS);
		await expect(done).resolves.toBeUndefined();
	});
});
