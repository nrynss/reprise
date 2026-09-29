// Playwright proofs for the editor screen. The fixture proofs run on the
// scripted draft with no backend. The live proofs stub the episode detail
// and one stem, so they pin the stored shapes with no network and no real
// clock beyond the audio element itself. This spec runs with the suite
// beside the route:
// npx playwright test -c src/routes/episode/[id]/edit/edit.playwright.config.ts
import { expect, test, type Locator, type Page } from '@playwright/test';

const DRAFT = '/episode/draft-1/edit?fixture=1';

// One mono 16-bit WAV of a plain tone, built in the runner and served by
// route, so the element reports a known length with no fixture file.
function toneWav(seconds: number, rate: number): Uint8Array {
	const frames = Math.floor(seconds * rate);
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
		const sample = Math.sin((2 * Math.PI * 220 * frame) / rate) * 0.4;
		view.setInt16(44 + frame * 2, Math.round(sample * 32767), true);
	}
	return new Uint8Array(buffer);
}

// Base 64 without node types, chunked so no call frame overflows.
function base64(bytes: Uint8Array): string {
	let binary = '';
	const step = 0x8000;
	for (let at = 0; at < bytes.length; at += step) {
		binary += String.fromCharCode(...bytes.subarray(at, at + step));
	}
	return btoa(binary);
}

// A stored draft of forty short words ending near fifteen seconds, one
// accepted cut, a title and notes, and no callback or cold open, with a
// twenty second tone behind it as a data address.
async function serveLiveDraft(page: Page): Promise<void> {
	const words = Array.from({ length: 40 }, (_, index) => ({
		text: `w${index}`,
		start: 0.4 + index * 0.36,
		end: 0.4 + index * 0.36 + 0.3
	}));
	const audioUrl = `data:audio/wav;base64,${base64(toneWav(20, 8000))}`;
	await page.route(
		(url) => url.pathname === '/api/episodes/live-9',
		(route) =>
			route.fulfill({
				json: {
					episode: { id: 'live-9', number: 9, title: 'Live take', state: 'draft', visibility: 'private' },
					proposals: [
						{ id: 'cut-a', kind: 'cut', start_word: 0, end_word: 2, reason: 'Trim the open.', decision: 'accepted' },
						{ id: 'title-9', kind: 'title', start_word: 0, end_word: 0, reason: 'Live take', decision: '' },
						{ id: 'notes-9', kind: 'show_notes', start_word: 0, end_word: 0, reason: 'Some notes.', decision: '' }
					],
					words,
					audio_url: audioUrl,
					render_audio_url: ''
				}
			})
	);
	await page.route((url) => url.pathname === '/api/episodes/live-9/decisions', (route) =>
		route.fulfill({ json: {} })
	);
	await page.goto('/episode/live-9/edit');
	await expect(page.getByRole('status', { name: 'Applied cuts' })).toHaveText('1 cuts applied');
}

// Whether a bright playhead column stands near the expected bitmap x.
// The readout rounds to whole seconds while the line draws at the exact
// position, so the scan covers the rounding window. Only the playhead
// ink passes: peaks, regions and the tint all stay below the threshold.
async function brightColumnNear(canvas: Locator, x: number): Promise<boolean> {
	return canvas.evaluate((el, px) => {
		const node = el as HTMLCanvasElement;
		const drawing = node.getContext('2d');
		if (!drawing) return false;
		const from = Math.max(0, px - 13);
		const to = Math.min(node.width - 1, px + 13);
		const stride = to - from + 1;
		const data = drawing.getImageData(from, 0, stride, node.height).data;
		for (let col = 0; col < stride; col += 1) {
			let bright = 0;
			for (let row = 0; row < node.height; row += 1) {
				const at = (row * stride + col) * 4;
				if ((data[at] ?? 0) > 200 && (data[at + 1] ?? 0) > 200 && (data[at + 2] ?? 0) > 200) {
					bright += 1;
				}
			}
			if (bright >= node.height / 2) return true;
		}
		return false;
	}, x);
}

// The playhead column in bitmap pixels, from the slider values alone.
async function playheadX(page: Page): Promise<number> {
	const canvas = page.getByRole('slider', { name: 'Draft waveform. Arrow keys seek.' });
	const now = Number(await canvas.getAttribute('aria-valuenow'));
	const max = Number(await canvas.getAttribute('aria-valuemax'));
	return Math.max(0, Math.min(599, Math.round((now / Math.max(1, max)) * 600)));
}

test('playing moves the playhead line across the waveform', async ({ page }) => {
	await page.goto(DRAFT);
	await expect(page.getByRole('status', { name: 'Applied cuts' })).toHaveText('3 cuts applied');
	const canvas = page.getByRole('slider', { name: 'Draft waveform. Arrow keys seek.' });
	await page.getByRole('button', { name: 'Play draft' }).click();
	await expect
		.poll(async () => Number(await canvas.getAttribute('aria-valuenow')), { timeout: 15_000 })
		.toBeGreaterThan(0);
	await page.getByRole('button', { name: 'Pause draft' }).click();
	const x = await playheadX(page);
	expect(await brightColumnNear(canvas, x)).toBe(true);
	await canvas.focus();
	await page.keyboard.press('Home');
	await expect(page.getByRole('status', { name: 'Playback position' })).toHaveText('0:00 of 0:24');
	expect(await brightColumnNear(canvas, x)).toBe(false);
});

test('a pointer seek parks the playhead line near the middle', async ({ page }) => {
	await page.goto(DRAFT);
	await expect(page.getByRole('status', { name: 'Applied cuts' })).toHaveText('3 cuts applied');
	const canvas = page.getByRole('slider', { name: 'Draft waveform. Arrow keys seek.' });
	const box = await canvas.boundingBox();
	if (!box) throw new Error('waveform has no box');
	await canvas.click({ position: { x: Math.floor(box.width / 2), y: 5 } });
	await expect(page.getByRole('status', { name: 'Playback position' })).toHaveText('0:12 of 0:24');
	const bright = await canvas.evaluate((el) => {
		const node = el as HTMLCanvasElement;
		const drawing = node.getContext('2d');
		if (!drawing) return false;
		const middle = Math.floor(node.width / 2);
		const data = drawing.getImageData(middle - 4, 0, 9, node.height).data;
		for (let index = 0; index < data.length; index += 4) {
			if ((data[index] ?? 0) > 200 && (data[index + 1] ?? 0) > 200 && (data[index + 2] ?? 0) > 200) {
				return true;
			}
		}
		return false;
	});
	expect(bright).toBe(true);
});

test('a live draft draws peaks away from the centre line', async ({ page }) => {
	await serveLiveDraft(page);
	const canvas = page.getByRole('slider', { name: 'Draft waveform. Arrow keys seek.' });
	await expect
		.poll(
			async () =>
				canvas.evaluate((el) => {
					const node = el as HTMLCanvasElement;
					const drawing = node.getContext('2d');
					if (!drawing) return 0;
					const top = drawing.getImageData(60, 0, 530, 26).data;
					let lit = 0;
					for (let index = 3; index < top.length; index += 4) {
						if ((top[index] ?? 0) > 0) lit += 1;
					}
					return lit;
				}),
			{ timeout: 15_000 }
		)
		.toBeGreaterThan(0);
});

test('the seekbar and the back control share the waveform position', async ({ page }) => {
	await page.goto(DRAFT);
	await expect(page.getByRole('status', { name: 'Applied cuts' })).toHaveText('3 cuts applied');
	const canvas = page.getByRole('slider', { name: 'Draft waveform. Arrow keys seek.' });
	const box = await canvas.boundingBox();
	if (!box) throw new Error('waveform has no box');
	await canvas.click({ position: { x: Math.floor(box.width / 2), y: 5 } });
	const seek = page.getByRole('slider', { name: 'Seek through the draft' });
	await expect(seek).toHaveValue('12');
	await page.getByRole('button', { name: 'Back fifteen seconds' }).click();
	await expect(page.getByRole('status', { name: 'Playback position' })).toHaveText('0:00 of 0:24');
	await expect(seek).toHaveValue('0');
});

test('reverting a cut writes its decision row', async ({ page }) => {
	await page.goto(DRAFT);
	await expect(page.getByRole('heading', { name: 'The only place nobody needs anything' })).toBeVisible();
	await expect(page.getByRole('status', { name: 'Applied cuts' })).toHaveText('3 cuts applied');

	await page.getByRole('button', { name: 'Revert cut: False start at the top of the answer.' }).click();

	await expect(page.getByRole('status', { name: 'Applied cuts' })).toHaveText('2 cuts applied');
	const decisions = page.getByRole('region', { name: 'Decisions' });
	await expect(decisions.getByText('Reverted: False start at the top of the answer. (prop-cut-1)')).toBeVisible();
});

test('reverting each non-cut proposal persists its decision row', async ({ page }) => {
	await page.goto(DRAFT);
	await expect(page.getByRole('heading', { name: 'The only place nobody needs anything' })).toBeVisible();

	await page.getByRole('button', { name: 'Revert cold open proposal' }).click();
	await expect(page.getByText('Cold open reverted. The episode starts at the top.')).toBeVisible();

	await page.getByRole('button', { name: 'Revert title proposal' }).click();
	await expect(page.getByRole('heading', { name: 'Episode draft-1' })).toBeVisible();

	await page.getByRole('button', { name: 'Revert show notes proposal' }).click();
	await expect(page.getByText('Show notes reverted. Nothing stands in their place.')).toBeVisible();

	const callback = page.getByRole('button', { name: 'Revert callback proposal' });
	await callback.focus();
	await expect(callback).toBeFocused();
	await page.keyboard.press('Enter');
	await expect(page.getByText('Callback reverted and cleared from the next opening.')).toBeVisible();

	const decisions = page.getByRole('region', { name: 'Decisions' });
	await expect(decisions.getByText('(prop-cold-open)')).toBeVisible();
	await expect(decisions.getByText('(prop-title)')).toBeVisible();
	await expect(decisions.getByText('(prop-notes)')).toBeVisible();
	await expect(decisions.getByText('(prop-callback)')).toBeVisible();
});

test('a keyboard-only run completes the edit and marks done', async ({ page }) => {
	await page.goto(DRAFT);
	await expect(page.getByRole('status', { name: 'Applied cuts' })).toHaveText('3 cuts applied');

	const revert = page.getByRole('button', { name: 'Revert cut: Bus timetable tangent that goes nowhere.' });
	await revert.focus();
	await expect(revert).toBeFocused();
	await page.keyboard.press('Enter');
	await expect(page.getByRole('status', { name: 'Applied cuts' })).toHaveText('2 cuts applied');

	const preview = page.getByRole('button', { name: 'Preview the cold open' });
	await preview.focus();
	await page.keyboard.press('Enter');

	const done = page.getByRole('button', { name: 'Mark episode done' });
	await done.focus();
	await page.keyboard.press('Enter');
	const confirm = page.getByRole('button', { name: 'Confirm mark done' });
	await expect(confirm).toBeVisible();
	await confirm.focus();
	await page.keyboard.press('Enter');
	await expect(page.getByText('Render running: fixture render.')).toBeVisible();
});

test('clicking a word seeks the readout', async ({ page }) => {
	await page.goto(DRAFT);
	await expect(page.getByRole('status', { name: 'Applied cuts' })).toHaveText('3 cuts applied');
	await page.getByRole('button', { name: 'shop. Activate to seek.' }).click();
	await expect(page.getByRole('status', { name: 'Playback position' })).toHaveText('0:03 of 0:24');
});

test('adjacent words read with a space between them', async ({ page }) => {
	await page.goto(DRAFT);
	await expect(page.getByRole('status', { name: 'Applied cuts' })).toHaveText('3 cuts applied');
	const words = page.locator('p.words');
	await expect(words).toContainText('I mean');
	await expect(words).toContainText('the shop');
});

test('a pointer down on the waveform seeks, and a drag with the button held follows', async ({
	page
}) => {
	await page.goto(DRAFT);
	await expect(page.getByRole('status', { name: 'Applied cuts' })).toHaveText('3 cuts applied');
	const position = page.getByRole('status', { name: 'Playback position' });
	await expect(position).toHaveText('0:00 of 0:24');

	const canvas = page.getByRole('slider', { name: 'Draft waveform. Arrow keys seek.' });
	const box = await canvas.boundingBox();
	if (!box) throw new Error('waveform has no box');
	const middle = Math.floor(box.width / 2);
	await canvas.click({ position: { x: middle, y: 5 } });
	await expect(position).toHaveText('0:12 of 0:24');

	await page.mouse.move(box.x + middle + 120, box.y + 5);
	await expect(position).toHaveText('0:12 of 0:24');

	await page.mouse.move(box.x + middle + 170, box.y + 5, { steps: 3 });
	await page.mouse.down();
	await page.mouse.move(box.x + middle + 270, box.y + 5, { steps: 3 });
	await page.mouse.up();
	await expect(position).toHaveText(/0:1[4-9] of 0:24/);
});

test('revert a cut, then play and seek still answer', async ({ page }) => {
	await page.goto(DRAFT);
	await expect(page.getByRole('status', { name: 'Applied cuts' })).toHaveText('3 cuts applied');
	await page.getByRole('button', { name: 'Revert cut: False start at the top of the answer.' }).click();
	await expect(page.getByRole('status', { name: 'Applied cuts' })).toHaveText('2 cuts applied');

	await page.getByRole('button', { name: 'Play draft' }).click();
	const position = page.getByRole('status', { name: 'Playback position' });
	await expect(position).toHaveText(/0:0[1-9] of 0:24/, { timeout: 15_000 });

	await page.getByRole('button', { name: 'Pause draft' }).click();
	const canvas = page.getByRole('slider', { name: 'Draft waveform. Arrow keys seek.' });
	const box = await canvas.boundingBox();
	if (!box) throw new Error('waveform has no box');
	await canvas.click({ position: { x: Math.floor(box.width / 2), y: 5 } });
	await expect(position).toHaveText('0:12 of 0:24');
});

test('revert every cut, then play and drag still answer', async ({ page }) => {
	await page.goto(DRAFT);
	await expect(page.getByRole('status', { name: 'Applied cuts' })).toHaveText('3 cuts applied');
	// The first cut starts at word 0, as the draft that stranded Firefox did.
	await page.getByRole('button', { name: 'Revert cut: False start at the top of the answer.' }).click();
	await page.getByRole('button', { name: 'Revert cut: Bus timetable tangent that goes nowhere.' }).click();
	await page.getByRole('button', { name: 'Revert cut: Trailing fragment after the harvest line.' }).click();
	await expect(page.getByRole('status', { name: 'Applied cuts' })).toHaveText('0 cuts applied');

	await page.getByRole('button', { name: 'Play draft' }).click();
	const position = page.getByRole('status', { name: 'Playback position' });
	await expect(position).toHaveText(/0:0[1-9] of 0:24/, { timeout: 15_000 });

	await page.getByRole('button', { name: 'Pause draft' }).click();
	const canvas = page.getByRole('slider', { name: 'Draft waveform. Arrow keys seek.' });
	const box = await canvas.boundingBox();
	if (!box) throw new Error('waveform has no box');
	await canvas.click({ position: { x: Math.floor(box.width / 2), y: 5 } });
	await expect(position).toHaveText('0:12 of 0:24');
});

test('revert the live cut, then play and drag still answer', async ({ page }) => {
	await serveLiveDraft(page);
	await page.getByRole('button', { name: 'Revert cut: Trim the open.' }).click();
	await expect(page.getByRole('status', { name: 'Applied cuts' })).toHaveText('0 cuts applied');

	await page.getByRole('button', { name: 'Play draft' }).click();
	const position = page.getByRole('status', { name: 'Playback position' });
	await expect(position).toHaveText(/0:0[1-9] of 0:20/, { timeout: 15_000 });

	await page.getByRole('button', { name: 'Pause draft' }).click();
	const canvas = page.getByRole('slider', { name: 'Draft waveform. Arrow keys seek.' });
	const box = await canvas.boundingBox();
	if (!box) throw new Error('waveform has no box');
	await canvas.click({ position: { x: Math.floor(box.width / 2), y: 5 } });
	await expect(position).toHaveText('0:10 of 0:20');
});

test('the shown length follows the audio, and the position never passes it', async ({ page }) => {
	await serveLiveDraft(page);
	const position = page.getByRole('status', { name: 'Playback position' });
	await expect(position).toHaveText('0:00 of 0:20', { timeout: 15_000 });

	const canvas = page.getByRole('slider', { name: 'Draft waveform. Arrow keys seek.' });
	await canvas.focus();
	await page.keyboard.press('End');
	await expect(position).toHaveText('0:20 of 0:20');
	await expect(canvas).toHaveAttribute('aria-valuemax', '20');
});

test('a draft with no callback and no cold open shows neither block', async ({ page }) => {
	await serveLiveDraft(page);
	await expect(page.getByRole('region', { name: 'Proposed cold open' })).toHaveCount(0);
	await expect(page.getByRole('region', { name: 'Planted for next time' })).toHaveCount(0);
	await expect(page.getByRole('button', { name: 'Revert callback proposal' })).toHaveCount(0);
	await expect(page.getByRole('button', { name: 'Preview the cold open' })).toHaveCount(0);
});

test('the editor links back to the gallery', async ({ page }) => {
	await page.goto(DRAFT);
	const back = page.getByRole('link', { name: 'Back to the gallery' });
	await expect(back).toBeVisible();
	await expect(back).toHaveAttribute('href', '/');
});

test('the screen passes both gates', async ({ page }) => {
	await page.goto(`${DRAFT}&gate=1`);
	await expect(page.locator('#gate-status')).toHaveText('Gates passed: accessibility and contrast.', {
		timeout: 20_000
	});
});

test('a refused live draft shows Retry and no scripted title', async ({ page }) => {
	let calls = 0;
	await page.route(
		(url) => url.pathname === '/api/episodes/live-500',
		(route) => {
			calls += 1;
			if (calls === 1) return route.fulfill({ status: 500, body: 'nope' });
			return route.fulfill({
				json: {
					episode: {
						id: 'live-500',
						number: 9,
						title: 'Live take',
						state: 'draft',
						visibility: 'private'
					},
					proposals: [],
					words: [{ text: 'Hello', start: 0, end: 0.4 }],
					audio_url: '',
					render_audio_url: ''
				}
			});
		}
	);
	await page.goto('/episode/live-500/edit');
	await expect(page.getByText('This episode did not load')).toBeVisible();
	await expect(page.getByRole('button', { name: 'Retry' })).toBeVisible();
	await expect(
		page.getByRole('heading', { name: 'The only place nobody needs anything' })
	).toHaveCount(0);
	await page.getByRole('button', { name: 'Retry' }).click();
	await expect(page.getByRole('heading', { name: 'Live take' })).toBeVisible();
});

test('mark done follows its render to done with no refusal', async ({ page }) => {
	const words = Array.from({ length: 40 }, (_, index) => ({
		text: `w${index}`,
		start: 0.4 + index * 0.36,
		end: 0.4 + index * 0.36 + 0.3
	}));
	const audioUrl = `data:audio/wav;base64,${base64(toneWav(20, 8000))}`;
	let detailCalls = 0;
	await page.route(
		(url) => url.pathname === '/api/episodes/live-done',
		(route) => {
			detailCalls += 1;
			return route.fulfill({
				json: {
					episode: {
						id: 'live-done',
						number: 9,
						title: 'Live take',
						state: detailCalls === 1 ? 'draft' : 'ready',
						visibility: 'private'
					},
					proposals: [
						{ id: 'cut-a', kind: 'cut', start_word: 0, end_word: 2, reason: 'Trim the open.', decision: 'accepted' },
						{ id: 'title-9', kind: 'title', start_word: 0, end_word: 0, reason: 'Live take', decision: '' },
						{ id: 'notes-9', kind: 'show_notes', start_word: 0, end_word: 0, reason: 'Some notes.', decision: '' }
					],
					words,
					audio_url: audioUrl,
					render_audio_url: ''
				}
			});
		}
	);
	await page.route((url) => url.pathname === '/api/episodes/live-done/decisions', (route) =>
		route.fulfill({ json: {} })
	);
	await page.route(
		(url) => url.pathname === '/api/episodes/live-done/done',
		(route) => route.fulfill({ status: 202, contentType: 'application/json', body: JSON.stringify({ job_id: 'r-202', queued: false }) })
	);
	await page.route('**/api/jobs/r-202', (route) =>
		route.fulfill({
			status: 200,
			contentType: 'application/json',
			body: JSON.stringify({ jobId: 'r-202', status: 'done' })
		})
	);
	await page.route('**/api/jobs/r-202/events', (route) =>
		route.fulfill({
			status: 200,
			contentType: 'text/event-stream',
			body: 'event: done\ndata: {"job_id":"r-202","status":"done"}\n\n'
		})
	);
	await page.goto('/episode/live-done/edit');
	await expect(page.getByRole('status', { name: 'Applied cuts' })).toHaveText('1 cuts applied');

	await page.getByRole('button', { name: 'Mark episode done' }).click();
	await page.getByRole('button', { name: 'Confirm mark done' }).click();

	await expect(page.getByText(/The render is done/)).toBeVisible({ timeout: 15_000 });
	await expect(page.getByText('refused')).toHaveCount(0);
	await expect(page.getByText('could not be followed')).toHaveCount(0);
	await expect(page.getByText('The render is done. Open the gallery to hear the finished episode.')).toBeVisible();
});
