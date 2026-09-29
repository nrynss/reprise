// Proofs for the shared responsive shell. A sample page built from
// the shared classes renders at phone, tablet, monitor and wide
// widths. Every width shows no sideways scroll and a column within
// the measure. Cards in one row share a height, and every button
// keeps the minimum tap target on the phone width. The stylesheet
// loads from disk, so the proofs measure the shipped rules. Run
// beside this file:
// npx playwright test -c src/lib/components/layout.playwright.config.ts
import { expect, test } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const css = readFileSync(
	join(dirname(fileURLToPath(import.meta.url)), '..', '..', 'app.css'),
	'utf8'
);

function card(number: number, title: string, meta: string, status: string): string {
	return `<li><article class="card">
		<div class="cover" role="img" aria-label="Cover of episode ${number}"><span>EP.${number}</span></div>
		<h2 class="title">${title}</h2>
		<p class="dur">${meta}</p>
		<p class="status">${status}</p>
	</article></li>`;
}

function sample(mainClass: string): string {
	return `<!doctype html><html><head><meta charset="utf-8">
		<meta name="viewport" content="width=device-width, initial-scale=1">
		<style>${css}</style></head>
	<body><main class="${mainClass}">
		<p class="eyebrow">Season one</p>
		<h1>A quiet week with long walks</h1>
		<p class="sub">Your episodes. Only you can see them until you publish.</p>
		<span class="demo-badge">Demo</span>
		<div class="tabs">
			<a class="button secondary active" href="/">Gallery</a>
			<a class="button secondary" href="/threads">Threads</a>
			<a class="button secondary" href="/account">Sign in</a>
		</div>
		<div class="actions">
			<a class="button" href="/record">Record a new episode</a>
			<button class="button secondary" type="button">Copy link</button>
			<button class="button quiet" type="button">Revoke</button>
		</div>
		<section class="section" aria-label="Sample section">
			<h2>Chapters</h2>
			<p class="transcript">The host opens on the conversation you kept circling last time.</p>
		</section>
		<ol class="cards" aria-label="Episodes, newest first">
			${card(4, 'A quiet week', '12 min', 'Ready')}
			${card(3, 'The long walk home through the rain with news to share', '34 min', 'Rendering')}
			${card(2, 'Bread, again', '8 min', 'Draft')}
			${card(1, 'What the neighbours said about the fence and the dog', '21 min', 'Ready')}
		</ol>
	</main></body></html>`;
}

async function holdsColumn(
	page: import('@playwright/test').Page,
	html: string,
	width: number,
	maxMain: number
): Promise<void> {
	await page.setViewportSize({ width, height: 800 });
	await page.setContent(html, { waitUntil: 'domcontentloaded' });
	const scroll = await page.evaluate(() => ({
		scroll: document.documentElement.scrollWidth,
		inner: window.innerWidth
	}));
	expect(scroll.scroll, `sideways scroll at ${width}px`).toBeLessThanOrEqual(scroll.inner + 1);
	const main = page.locator('main');
	const box = await main.boundingBox();
	if (box === null) throw new Error('The sample page reported no layout.');
	expect(box.width, `main width at ${width}px`).toBeLessThanOrEqual(maxMain + 1);
}

for (const width of [375, 768, 1280, 1920]) {
	test(`the shared column holds at ${width}px`, async ({ page }) => {
		await holdsColumn(page, sample(''), width, 1024);
		const bodySize = await page.evaluate(
			() => parseFloat(getComputedStyle(document.body).fontSize)
		);
		expect(bodySize, `body size at ${width}px`).toBeGreaterThanOrEqual(16);
		const rows = await page.locator('.cards .card').evaluateAll((cards) =>
			cards.map((entry) => {
				const rect = (entry as HTMLElement).getBoundingClientRect();
				return { top: rect.top, height: rect.height };
			})
		);
		rows.sort((a, b) => a.top - b.top);
		const grouped: number[][] = [];
		for (const row of rows) {
			const open = grouped.find((group) => Math.abs(group[0] - row.top) <= 2);
			if (open) open.push(row.height);
			else grouped.push([row.top, row.height]);
		}
		for (const group of grouped) {
			const heights = group.slice(1);
			if (heights.length < 2) continue;
			const spread = Math.max(...heights) - Math.min(...heights);
			expect(spread, `card heights at ${width}px`).toBeLessThanOrEqual(2);
		}
		const shorts = await page.locator('button, .button').evaluateAll((controls) =>
			controls.map((entry) => (entry as HTMLElement).getBoundingClientRect().height)
		);
		for (const height of shorts) {
			expect(height, `tap target at ${width}px`).toBeGreaterThanOrEqual(43.5);
		}
	});
}

test('the narrow column holds the short page', async ({ page }) => {
	await holdsColumn(page, sample('narrow'), 1280, 576);
});

test('the wide column opens on a monitor', async ({ page }) => {
	await holdsColumn(page, sample('wide'), 1280, 1216);
	await holdsColumn(page, sample('wide'), 1920, 1216);
});
