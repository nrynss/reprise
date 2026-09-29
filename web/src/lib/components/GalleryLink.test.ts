// Pins for the shared season navigation. The gallery link keeps one
// label and one address on every page, and the season tabs mark the
// open page with a filled tab. Both render as anchors, so middle-click
// keeps working. All three ride the shared button classes, so one
// stylesheet sets their height and shape.
import { afterEach, describe, expect, it, vi } from 'vitest';
// mount and unmount come from the client runtime by path. The bare
// specifier resolves to the server build under vitest, which refuses to
// mount. The relative path reaches the same client build the page ships.
// The runtime ships no declaration file on this path.
// @ts-expect-error: untyped client runtime path, typed as used below.
import { mount, unmount } from '../../../node_modules/svelte/src/internal/client/render.js';
import GalleryLink from './GalleryLink.svelte';
import SeasonNav from './SeasonNav.svelte';

vi.mock('$app/paths', () => ({ resolve: (path: string) => path }));

afterEach(() => {
	document.body.innerHTML = '';
	vi.unstubAllGlobals();
});

describe('gallery link', () => {
	it('renders one link named Back to the gallery at the season root', () => {
		const app = mount(GalleryLink, { target: document.body });
		try {
			const links = Array.from(document.body.querySelectorAll('a'));
			expect(links).toHaveLength(1);
			const link = links[0] as HTMLAnchorElement;
			expect(link.textContent).toContain('Back to the gallery');
			expect(link.getAttribute('href')).toBe('/');
			expect(link.classList.contains('button')).toBe(true);
			expect(link.classList.contains('secondary')).toBe(true);
		} finally {
			unmount(app);
		}
	});

	it('hides the leading arrow from assistive names', () => {
		const app = mount(GalleryLink, { target: document.body });
		try {
			const link = document.body.querySelector('a');
			const arrow = link?.querySelector('span[aria-hidden="true"]');
			expect(arrow?.textContent).toBe('←');
			const named = link?.textContent?.replace(arrow?.textContent ?? '', '').trim();
			expect(named).toBe('Back to the gallery');
		} finally {
			unmount(app);
		}
	});
});

describe('season nav', () => {
	it('marks the gallery tab current on the gallery page', () => {
		const app = mount(SeasonNav, { target: document.body, props: { current: 'gallery' } });
		try {
			const gallery = document.body.querySelector('a[href="/"]');
			const threads = document.body.querySelector('a[href="/threads"]');
			expect(gallery?.textContent).toBe('Gallery');
			expect(gallery?.getAttribute('aria-current')).toBe('page');
			expect(gallery?.classList.contains('active')).toBe(true);
			expect(threads?.getAttribute('aria-current')).toBeNull();
			expect(threads?.classList.contains('active')).toBe(false);
		} finally {
			unmount(app);
		}
	});

	it('marks the threads tab current on the threads page', () => {
		const app = mount(SeasonNav, { target: document.body, props: { current: 'threads' } });
		try {
			const gallery = document.body.querySelector('a[href="/"]');
			const threads = document.body.querySelector('a[href="/threads"]');
			expect(threads?.getAttribute('aria-current')).toBe('page');
			expect(threads?.classList.contains('active')).toBe(true);
			expect(gallery?.getAttribute('aria-current')).toBeNull();
			expect(gallery?.classList.contains('active')).toBe(false);
		} finally {
			unmount(app);
		}
	});

	it('keeps the fixture season behind its query string', () => {
		const app = mount(SeasonNav, {
			target: document.body,
			props: { current: 'threads', fixture: true }
		});
		try {
			expect(document.body.querySelector('a[href="/?fixture=1"]')?.textContent).toBe('Gallery');
			const threads = document.body.querySelector('a[href="/threads?fixture=1"]');
			expect(threads?.getAttribute('aria-current')).toBe('page');
		} finally {
			unmount(app);
		}
	});
});
