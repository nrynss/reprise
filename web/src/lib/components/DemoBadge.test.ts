// Pins for the demo marker. Demo mode shows one small pill reading
// Demo, never a sentence about the season behind it.
import { afterEach, describe, expect, it } from 'vitest';
// mount and unmount come from the client runtime by path. The bare
// specifier resolves to the server build under vitest, which refuses to
// mount. The relative path reaches the same client build the page ships.
// The runtime ships no declaration file on this path.
// @ts-expect-error: untyped client runtime path, typed as used below.
import { mount, unmount } from '../../../node_modules/svelte/src/internal/client/render.js';
import DemoBadge from './DemoBadge.svelte';

afterEach(() => {
	document.body.innerHTML = '';
});

describe('demo badge', () => {
	it('renders one pill reading Demo', () => {
		const app = mount(DemoBadge, { target: document.body });
		try {
			const badge = document.body.querySelector('.demo-badge');
			expect(badge?.textContent).toBe('Demo');
			expect(badge?.tagName.toLowerCase()).toBe('span');
		} finally {
			unmount(app);
		}
	});
});
