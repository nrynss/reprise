// Layout proofs across every page. Each page opens in its fixture mode
// at phone, tablet, monitor and wide widths. Every width shows no
// sideways scroll and a column within the page measure. The gallery
// grid keeps equal card heights in each row, and every control keeps
// the minimum tap target on the phone width. The default run serves
// the built app, so the proofs measure the shipped pages. This file
// lives in the shared tests folder, so the plain run picks it up
// with no extra wiring.
import { expect, test, type Page } from '@playwright/test';

const WIDTHS = [375, 768, 1280, 1920];

interface LayoutPage {
	name: string;
	path: string;
	measure: number;
	cards: boolean;
}

const PAGES: LayoutPage[] = [
	{ name: 'gallery', path: '/?fixture=1', measure: 1024, cards: true },
	{ name: 'threads', path: '/threads?fixture=1', measure: 1024, cards: false },
	{ name: 'episode', path: '/episode/ep-4?fixture=1', measure: 1024, cards: false },
	{ name: 'editor', path: '/episode/draft-1/edit?fixture=1', measure: 1216, cards: false },
	{ name: 'record', path: '/record?mock=1', measure: 1024, cards: false },
	{
		name: 'processing',
		path: '/processing?episode=e1&transcript=t1&editorial=e1&mock=1&uploads=done&userBytes=10&hostBytes=20',
		measure: 1024,
		cards: false
	},
	{ name: 'welcome', path: '/welcome?seed=1&fixture=1', measure: 1024, cards: false },
	{
		name: 'share',
		path: '/share/fixture-token?fixture=published',
		measure: 576,
		cards: false
	},
	{ name: 'account', path: '/account', measure: 1024, cards: false },
	{ name: 'account delete', path: '/account/delete', measure: 1024, cards: false },
	{ name: 'account google', path: '/account/google', measure: 1024, cards: false },
	{ name: 'account callback', path: '/account/google/callback', measure: 1024, cards: false },
	{ name: 'privacy', path: '/privacy', measure: 1024, cards: false },
	{ name: 'admin', path: '/admin', measure: 1024, cards: false }
];

async function holdsColumn(page: Page, width: number, maxMain: number): Promise<void> {
	await page.setViewportSize({ width, height: 800 });
	const scroll = await page.evaluate(() => ({
		scroll: document.documentElement.scrollWidth,
		inner: window.innerWidth
	}));
	expect(scroll.scroll, `sideways scroll at ${width}px`).toBeLessThanOrEqual(scroll.inner + 1);
	const box = await page.locator('main').first().boundingBox();
	if (box === null) throw new Error('The page reported no layout.');
	expect(box.width, `main width at ${width}px`).toBeLessThanOrEqual(maxMain + 1);
}

async function holdsCardRows(page: Page, width: number): Promise<void> {
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
}

async function holdsTapTargets(page: Page, width: number): Promise<void> {
	const targets = await page.locator('button, .button').evaluateAll((controls) =>
		controls.map((entry) => {
			const box = entry as HTMLElement;
			const flowingWord =
				box.closest('section[aria-label*="Transcript"]') !== null ||
				box.closest('.words') !== null;
			return { height: box.getBoundingClientRect().height, flowingWord };
		})
	);
	// Word buttons read as flowing text, so the target size minimum
	// exempts them as inline targets. Every other control still holds
	// the minimum. The exemption stays scoped to transcript sections
	// and word containers, so a small control elsewhere still fails.
	for (const target of targets) {
		if (target.flowingWord) continue;
		expect(target.height, `tap target at ${width}px`).toBeGreaterThanOrEqual(43.5);
	}
}

for (const entry of PAGES) {
	test(`${entry.name} holds its column at every width`, async ({ page }) => {
		for (const width of WIDTHS) {
			await page.setViewportSize({ width, height: 800 });
			await page.goto(entry.path, { waitUntil: 'domcontentloaded' });
			await page.locator('main').first().waitFor();
			if (entry.cards) {
				await page.locator('.cards .card').first().waitFor({ timeout: 10_000 });
				await holdsCardRows(page, width);
			}
			await holdsColumn(page, width, entry.measure);
			if (width === 375) await holdsTapTargets(page, width);
		}
	});
}
