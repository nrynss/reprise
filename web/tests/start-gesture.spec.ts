// The start control has to open the audio context and request the
// microphone before the session mint returns. WebKit ends the gesture at
// the first wait, so this proof holds that mint and reads the order.
import { expect, test } from '@playwright/test';

test('a start gesture opens the microphone before the session mint returns', async ({ page }) => {
	await page.addInitScript(() => {
		const marks: string[] = [];
		(window as unknown as { __captureMarks?: string[] }).__captureMarks = marks;
		const real = window.AudioContext;
		const Wrapped = function (this: AudioContext, options?: AudioContextOptions): AudioContext {
			const context = new real(options);
			marks.push('context');
			const resume = context.resume.bind(context);
			context.resume = () => {
				marks.push('resume');
				return resume();
			};
			return context;
		};
		Wrapped.prototype = real.prototype;
		Object.defineProperty(window, 'AudioContext', {
			configurable: true,
			writable: true,
			value: Wrapped
		});
		const request = (): Promise<MediaStream> => {
			marks.push('microphone');
			return new Promise(() => undefined);
		};
		const devices = navigator.mediaDevices;
		if (devices) {
			devices.getUserMedia = request;
		} else {
			Object.defineProperty(navigator, 'mediaDevices', {
				configurable: true,
				value: { getUserMedia: request }
			});
		}
	});

	let releaseMint = (): void => undefined;
	const held = new Promise<void>((resolve) => {
		releaseMint = resolve;
	});
	await page.route(
		(url) => url.pathname === '/api/sessions',
		async (route) => {
			if (route.request().method() !== 'POST') {
				await route.fallback();
				return;
			}
			await held;
			await route.abort();
		}
	);

	try {
		await page.goto('/record');
		await page.getByRole('button', { name: 'Start session' }).click();
		const marks = await page.evaluate(() => {
			const target = window as unknown as { __captureMarks?: string[] };
			return target.__captureMarks ?? [];
		});
		expect(marks).toEqual(['context', 'resume', 'microphone']);
	} finally {
		releaseMint();
	}
});
