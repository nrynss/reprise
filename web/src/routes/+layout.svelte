<script lang="ts">
	import '../app.css';
	import { browser } from '$app/environment';
	import { page } from '$app/state';
	import { ensureSeasonCopy } from '$lib/api/season-copy';

	let { children } = $props();

	// Whichever page a visitor opens first, their starter season is copied
	// before they list episodes or start a take. The welcome page makes the
	// same read itself and shows its answer. Two reads at once could race
	// the server's copy, so the layout leaves that page alone.
	$effect(() => {
		if (!browser || page.url.pathname.startsWith('/welcome')) return;
		void ensureSeasonCopy();
	});
</script>

{@render children()}
