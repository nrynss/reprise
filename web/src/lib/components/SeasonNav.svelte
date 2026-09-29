<script lang="ts">
	import { resolve } from '$app/paths';
	import AccountLink from '$lib/components/AccountLink.svelte';

	// The season tabs shared by the gallery and the threads pages. The
	// tabs ride the shared button classes, so the current tab fills
	// while the other stays outlined. The episode page passes none, so
	// both tabs stay plain beside the filled Edit pill it renders
	// itself. The row scrolls sideways on a phone through the shared
	// tabs class. The fixture flag keeps the scripted season behind
	// its query string. The anchors carry both the tab and the shared
	// secondary pill, so the older tab pins and the one button rule
	// agree on their height and shape. The props carry current
	// (gallery, threads, or none) and fixture. The account link trails
	// the tabs. Guests read Sign in, and a signed in device reads
	// Account.
	let { current, fixture = false } = $props();
</script>

<div class="tabs">
	<a
		class="tab button secondary"
		class:active={current === 'gallery'}
		href={fixture ? resolve('/?fixture=1') : resolve('/')}
		aria-current={current === 'gallery' ? 'page' : undefined}>Gallery</a
	>
	<a
		class="tab button secondary"
		class:active={current === 'threads'}
		href={fixture ? resolve('/threads?fixture=1') : resolve('/threads')}
		aria-current={current === 'threads' ? 'page' : undefined}>Threads</a
	>
	<AccountLink />
</div>
